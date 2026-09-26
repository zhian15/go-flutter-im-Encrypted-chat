package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"
	"gorm.io/gorm"
)

// ========== 支付配置（sys_config:pay_config） ==========

type PayConfig struct {
	Enabled                bool        `json:"enabled"`
	ReceiveWechatQrcodeURL string      `json:"receiveWechatQrcodeUrl"`
	ReceiveAlipayQrcodeURL string      `json:"receiveAlipayQrcodeUrl"`
	ReceiveBankQrcodeURL   string      `json:"receiveBankQrcodeUrl"`
	ReceiveBankInfo        PayBankInfo `json:"receiveBankInfo"`
	RechargeTips           string      `json:"rechargeTips"`
	WithdrawEnabled        bool        `json:"withdrawEnabled"`
	WithdrawMin            float64     `json:"withdrawMin"`
	WithdrawMax            float64     `json:"withdrawMax"`
	WithdrawFeeRate        float64     `json:"withdrawFeeRate"` // 0 ~ 0.1
	WithdrawFeeMin         float64     `json:"withdrawFeeMin"`
}
type PayBankInfo struct {
	BankName    string `json:"bankName"`
	CardNo      string `json:"cardNo"`
	AccountName string `json:"accountName"`
}

func defaultPayConfig() PayConfig {
	return PayConfig{
		Enabled:         true,
		RechargeTips:    "请扫码向平台支付对应金额，并上传支付凭证，审核通过后余额会自动到账。",
		WithdrawEnabled: true,
		WithdrawMin:     10,
		WithdrawMax:     50000,
		WithdrawFeeRate: 0,
		WithdrawFeeMin:  0,
	}
}

// PayConfigGet 读 sys_config.pay_config；取不到返回默认值
func PayConfigGet(ctx context.Context) PayConfig {
	v := SysConfigGet(ctx, "pay_config", nil)
	cfg := defaultPayConfig()
	if v == nil {
		return cfg
	}
	// v 可能是 map[string]any（SysConfigGet 里对 JSON 对象解包返回 value 或本体）
	// 粗暴 json roundtrip 到 PayConfig
	if m, ok := v.(map[string]any); ok {
		if b, err := json.Marshal(m); err == nil {
			_ = json.Unmarshal(b, &cfg)
		}
	}
	return cfg
}

// PayConfigSet 保存支付配置（会 validate 字段范围）
func PayConfigSet(ctx context.Context, cfg PayConfig) error {
	if cfg.WithdrawFeeRate < 0 {
		cfg.WithdrawFeeRate = 0
	}
	if cfg.WithdrawFeeRate > 0.1 {
		cfg.WithdrawFeeRate = 0.1
	}
	if cfg.WithdrawMin < 0.01 {
		cfg.WithdrawMin = 0.01
	}
	if cfg.WithdrawMax < cfg.WithdrawMin {
		cfg.WithdrawMax = cfg.WithdrawMin
	}
	if cfg.WithdrawFeeMin < 0 {
		cfg.WithdrawFeeMin = 0
	}
	return SysConfigSet(ctx, "pay_config", cfg)
}

// ReceiveQrcodeByPayMethod 按支付方式(1微信 2支付宝 3银行卡)取对应的收款码 URL
func (p PayConfig) ReceiveQrcodeByPayMethod(payMethod int) string {
	switch payMethod {
	case model.PayMethodWeChat:
		return p.ReceiveWechatQrcodeURL
	case model.PayMethodAliPay:
		return p.ReceiveAlipayQrcodeURL
	case model.PayMethodBank:
		return p.ReceiveBankQrcodeURL
	}
	return ""
}

// ========== 用户侧充值 ==========

// UserRechargeSubmit 用户提交充值订单：上传支付凭证截图(proofImage) 等。
// 下单时会把"当前后台收款码 URL"保存快照，避免后台换码后对账混乱。
func UserRechargeSubmit(ctx context.Context, userID int64, amount float64, payMethod int,
	proofImage, payTxNo, remark string) (map[string]any, error) {
	cfg := PayConfigGet(ctx)
	if !cfg.Enabled {
		return nil, &errs.Err{Code: 1001, Msg: "当前未启用充值通道"}
	}
	amount = round2(amount)
	if amount < 0.01 {
		return nil, &errs.Err{Code: 1001, Msg: "充值金额必须大于 0"}
	}
	if payMethod < model.PayMethodWeChat || payMethod > model.PayMethodBank {
		return nil, &errs.Err{Code: 1001, Msg: "支付方式不支持"}
	}
	if strings.TrimSpace(proofImage) == "" {
		return nil, &errs.Err{Code: 1001, Msg: "请上传支付凭证截图"}
	}
	// 幂等保护：600s 内同用户 同金额 同凭证图片 hash，不会重复
	//    （简化：若 600s 内已存在 pending order 同 user/amount/payMethod/ProofImage，直接返回该条）
	var dup model.RechargeOrder
	store.DB.Where("user_id = ? AND amount = ? AND pay_method = ? AND proof_image = ? AND status = ? AND created_at >= ?",
		userID, amount, payMethod, proofImage, model.OrderStatusPending, time.Now().Add(-600*time.Second)).
		Order("id DESC").First(&dup)
	if dup.ID > 0 {
		return map[string]any{"id": dup.ID, "status": dup.Status, "tips": cfg.RechargeTips}, nil
	}
	o := model.RechargeOrder{
		UserID:           userID,
		Amount:           amount,
		PayMethod:        payMethod,
		ReceiveQrcodeURL: cfg.ReceiveQrcodeByPayMethod(payMethod),
		ProofImage:       proofImage,
		PayTxNo:          payTxNo,
		Status:           model.OrderStatusPending,
		Remark:           remark,
		CreatedAt:        time.Now(),
	}
	if err := store.DB.Create(&o).Error; err != nil {
		return nil, err
	}
	return map[string]any{"id": o.ID, "status": o.Status, "tips": cfg.RechargeTips}, nil
}

// UserRechargeList 用户看自己的充值订单
func UserRechargeList(ctx context.Context, userID int64, page, size int) ([]model.RechargeOrder, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	q := store.DB.Model(&model.RechargeOrder{}).Where("user_id = ?", userID)
	q.Count(&total)
	var list []model.RechargeOrder
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ========== 用户侧提现绑定 ==========

// UserWithdrawAccountSave 保存用户提现绑定（按 account_type 校验必填，其它类型字段清空为默认空）
func UserWithdrawAccountSave(ctx context.Context, userID int64, wa *model.WithdrawAccount) error {
	if wa == nil || userID <= 0 {
		return errs.ParamError
	}
	switch wa.AccountType {
	case 1: // 微信
		if strings.TrimSpace(wa.WechatName) == "" {
			return &errs.Err{Code: 1001, Msg: "请填写微信收款姓名"}
		}
	case 2: // 支付宝
		if strings.TrimSpace(wa.AlipayAccount) == "" || strings.TrimSpace(wa.AlipayName) == "" {
			return &errs.Err{Code: 1001, Msg: "请填写支付宝账号和姓名"}
		}
	case 3: // 银行卡
		if strings.TrimSpace(wa.BankCardNo) == "" || strings.TrimSpace(wa.BankName) == "" || strings.TrimSpace(wa.BankAccountName) == "" {
			return &errs.Err{Code: 1001, Msg: "请填写银行卡号、开户银行、开户姓名"}
		}
	default:
		return &errs.Err{Code: 1001, Msg: "提现方式不支持"}
	}
	// 不保存未选中类型的脏数据
	if wa.AccountType != 1 {
		wa.WechatQrcodeURL = ""
		wa.WechatName = ""
	}
	if wa.AccountType != 2 {
		wa.AlipayQrcodeURL = ""
		wa.AlipayAccount = ""
		wa.AlipayName = ""
	}
	if wa.AccountType != 3 {
		wa.BankCardNo = ""
		wa.BankName = ""
		wa.BankAccountName = ""
	}
	now := time.Now()
	// upsert on user_id unique
	var existing model.WithdrawAccount
	err := store.DB.Where("user_id = ?", userID).First(&existing).Error
	if err != nil {
		// 新建
		wa.ID = 0
		wa.UserID = userID
		wa.CreatedAt = now
		wa.UpdatedAt = now
		return store.DB.Create(wa).Error
	}
	wa.ID = existing.ID
	wa.UserID = userID
	wa.CreatedAt = existing.CreatedAt
	wa.UpdatedAt = now
	return store.DB.Model(&existing).Updates(map[string]any{
		"account_type":      wa.AccountType,
		"wechat_qrcode_url": wa.WechatQrcodeURL,
		"wechat_name":       wa.WechatName,
		"alipay_qrcode_url": wa.AlipayQrcodeURL,
		"alipay_account":    wa.AlipayAccount,
		"alipay_name":       wa.AlipayName,
		"bank_card_no":      wa.BankCardNo,
		"bank_name":         wa.BankName,
		"bank_account_name": wa.BankAccountName,
		"updated_at":        now,
	}).Error
}

// UserWithdrawAccountGet 读用户绑定（没有则返回空对象+nil，前端据此填默认）
func UserWithdrawAccountGet(ctx context.Context, userID int64) (*model.WithdrawAccount, error) {
	var wa model.WithdrawAccount
	if err := store.DB.Where("user_id = ?", userID).First(&wa).Error; err != nil {
		// 未绑定不报错
		wa.UserID = userID
		return &wa, nil
	}
	return &wa, nil
}

// ========== 用户侧提现订单 ==========

// calcWithdrawFee 计算提现手续费：max(amount*rate, feeMin)
func calcWithdrawFee(amount float64, cfg PayConfig) float64 {
	f := round2(amount * cfg.WithdrawFeeRate)
	if f < cfg.WithdrawFeeMin {
		f = round2(cfg.WithdrawFeeMin)
	}
	return f
}

// UserWithdrawSubmit 用户提交提现：balance → frozen，写 withdraw_order status=1
func UserWithdrawSubmit(ctx context.Context, userID int64, amount float64, withdrawType int) (map[string]any, error) {
	cfg := PayConfigGet(ctx)
	if !cfg.WithdrawEnabled {
		return nil, &errs.Err{Code: 1001, Msg: "当前未开启提现"}
	}
	amount = round2(amount)
	if amount < round2(cfg.WithdrawMin) {
		return nil, &errs.Err{Code: 1001, Msg: fmt.Sprintf("单笔提现最低 %.2f 元", cfg.WithdrawMin)}
	}
	if amount > round2(cfg.WithdrawMax) {
		return nil, &errs.Err{Code: 1001, Msg: fmt.Sprintf("单笔提现最高 %.2f 元", cfg.WithdrawMax)}
	}
	if withdrawType < 1 || withdrawType > 3 {
		return nil, &errs.Err{Code: 1001, Msg: "提现方式不支持"}
	}
	// 必须绑定了对应提现方式
	wa, err := UserWithdrawAccountGet(ctx, userID)
	if err != nil {
		return nil, err
	}
	if wa.AccountType != withdrawType {
		return nil, &errs.Err{Code: 1001, Msg: "请先绑定该提现方式的收款账户"}
	}
	switch withdrawType {
	case 1:
		if strings.TrimSpace(wa.WechatName) == "" {
			return nil, &errs.Err{Code: 1001, Msg: "请先完善微信收款账户"}
		}
	case 2:
		if strings.TrimSpace(wa.AlipayAccount) == "" || strings.TrimSpace(wa.AlipayName) == "" {
			return nil, &errs.Err{Code: 1001, Msg: "请先完善支付宝收款账户"}
		}
	case 3:
		if strings.TrimSpace(wa.BankCardNo) == "" || strings.TrimSpace(wa.BankName) == "" || strings.TrimSpace(wa.BankAccountName) == "" {
			return nil, &errs.Err{Code: 1001, Msg: "请先完善银行卡信息"}
		}
	}

	fee := calcWithdrawFee(amount, cfg)
	actualAmount := round2(amount - fee)
	if actualAmount < 0 {
		actualAmount = 0
	}
	snap := model.JSONBlob(map[string]any{
		"accountType":     wa.AccountType,
		"wechatQrcodeUrl": wa.WechatQrcodeURL,
		"wechatName":      wa.WechatName,
		"alipayQrcodeUrl": wa.AlipayQrcodeURL,
		"alipayAccount":   wa.AlipayAccount,
		"alipayName":      wa.AlipayName,
		"bankCardNo":      maskCard(wa.BankCardNo), // 卡号/账号 mask 存快照，管理员列表也能看到 mask
		"bankCardNoFull":  wa.BankCardNo,           // 原始卡号（仅审核时用，接口返回按需要 mask）
		"bankName":        wa.BankName,
		"bankAccountName": wa.BankAccountName,
	})

	// ========== 原子：建订单 + 冻结（同一个事务）==========
	var oid int64
	err = store.DB.Transaction(func(tx *gorm.DB) error {
		o := &model.WithdrawOrder{
			UserID:          userID,
			Amount:          amount,
			Fee:             fee,
			ActualAmount:    actualAmount,
			WithdrawType:    withdrawType,
			AccountSnapshot: snap,
			Status:          model.OrderStatusPending,
			CreatedAt:       time.Now(),
		}
		if err := tx.Create(o).Error; err != nil {
			return err
		}
		oid = o.ID
		// 申请提现：可用余额 -amount，冻结 +amount（手续费不从冻结扣；审核通过时 frozen-amount、再从 balance 扣 -fee 即可）
		return WalletMutateInTx(tx, userID, -amount, +amount,
			model.WalletTxFreeze, "提现申请冻结", fmt.Sprintf("提现单号 #%d", o.ID),
			fmt.Sprintf("wd:%d", o.ID), 0)
	})
	if err != nil {
		return nil, err
	}
	PublishWalletUpdate(ctx, userID)
	return map[string]any{
		"id":           oid,
		"amount":       amount,
		"fee":          fee,
		"actualAmount": actualAmount,
		"status":       model.OrderStatusPending,
	}, nil
}

// maskCard 账号/卡号脱敏：仅保留前 4 + 后 4，中间 *
func maskCard(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 8 {
		return s
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}

// UserWithdrawList 用户自己的提现订单
func UserWithdrawList(ctx context.Context, userID int64, page, size int) ([]model.WithdrawOrder, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	q := store.DB.Model(&model.WithdrawOrder{}).Where("user_id = ?", userID)
	q.Count(&total)
	var list []model.WithdrawOrder
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ========== 后台：充值订单 ==========

// AdminRechargeOrderList 后台充值订单列表，支持 kw(用户账号/昵称/短ID) + status 筛选
func AdminRechargeOrderList(ctx context.Context, kw string, status int, page, size int) ([]map[string]any, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	q := store.DB.Table("recharge_order o").
		Select("o.id, o.user_id, o.amount, o.pay_method, o.receive_qrcode_url, o.proof_image, o.pay_tx_no, o.status, o.reject_reason, o.reviewer_id, o.reviewed_at, o.remark, o.created_at, " +
			"u.account user_account, u.nickname user_nickname, u.short_id user_short_id").
		Joins("LEFT JOIN user u ON u.id = o.user_id")
	if kw != "" {
		q = q.Where("u.account LIKE ? OR u.nickname LIKE ? OR u.short_id LIKE ? OR o.pay_tx_no LIKE ?", "%"+kw+"%", "%"+kw+"%", "%"+kw+"%", "%"+kw+"%")
	}
	if status > 0 {
		q = q.Where("o.status = ?", status)
	}
	var total int64
	q.Count(&total)
	rows := make([]map[string]any, 0, size)
	if err := q.Order("o.id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	// column 名从 gorm 查询结果下划线 → 驼峰
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"id":               r["id"],
			"userId":           r["user_id"],
			"amount":           r["amount"],
			"payMethod":        r["pay_method"],
			"receiveQrcodeUrl": r["receive_qrcode_url"],
			"proofImage":       r["proof_image"],
			"payTxNo":          r["pay_tx_no"],
			"status":           r["status"],
			"rejectReason":     r["reject_reason"],
			"reviewerId":       r["reviewer_id"],
			"reviewedAt":       r["reviewed_at"],
			"remark":           r["remark"],
			"createdAt":        r["created_at"],
			"userAccount":      r["user_account"],
			"userNickname":     r["user_nickname"],
			"userShortId":      r["user_short_id"],
		})
	}
	return out, total, nil
}

// AdminRechargeOrderApprove 后台通过充值：给用户加余额(= recharge)，写流水
func AdminRechargeOrderApprove(ctx context.Context, orderID int64, reviewerID int64) (map[string]any, error) {
	var (
		order   model.RechargeOrder
		newBal  float64
		userID  int64
		amountF float64
	)
	err := store.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&order, orderID).Error; err != nil {
			return errs.ParamError
		}
		if order.Status == model.OrderStatusApproved {
			// 幂等
			return nil
		}
		if order.Status != model.OrderStatusPending {
			return &errs.Err{Code: 1001, Msg: "该订单已处理，不能重复审核"}
		}
		userID = order.UserID
		amountF = order.Amount
		// 加余额；WalletApply 是独立事务，这里组合到同一个事务里 → 直接 WalletMutateInTx
		refID := "recharge:" + strconv.FormatInt(order.ID, 10)
		if err := WalletMutateInTx(tx, userID, +amountF, 0,
			model.WalletTxRecharge, "用户充值审核通过",
			fmt.Sprintf("充值订单 #%d", order.ID), refID, reviewerID); err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&order).Updates(map[string]any{
			"status":      model.OrderStatusApproved,
			"reviewer_id": reviewerID,
			"reviewed_at": now,
		}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	PublishWalletUpdate(ctx, userID)
	// 充值审核通过 → 小助手系统提醒（uid=-1；失败不影响审核结果，但要留日志排障）
	if nerr := AssistantNotify(ctx, userID, fmt.Sprintf("充值成功：您的充值 ¥%.2f 已到账，请留意余额变化。", amountF)); nerr != nil {
		log.Printf("[Assistant] recharge notify failed: order=%d user=%d err=%v", orderID, userID, nerr)
	}
	return map[string]any{
		"orderId": orderID,
		"userId":  userID,
		"amount":  amountF,
		"balance": newBal,
	}, nil
}

// AdminRechargeOrderReject 后台驳回充值：不扣钱；把订单置 3 + 原因
func AdminRechargeOrderReject(ctx context.Context, orderID int64, reviewerID int64, reason string) error {
	return store.DB.Transaction(func(tx *gorm.DB) error {
		var order model.RechargeOrder
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&order, orderID).Error; err != nil {
			return errs.ParamError
		}
		if order.Status != model.OrderStatusPending {
			return &errs.Err{Code: 1001, Msg: "该订单已处理，不能重复审核"}
		}
		now := time.Now()
		return tx.Model(&order).Updates(map[string]any{
			"status":        model.OrderStatusRejected,
			"reject_reason": reason,
			"reviewer_id":   reviewerID,
			"reviewed_at":   now,
		}).Error
	})
}

// ========== 后台：提现订单 ==========

// AdminWithdrawOrderList 后台提现订单列表，含账号/昵称/短ID 和 账户快照
func AdminWithdrawOrderList(ctx context.Context, kw string, status int, wType int, page, size int) ([]map[string]any, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	q := store.DB.Table("withdraw_order o").
		Select("o.id, o.user_id, o.amount, o.fee, o.actual_amount, o.withdraw_type, o.account_snapshot, o.status, o.reject_reason, o.reviewer_id, o.reviewed_at, o.remark, o.created_at, " +
			"u.account user_account, u.nickname user_nickname, u.short_id user_short_id").
		Joins("LEFT JOIN user u ON u.id = o.user_id")
	if kw != "" {
		q = q.Where("u.account LIKE ? OR u.nickname LIKE ? OR u.short_id LIKE ?", "%"+kw+"%", "%"+kw+"%", "%"+kw+"%")
	}
	if status > 0 {
		q = q.Where("o.status = ?", status)
	}
	if wType > 0 {
		q = q.Where("o.withdraw_type = ?", wType)
	}
	var total int64
	q.Count(&total)
	type Row struct {
		ID              int64      `json:"id"`
		UserID          int64      `json:"userId"`
		Amount          float64    `json:"amount"`
		Fee             float64    `json:"fee"`
		ActualAmount    float64    `json:"actualAmount"`
		WithdrawType    int        `json:"withdrawType"`
		AccountSnapshot []byte     `gorm:"column:account_snapshot" json:"-"`
		Status          int        `json:"status"`
		RejectReason    string     `json:"rejectReason"`
		ReviewerID      int64      `json:"reviewerId"`
		ReviewedAt      *time.Time `json:"reviewedAt"`
		Remark          string     `json:"remark"`
		CreatedAt       time.Time  `json:"createdAt"`
		UserAccount     string     `gorm:"column:user_account"`
		UserNickname    string     `gorm:"column:user_nickname"`
		UserShortID     *string    `gorm:"column:user_short_id"`
	}
	var rows []Row
	if err := q.Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		var snap model.JSONBlob
		_ = snap.Scan(r.AccountSnapshot)
		out = append(out, map[string]any{
			"id":              r.ID,
			"userId":          r.UserID,
			"userAccount":     r.UserAccount,
			"userNickname":    r.UserNickname,
			"userShortId":     model.StrVal(r.UserShortID),
			"amount":          r.Amount,
			"fee":             r.Fee,
			"actualAmount":    r.ActualAmount,
			"withdrawType":    r.WithdrawType,
			"accountSnapshot": snap,
			"status":          r.Status,
			"rejectReason":    r.RejectReason,
			"reviewerId":      r.ReviewerID,
			"reviewedAt":      r.ReviewedAt,
			"remark":          r.Remark,
			"createdAt":       r.CreatedAt,
		})
	}
	return out, total, nil
}

// AdminWithdrawOrderApprove 后台确认提现：
//   - frozen -amount（释放冻结的提现本金）
//   - balance -fee（手续费真正扣掉，用户提现 100 手续费 1 → 冻结 100，实际到账 99）
//     → 这样用户总余额变化：balance -amount -fee + frozen -(-amount) = -amount -fee + amount = -fee?
//     不，用户提现 100，最终资产应该是 balance-100、frozen-0（原本冻结 100）+ 手续费 -1
//     正确账户变化（通过审核 = 提现成功）：
//     frozen  -100（不再冻结）
//     balance -100（真的扣本金，而之前申请时是 balance -100 已发生）
//     实际上申请时我们做的是 balance -amount + frozen +amount，所以 总资产 = balance+frozen 不变。
//     通过审核时：frozen -amount（把冻结"释放"，但钱是要真走的，所以不再加回 balance）
//     再 balance -fee（手续费从余额里扣。申请时没扣手续费，余额正好 = 申请前 - amount，够扣 fee 吗？若用户提交时余额 = amount，手续费就会余额不足 4101。
//     → 更稳妥做法：提交申请时就把 amount+fee 一起冻结，审核通过 frozen -(amount+fee)、balance 不动；驳回就 frozen +(amount+fee) balance +(amount+fee)。
//     但前面提交实现已按 amount 冻结，为了避免迁移，这里处理成：
//     审核通过时：frozen -amount（释放冻结，不回 balance 表示钱已出款）
//     再 WalletApply 扣 -fee（余额不足报错，由管理员看到让用户再充值或修改手续费配置）。
func AdminWithdrawOrderApprove(ctx context.Context, orderID int64, reviewerID int64) (map[string]any, error) {
	var (
		order   model.WithdrawOrder
		userID  int64
		amountF float64
		feeF    float64
	)
	err := store.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&order, orderID).Error; err != nil {
			return errs.ParamError
		}
		if order.Status == model.OrderStatusApproved {
			return nil
		}
		if order.Status != model.OrderStatusPending {
			return &errs.Err{Code: 1001, Msg: "该订单已处理，不能重复审核"}
		}
		userID = order.UserID
		amountF = order.Amount
		feeF = order.Fee
		refID := "withdraw:" + strconv.FormatInt(order.ID, 10)
		// Step 1: 冻结扣本金（frozen - amount）。这部分不影响总资产（资产 = balance+frozen：frozen少了但相当于已经走款，不再回 balance）
		if err := WalletMutateInTx(tx, userID, 0, -amountF,
			model.WalletTxWithdraw, "提现审核通过-本金出款",
			fmt.Sprintf("提现订单 #%d", order.ID), refID, reviewerID); err != nil {
			return err
		}
		// Step 2: 若手续费 > 0，从余额单独扣（如果申请时余额刚好 = amount 会失败，这里要报错给管理员）
		if feeF > 0.0001 {
			if err := WalletMutateInTx(tx, userID, -feeF, 0,
				model.WalletTxWithdraw, "提现审核通过-手续费扣除",
				fmt.Sprintf("提现订单 #%d", order.ID), refID, reviewerID); err != nil {
				return err
			}
		}
		now := time.Now()
		return tx.Model(&order).Updates(map[string]any{
			"status":      model.OrderStatusApproved,
			"reviewer_id": reviewerID,
			"reviewed_at": now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	PublishWalletUpdate(ctx, userID)
	// 提现审核通过 → 小助手系统提醒（uid=-1；金额用扣除手续费后的实际到账额，比打款额更贴近用户感知）
	if nerr := AssistantNotify(ctx, userID, fmt.Sprintf("提现成功：您的提现 ¥%.2f 已完成打款，实际到账 ¥%.2f。", amountF, amountF-feeF)); nerr != nil {
		log.Printf("[Assistant] withdraw notify failed: order=%d user=%d err=%v", orderID, userID, nerr)
	}
	return map[string]any{
		"orderId": orderID,
		"userId":  userID,
		"amount":  amountF,
		"fee":     feeF,
	}, nil
}

// AdminWithdrawOrderReject 后台驳回提现：把申请时冻结的 amount 解冻退回 balance
func AdminWithdrawOrderReject(ctx context.Context, orderID int64, reviewerID int64, reason string) error {
	var order model.WithdrawOrder
	err := store.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&order, orderID).Error; err != nil {
			return errs.ParamError
		}
		if order.Status != model.OrderStatusPending {
			return &errs.Err{Code: 1001, Msg: "该订单已处理，不能重复审核"}
		}
		refID := "withdraw:" + strconv.FormatInt(order.ID, 10)
		// 冻结退回：frozen -amount, balance +amount
		if err := WalletMutateInTx(tx, order.UserID, +order.Amount, -order.Amount,
			model.WalletTxUnfreeze, "提现申请驳回-解冻退回",
			fmt.Sprintf("提现订单 #%d，原因：%s", order.ID, reason), refID, reviewerID); err != nil {
			return err
		}
		now := time.Now()
		return tx.Model(&order).Updates(map[string]any{
			"status":        model.OrderStatusRejected,
			"reject_reason": reason,
			"reviewer_id":   reviewerID,
			"reviewed_at":   now,
		}).Error
	})
	if err != nil {
		return err
	}
	PublishWalletUpdate(ctx, order.UserID)
	return nil
}
