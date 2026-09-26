package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"
	"go.mongodb.org/mongo-driver/bson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============ 钱包 ============
// 余额存 user.balance；每次变动写 wallet_transaction 流水（余额与流水强一致）

// WalletApply 钱包变动（唯一入口）：delta 正=入账 负=支出，返回变动后余额
//
// B-24：改余额与写流水必须在**同一个事务**里。
// 以前两步分开，若流水写入失败（连接抖动/磁盘满）就会出现
// 「余额加了、流水没有」→ 后台财务页与 /admin/wallet/reconcile 对不上账。
func WalletApply(ctx context.Context, userID int64, delta float64, txType, title, remark, refID string, operator int64) (float64, error) {
	if delta == 0 {
		return 0, errs.ParamError
	}
	var newBalance float64
	err := store.DB.Transaction(func(db *gorm.DB) error {
		var u model.User
		// 行锁取当前余额（必须锁，否则并发加款会互相覆盖）
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, userID).Error; err != nil {
			return errs.ParamError
		}
		nb := u.Balance + delta
		if nb < 0 {
			return &errs.Err{Code: 4101, Msg: "余额不足"}
		}
		if err := db.Model(&model.User{}).Where("id = ?", userID).
			Update("balance", nb).Error; err != nil {
			return err
		}
		tx := model.WalletTransaction{
			UserID:      userID,
			Type:        txType,
			Amount:      delta,
			FrozenDelta: 0, // 只动可用余额，冻结额不变
			Balance:     nb,
			Frozen:      u.Frozen,
			Title:       title,
			Remark:      remark,
			RefID:       refID,
			Operator:    operator,
		}
		if err := db.Create(&tx).Error; err != nil {
			return err
		}
		newBalance = nb
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newBalance, nil
}

// AdminUserWallet 后台查询某用户的真实钱包（余额 + 冻结）。
//
// B-24：后台「用户管理 → 充值」以前调的接口不存在，前端静默 fallback 写 meta，
// 导致后台显示成功、App 端余额纹丝不动。这里给出权威数据源，前端只许读它。
func AdminUserWallet(ctx context.Context, userID int64) (map[string]interface{}, error) {
	var u model.User
	if err := store.DB.First(&u, userID).Error; err != nil {
		return nil, errs.ParamError
	}
	return map[string]interface{}{
		"userId":  u.ID,
		"balance": u.Balance,
		"frozen":  u.Frozen,
	}, nil
}

// AdminUserRecharge 后台给用户充值 / 扣款（统一入口）。
//
//   - amount > 0：充值，写 `recharge` 流水，标题「后台充值」
//   - amount < 0：扣款，写 `adjust`   流水，标题「后台扣款」（余额不足会被 WalletApply 拒绝，返回 4101）
//   - amount = 0：参数错误
//
// 与独立的 `wallet/adjust` 区别：这里是「用户管理」页的常用操作，管理员不需要
// 切到财务管理页就能直接加减钱；账务分类保持正确（充值走 recharge、扣款走 adjust），
// 财务报表按类型聚合时不会混淆。
func AdminUserRecharge(ctx context.Context, userID int64, amount float64, remark string, operator int64) (map[string]interface{}, error) {
	if userID <= 0 || amount == 0 {
		return nil, errs.ParamError
	}
	var (
		txType string
		title  string
	)
	if amount > 0 {
		txType = model.WalletTxRecharge
		title = "后台充值"
		if remark == "" {
			remark = "管理员手动充值"
		}
	} else {
		txType = model.WalletTxAdjust
		title = "后台扣款"
		if remark == "" {
			remark = "管理员手动扣款"
		}
	}
	balance, err := WalletApply(ctx, userID, amount, txType, title, remark, "", operator)
	if err != nil {
		return nil, err
	}
	// 变动后立刻推给在线客户端，App 不用切页面/重进就能看到新余额
	PublishWalletUpdate(ctx, userID)
	return map[string]interface{}{"balance": balance}, nil
}

// ============ 发红包 / 转账：扣款 → 冻结（B-19 → B-22）============
// 历史背景：以前发红包(type=8) / 转账(type=9) 时服务端**不扣款**，靠客户端事后记账 → 白嫖。
// B-19 改成服务端落库前扣款，但又暴露出新问题：没人领的钱**凭空蒸发**，
// 而且领取是凭空入账，只要没人领就能一直发一直领 = 刷钱。
// B-22 改为**冻结制**：balance → frozen → 收款人 balance，24h 未领完自动退回。
// 实现见 money_packet.go：SendMoneyFreeze / SettleClaim / RefundExpiredPackets。

// WalletMe 我的钱包：余额 + 交易记录
func WalletMe(ctx context.Context, userID int64) (map[string]interface{}, error) {
	var u model.User
	if err := store.DB.First(&u, userID).Error; err != nil {
		return nil, errs.ParamError
	}
	var list []model.WalletTransaction
	// 与 WalletRecords 同规则：过滤 amount=0 的纯冻结内部流转（settle +0 记录），
	// 否则钱包首页交易记录会出现「红包被领取 +0」
	store.DB.Where("user_id = ? AND amount <> ?", userID, 0).
		Order("id DESC").Limit(100).Find(&list)
	return map[string]interface{}{
		"balance": u.Balance,
		"frozen":  u.Frozen, // 冻结金额（发出的红包/转账还没被领走的部分）
		// 必须走 walletTxOut 序列化：createdAt 格式化为 "2006-01-02 15:04:05" +
		// 补 typeName 中文名。直接返回裸 model 会把 RFC3339 原串
		// （2026-09-06T20:58:55.933Z）透传给 App，钱包页时间显示成 ISO 格式
		"records": walletTxOut(list),
	}, nil
}

// PublishWalletUpdate 把最新余额推给在线客户端，让 App **立刻**刷新（B-24）
//
// 背景：余额展示以前只有两个触发点——「我的」页 initState 拉一次（IndexedStack 常驻、
// 页面永不销毁，所以永远只有第一次），以及**点击**「我的」tab 时拉一次。
// 于是站在「我的」页上不动、后台给用户加多少钱，App 都毫无反应。
//
// 现在凡是余额/冻结发生变动，服务端都主动推一条 `wallet` 事件，
// 客户端 WS 收到后直接 refresh —— 加款、红包被领、到期退回全都是实时的。
// 走的是已有的 Redis Pub/Sub → gateway → WS 通道（见 ws.go 的 PublishEvent）。
func PublishWalletUpdate(ctx context.Context, userIDs ...int64) {
	ids := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 || store.RDB == nil {
		return
	}
	var users []model.User
	if err := store.DB.Where("id IN ?", ids).Find(&users).Error; err != nil {
		return
	}
	for _, u := range users {
		payload, err := json.Marshal(map[string]interface{}{
			"balance": u.Balance,
			"frozen":  u.Frozen,
		})
		if err != nil {
			continue
		}
		// 推送失败不影响主流程（客户端还有进入页面时的兜底拉取）
		_ = PublishEvent(ctx, &Event{
			Type:   "wallet",
			ToUIDs: []int64{u.ID},
			Data:   payload,
		})
	}
}

// AdminWalletAdjust 后台手工调整余额（delta 正=加 负=减），写 adjust 流水
func AdminWalletAdjust(ctx context.Context, targetUID int64, delta float64, reason string, operator int64) (float64, error) {
	if targetUID <= 0 {
		return 0, errs.ParamError
	}
	title := "手工调整"
	if delta > 0 {
		title = "后台加款"
	} else {
		title = "后台扣款"
	}
	return WalletApply(ctx, targetUID, delta, model.WalletTxAdjust, title, reason, "", operator)
}

// AdminWalletTxList 后台流水列表（type 过滤：recharge/withdraw/adjust/空=全部）
func AdminWalletTxList(ctx context.Context, txType string, page, size int) ([]model.WalletTransaction, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	q := store.DB.Model(&model.WalletTransaction{})
	if txType != "" {
		q = q.Where("type = ?", txType)
	}
	var total int64
	q.Count(&total)
	var list []model.WalletTransaction
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ============ 财务记录（后台「财务管理」页，B-24）============
//
// 以前财务页调 /admin/finances，后端**没有这个路由**，前端 catch 后直接
// 用 buildMockFinanceRecords() 顶上一堆随机假数据 —— 页面看着很热闹，
// 实际跟数据库一点关系都没有，这正是「财务记录对不上」的根因。
// 现在改成直接读 wallet_transaction，页面上每一分钱都能在流水表里对上。

// financeTypeGroups 前端财务类型 → 后端流水类型
var financeTypeGroups = map[string][]string{
	"RECHARGE":  {model.WalletTxRecharge},
	"WITHDRAW":  {model.WalletTxWithdraw},
	"TRANSFER":  {model.WalletTxTrOut, model.WalletTxTrIn},
	"REDPACKET": {model.WalletTxRedOut, model.WalletTxRedIn},
	"REFUND":    {model.WalletTxRedOutRefund, model.WalletTxTrOutRefund},
	"FREEZE":    {model.WalletTxFreeze, model.WalletTxUnfreeze, model.WalletTxSettle},
	"OTHER":     {model.WalletTxAdjust},
}

func financeTypeOf(t string) string {
	for ft, list := range financeTypeGroups {
		for _, x := range list {
			if x == t {
				return ft
			}
		}
	}
	return "OTHER"
}

// AdminFinanceList 财务记录列表（wallet_transaction → 财务页字段）
//
// 只展示 **amount != 0** 的流水（即对可用余额有实际影响的），
// 纯冻结内部流转（settle：frozen 减少、balance 不变）不展示，
// 这样列表金额合计恒等于 Σ(user.balance)，与 /admin/wallet/reconcile 一致。
func AdminFinanceList(ctx context.Context, kw, side, finType string, from, to int64, page, size int) ([]map[string]any, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	q := store.DB.Model(&model.WalletTransaction{}).Where("amount <> ?", 0)
	if side == "IN" {
		q = q.Where("amount > ?", 0)
	} else if side == "OUT" {
		q = q.Where("amount < ?", 0)
	}
	if g, ok := financeTypeGroups[finType]; ok && len(g) > 0 {
		q = q.Where("type IN ?", g)
	} else if finType != "" {
		q = q.Where("type = ?", finType)
	}
	if from > 0 {
		q = q.Where("created_at >= ?", time.UnixMilli(from))
	}
	if to > 0 {
		q = q.Where("created_at <= ?", time.UnixMilli(to))
	}
	if kw != "" {
		like := "%" + kw + "%"
		// 用户字段走子查询：允许按账号/昵称/靓号搜
		sub := store.DB.Model(&model.User{}).Select("id").
			Where("account LIKE ? OR nickname LIKE ? OR short_id LIKE ?", like, like, like)
		q = q.Where("user_id IN (?) OR remark LIKE ? OR title LIKE ?", sub, like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.WalletTransaction
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	// 批量取用户信息，避免 N+1
	ids := make([]int64, 0, len(list))
	for _, t := range list {
		ids = append(ids, t.UserID)
	}
	users := make(map[int64]model.User, len(ids))
	if len(ids) > 0 {
		var us []model.User
		store.DB.Where("id IN ?", ids).Find(&us)
		for _, u := range us {
			users[u.ID] = u
		}
	}
	out := make([]map[string]any, 0, len(list))
	for _, t := range list {
		u := users[t.UserID]
		amt := t.Amount
		if amt < 0 {
			amt = -amt
		}
		remark := t.Remark
		if remark == "" {
			remark = t.Title
		}
		out = append(out, map[string]any{
			"id":           t.ID,
			"orderNo":      fmt.Sprintf("FT%012d", t.ID),
			"createdAt":    t.CreatedAt.Format(time.RFC3339),
			"side":         map[bool]string{true: "IN", false: "OUT"}[t.Amount > 0],
			"type":         financeTypeOf(t.Type),
			"rawType":      t.Type,
			"status":       1,
			"userId":       t.UserID,
			"userAccount":  u.Account,
			"userNickname": u.Nickname,
			"userShortId":  model.StrVal(u.ShortID),
			"amount":       amt,
			"balanceAfter": t.Balance,
			"frozenAfter":  t.Frozen,
			"remark":       remark,
		})
	}
	return out, total, nil
}

// 用户昵称映射（钱包流水/朋友圈展示用）
func userNameMap(ctx context.Context, ids []int64) map[int64]string {
	m := make(map[int64]string)
	if len(ids) == 0 {
		return m
	}
	var users []model.User
	store.DB.Where("id IN (?)", ids).Find(&users)
	for _, u := range users {
		m[u.ID] = u.Nickname
	}
	return m
}

// walletTxTypeName 流水类型中文名
func walletTxTypeName(t string) string {
	switch t {
	case model.WalletTxRecharge:
		return "充值"
	case model.WalletTxWithdraw:
		return "提现"
	case model.WalletTxAdjust:
		return "手工调整"
	case model.WalletTxRedOut:
		return "发出红包"
	case model.WalletTxRedIn:
		return "领取红包"
	case model.WalletTxTrOut:
		return "转账支出"
	case model.WalletTxTrIn:
		return "转账收款"
	case model.WalletTxRedOutRefund:
		return "红包退回"
	case model.WalletTxTrOutRefund:
		return "转账退回"
	case model.WalletTxFreeze:
		return "冻结（发出）"
	case model.WalletTxSettle:
		return "结算（被领取）"
	case model.WalletTxUnfreeze:
		return "解冻退回"
	}
	return t
}

// 序列化流水（带用户昵称，后台列表用）
func walletTxOut(list []model.WalletTransaction) []map[string]interface{} {
	ids := make([]int64, 0, len(list))
	for _, t := range list {
		ids = append(ids, t.UserID)
	}
	nameMap := userNameMap(context.Background(), ids)
	out := make([]map[string]interface{}, 0, len(list))
	for _, t := range list {
		out = append(out, map[string]interface{}{
			"id":          strconv.FormatInt(t.ID, 10),
			"userId":      strconv.FormatInt(t.UserID, 10),
			"userName":    nameMap[t.UserID],
			"type":        t.Type,
			"typeName":    walletTxTypeName(t.Type),
			"amount":      t.Amount,
			"frozenDelta": t.FrozenDelta,
			"balance":     t.Balance,
			"frozen":      t.Frozen,
			"title":       t.Title,
			"remark":      t.Remark,
			"operatorId":  strconv.FormatInt(t.Operator, 10),
			"createdAt":   t.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return out
}

// ============ 红包领取 ============
// 红包语义（微信群对齐）：mode=lucky 时 amount 为总金额按个数随机分配；normal 时 amount 为单个金额

// RedPacketInfo 红包消息解析结果
type RedPacketInfo struct {
	MsgID      int64
	SenderID   int64
	Mode       string  // lucky / normal
	Amount     float64 // lucky=总金额 normal=**单个**金额（总额要用 TotalAmount()，别直接用）
	Count      int
	Note       string
	ClaimedCnt int
	ClaimedSum float64
	Status     int       // 资金包状态：1进行中 2已领完 3已过期退回 4已关闭
	ExpireAt   time.Time // 过期时间（24h）
	Recalled   bool      // 消息是否已撤回（撤回后资金已退回发送者，禁止再领取）
}

// redPacketTotal 红包**冻结总额**（与 money_packet.go moneyMsgTotal 的口径必须一致）
//
//	mode=lucky ：Amount 本身就是总金额，总额 = Amount
//	mode=normal：Amount 是【单个】金额，总额 = Amount × Count
//
// 注意：详情接口对外的 totalAmount 必须是这个值而不是 info.Amount，
// 否则普通红包会把「单个金额」当成「总额」返回，用户看到「发了 10 元」实际被冻结 20 元。
func redPacketTotal(info *RedPacketInfo) float64 {
	if info == nil {
		return 0
	}
	if info.Mode == "lucky" {
		return round2(info.Amount)
	}
	if info.Count < 1 {
		return round2(info.Amount)
	}
	return round2(info.Amount * float64(info.Count))
}

// redPacketPerAmount 红包**单个金额**（normal 即 Amount；lucky 用总额/个数估算，仅用于展示）
func redPacketPerAmount(info *RedPacketInfo) float64 {
	if info == nil {
		return 0
	}
	if info.Mode != "lucky" {
		return round2(info.Amount)
	}
	if info.Count < 1 {
		return round2(info.Amount)
	}
	return round2(info.Amount / float64(info.Count))
}

// loadRedPacket 从 mongo 拉红包消息并统计已领
func loadRedPacket(ctx context.Context, msgID int64) (*RedPacketInfo, error) {
	var m model.Message
	if err := msgColl().FindOne(ctx, bson.M{"msg_id": msgID}).Decode(&m); err != nil {
		return nil, &errs.Err{Code: 4201, Msg: "红包不存在或已过期"}
	}
	if m.Type != model.MsgRedPacket {
		return nil, &errs.Err{Code: 4201, Msg: "该消息不是红包"}
	}
	info := &RedPacketInfo{MsgID: msgID, SenderID: m.SenderID, Mode: "normal", Count: 1}
	// 撤回状态两个来源都要看：老数据只有 recalled 字段，新数据还会写 status
	info.Recalled = m.Recalled || m.Status == model.MsgStatusRecalled
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(m.Content), &data); err == nil {
		// 断言全部走 ok 判断：脏数据/老版本消息缺字段时不能 panic 掉整个进程
		if v, ok := data["mode"].(string); ok && v != "" {
			info.Mode = v
		}
		if v, ok := data["amount"].(float64); ok {
			info.Amount = v
		}
		if v, ok := data["count"].(float64); ok && int(v) > 0 {
			info.Count = int(v)
		}
		if v, ok := data["note"].(string); ok {
			info.Note = v
		}
	}
	// 已领统计：优先以资金包为准（它是钱的真值来源），没有包才是旧版本遗留数据
	if p := loadPacket(msgID); p != nil {
		info.ClaimedCnt = p.ClaimedCnt
		info.ClaimedSum = p.Claimed
		info.Status = p.Status
		info.ExpireAt = p.ExpireAt
		// 到期但未及时被后台任务扫到 → 这里顺手补一次退回
		if p.Status == model.MoneyPacketOpen && p.ExpireAt.Before(time.Now()) {
			_ = refundOne(context.Background(), p)
			info.Status = model.MoneyPacketExpired
		}
	} else {
		var claims []model.RedPacketClaim
		store.DB.Where("msg_id = ?", msgID).Order("seq ASC").Find(&claims)
		info.ClaimedCnt = len(claims)
		for _, c := range claims {
			info.ClaimedSum += c.Amount
		}
		info.Status = model.MoneyPacketClosed // 旧数据：没有冻结记录，禁止继续领取
	}
	return info, nil
}

// luckyAmount 微信拼手气算法：剩余金额 / 剩余人数的 0.01~2 倍均值区间随机
func luckyAmount(remain float64, remainCnt int) float64 {
	if remainCnt <= 1 {
		return remain // 最后一份拿剩余全部
	}
	avg := remain / float64(remainCnt) * 2
	amt := rand.Float64() * avg
	if amt < 0.01 {
		amt = 0.01
	}
	return math.Round(amt*100) / 100
}

// WalletRedPacketClaim 领取红包（结算冻结资金 + 行锁防超领），返回本次金额 + 领取列表
//
// B-22：入账不再是「凭空 WalletApply +amt」，而是从发送者冻结额里结算（SettleClaim）。
// 这样没人领的钱不会蒸发、也不会被重复领取 —— 想刷钱必须先真的有钱被冻结。
func WalletRedPacketClaim(ctx context.Context, userID, msgID int64) (map[string]interface{}, error) {
	info, err := loadRedPacket(ctx, msgID)
	if err != nil {
		return nil, err
	}
	// 已撤回：撤回时剩余冻结款已退回发送者，资金包被置为 Expired。
	// 这个判断必须放在状态判断之前，否则撤回到账后会被误报成「超过 24 小时未领完」。
	if info.Recalled {
		return nil, &errs.Err{Code: 4208, Msg: "该红包已被撤回，剩余金额已退回发送者"}
	}
	if info.Status == model.MoneyPacketExpired {
		return nil, &errs.Err{Code: 4207, Msg: "红包已超过 24 小时未领完，剩余金额已退回"}
	}
	if info.Status == model.MoneyPacketFinished {
		return nil, &errs.Err{Code: 4202, Msg: "红包已被领完"}
	}
	if info.Status == model.MoneyPacketClosed {
		return nil, &errs.Err{Code: 4206, Msg: "该红包为旧版本数据，已停止领取"}
	}
	if info.ClaimedCnt >= info.Count {
		return nil, &errs.Err{Code: 4202, Msg: "红包已被领完"}
	}
	// 已领过不能重复领（自己的红包自己也可以领，微信语义）
	var dup int64
	store.DB.Model(&model.RedPacketClaim{}).Where("msg_id = ? AND user_id = ?", msgID, userID).Count(&dup)
	if dup > 0 {
		return nil, &errs.Err{Code: 4204, Msg: "已经领取过了"}
	}
	// 计算本次金额（拼手气按剩余金额/剩余人数随机；普通红包固定单个金额）
	var amt float64
	if info.Mode == "lucky" {
		remain := info.Amount - info.ClaimedSum
		if remain < 0.01 {
			return nil, &errs.Err{Code: 4202, Msg: "红包已被领完"}
		}
		amt = luckyAmount(remain, info.Count-info.ClaimedCnt)
	} else {
		amt = info.Amount
	}
	amt = math.Round(amt*100) / 100

	// 结算（发送者 frozen -amt / 领取者 balance +amt）+ 写领取记录，同一事务提交
	// 上面的 dup 预检是快速失败，uk_msg_user 唯一索引才是并发下的最后一道防线：
	// 两个并发请求可能都看到 dup=0，但只有一条 INSERT 能成功，另一条撞唯一键。
	// 撞键必须翻译成 4204（而不是 500），否则客户端会误以为服务端出错而重试。
	got, err := SettleClaim(ctx, msgID, userID, amt,
		func(tx *gorm.DB, a float64, seq int) error {
			// OnConflict DoNothing 在 MySQL 下生成 ON DUPLICATE KEY UPDATE id=id：
			// 并发时第二个请求不会报错而是 RowsAffected=0，据此返回 4204 让整个事务回滚。
			// 这里不能依赖 gorm.ErrDuplicatedKey —— gorm.Config.TranslateError 默认 false，
			// store.InitMySQL 用的是 &gorm.Config{}，MySQL 1062 不会被翻译成 ErrDuplicatedKey。
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).
				Create(&model.RedPacketClaim{
					MsgID: msgID, UserID: userID, Amount: a, Seq: seq,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return &errs.Err{Code: 4204, Msg: "已经领取过了"}
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	detail, _ := WalletRedPacketDetail(ctx, userID, msgID)
	detail["myAmount"] = got
	return detail, nil
}

// WalletRedPacketDetail 红包详情：信息 + 领取列表
func WalletRedPacketDetail(ctx context.Context, viewerID, msgID int64) (map[string]interface{}, error) {
	info, err := loadRedPacket(ctx, msgID)
	if err != nil {
		return nil, err
	}
	var claims []model.RedPacketClaim
	store.DB.Where("msg_id = ?", msgID).Order("seq ASC").Find(&claims)
	ids := make([]int64, 0, len(claims))
	for _, c := range claims {
		ids = append(ids, c.UserID)
	}
	nameMap := userNameMap(ctx, ids)
	var users []model.User
	if len(ids) > 0 {
		store.DB.Where("id IN (?)", ids).Find(&users)
	}
	avatarMap := make(map[int64]string)
	for _, u := range users {
		avatarMap[u.ID] = u.Avatar
	}
	list := make([]map[string]interface{}, 0, len(claims))
	for _, c := range claims {
		list = append(list, map[string]interface{}{
			"userId":    strconv.FormatInt(c.UserID, 10),
			"userName":  nameMap[c.UserID],
			"avatar":    avatarMap[c.UserID],
			"amount":    c.Amount,
			"seq":       c.Seq,
			"createdAt": c.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	// 发送者信息
	senderName, senderAvatar := "", ""
	if info.SenderID > 0 {
		var u model.User
		if store.DB.First(&u, info.SenderID).Error == nil {
			senderName, senderAvatar = u.Nickname, u.Avatar
		}
	}
	expireAt := ""
	if !info.ExpireAt.IsZero() {
		expireAt = info.ExpireAt.Format("2006-01-02 15:04:05")
	}
	return map[string]interface{}{
		"msgId":        strconv.FormatInt(msgID, 10),
		"senderId":     strconv.FormatInt(info.SenderID, 10),
		"senderName":   senderName,
		"senderAvatar": senderAvatar,
		"note":         info.Note,
		"mode":         info.Mode,
		// totalAmount = **真实冻结总额**（lucky=Amount；normal=Amount×Count），
		// 不是 info.Amount —— 后者在普通红包下只是单个金额
		"totalAmount": round2(redPacketTotal(info)),
		// perAmount = 单个金额（normal=Amount；lucky=总额/个数，仅供展示）
		"perAmount":  round2(redPacketPerAmount(info)),
		"count":      info.Count,
		"claimedCnt": info.ClaimedCnt,
		"claimedSum": info.ClaimedSum,
		"status":     info.Status, // 1进行中 2已领完 3已过期退回 4已关闭
		"recalled":   info.Recalled,
		"expireAt":   expireAt,
		"list":       list,
	}, nil
}

// 补偿退款已由 RefundMoneyPacket 接管（见 money_packet.go）：
// 冻结制下「消息落库失败」只需把刚冻结的钱解冻回去，不需要单独的补偿流水类型。

// ============ 转账收款（服务端交叉校验，B-21）============
// 旧实现的两个致命问题：
//  1. 客户端 POST /wallet/record {type:"tr_in", amount:X} 自己给自己加钱，金额完全由客户端决定
//     → 任何人都能无限给自己充值；
//  2. "是否已领"只靠客户端本地存储（localStorage / SharedPreferences），
//     清掉缓存就能把同一笔转账反复领。
//
// 新实现：**金额只从 mongo 里的转账消息内容读取**，并做四重校验：
//
//	a) 消息必须是转账类型、存在、且未撤回；
//	b) 领取人必须是对话成员、且不是发送者本人；
//	c) transfer_claim 表 msg_id 唯一索引做幂等（并发也只能成功一次）；
//	d) B-22：**钱必须从发送者冻结额里结算出来**，不是凭空入账 —— 没有冻结记录的
//	   旧版本转账一律拒绝（否则就是印钱）。
//
// WalletTransferStatus 转账领取状态（气泡展示「您已领取/已被领取」用）
func WalletTransferStatus(ctx context.Context, msgID int64) (map[string]interface{}, error) {
	if msgID <= 0 {
		return nil, errs.ParamError
	}
	var claim model.TransferClaim
	claimed := false
	var claimUserId, claimUserName string
	if err := store.DB.Where("msg_id = ?", msgID).First(&claim).Error; err == nil {
		claimed = true
		claimUserId = strconv.FormatInt(claim.UserID, 10)
		var u model.User
		if store.DB.First(&u, claim.UserID).Error == nil {
			claimUserName = u.Nickname
		}
	}
	return map[string]interface{}{
		"msgId":         strconv.FormatInt(msgID, 10),
		"claimed":       claimed,
		"claimUserId":   claimUserId,
		"claimUserName": claimUserName,
	}, nil
}

func WalletTransferAccept(ctx context.Context, userID, msgID int64) (map[string]interface{}, error) {
	if msgID <= 0 || userID <= 0 {
		return nil, errs.ParamError
	}
	// a) 幂等：已领过直接返回本次金额（重复点击/重试不重复入账）
	var exist model.TransferClaim
	if err := store.DB.Where("msg_id = ?", msgID).First(&exist).Error; err == nil {
		if exist.UserID != userID {
			return nil, &errs.Err{Code: 4203, Msg: "该转账已被他人领取"}
		}
		bal := CurrentBalance(userID)
		return map[string]interface{}{
			"msgId":  strconv.FormatInt(msgID, 10),
			"amount": exist.Amount, "balance": bal, "already": true,
		}, nil
	}
	// 已过期退回 / 已关闭的资金包直接拒绝
	if p := loadPacket(msgID); p != nil && p.Status != model.MoneyPacketOpen {
		if p.Status == model.MoneyPacketExpired {
			return nil, &errs.Err{Code: 4207, Msg: "转账超过 24 小时未领取，已退回对方"}
		}
		return nil, &errs.Err{Code: 4202, Msg: "该转账已被领取"}
	}
	// b) 取转账消息
	var m model.Message
	if err := msgColl().FindOne(ctx, bson.M{"msg_id": msgID}).Decode(&m); err != nil {
		return nil, &errs.Err{Code: 4201, Msg: "转账不存在或已过期"}
	}
	if m.Type != model.MsgTransfer {
		return nil, &errs.Err{Code: 4201, Msg: "该消息不是转账"}
	}
	if m.Recalled || m.Status == model.MsgStatusRecalled {
		return nil, &errs.Err{Code: 4201, Msg: "该转账已被撤回"}
	}
	if m.SenderID == userID {
		return nil, &errs.Err{Code: 4205, Msg: "不能领取自己发出的转账"}
	}
	// c) 领取人必须在会话内
	var memCnt int64
	store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", m.ConversationID, userID).Count(&memCnt)
	if memCnt == 0 {
		return nil, &errs.Err{Code: 4205, Msg: "无权领取该转账"}
	}
	// d) 金额只认消息内容（不信客户端传的任何金额）
	var d struct {
		Amount float64 `json:"amount"`
		Note   string  `json:"note"`
	}
	if err := json.Unmarshal([]byte(m.Content), &d); err != nil {
		return nil, errs.ParamError
	}
	amount := math.Round(d.Amount*100) / 100
	if amount <= 0 {
		return nil, errs.ParamError
	}
	// e) 结算冻结资金 + 写领取记录（**同一个事务**，不做嵌套 —— 嵌套事务会开第二条连接，
	//    可能与外层持有的行锁互等造成死锁）
	//    uk_msg 唯一索引兜底并发重复领取；结算失败整体回滚，保证"领了就一定到账、没到账就能重领"
	got, err := SettleClaim(ctx, msgID, userID, amount,
		func(tx *gorm.DB, a float64, _ int) error {
			if err := tx.Create(&model.TransferClaim{
				MsgID: msgID, UserID: userID, Amount: a,
			}).Error; err != nil {
				return &errs.Err{Code: 4204, Msg: "已经领取过了"}
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"msgId":  strconv.FormatInt(msgID, 10),
		"amount": got, "balance": CurrentBalance(userID), "already": false,
	}, nil
}

// CurrentBalance 取用户当前余额（失败返回 0）
func CurrentBalance(userID int64) float64 {
	var u model.User
	if err := store.DB.First(&u, userID).Error; err != nil {
		return 0
	}
	return u.Balance
}

// WalletRecords 账单：时间筛选 + 分页
func WalletRecords(ctx context.Context, userID int64, start, end string, page, size int) (map[string]interface{}, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	// 纯冻结内部流转（settle：只有 frozen 变动、balance 不变，amount==0）不展示，
	// 否则自己领自己的红包会出现「红包被领取 +0」这类困惑记录
	// （与管理端 AdminFinanceList 的 amount<>0 同一规则，金额合计 = Σbalance 可对账）
	q := store.DB.Model(&model.WalletTransaction{}).
		Where("user_id = ? AND amount <> ?", userID, 0)
	if start != "" {
		if t, err := time.ParseInLocation("2006-01-02", start, time.Local); err == nil {
			q = q.Where("created_at >= ?", t)
		}
	}
	if end != "" {
		if t, err := time.ParseInLocation("2006-01-02", end, time.Local); err == nil {
			q = q.Where("created_at < ?", t.AddDate(0, 0, 1))
		}
	}
	var total int64
	q.Count(&total)
	var list []model.WalletTransaction
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, err
	}
	return map[string]interface{}{"total": total, "list": walletTxOut(list)}, nil
}

// WalletFreeze 统一冻结/解冻入口（行锁 + 同一事务写流水）。
//
//	dFrozen > 0：冻结 balance→frozen（需余额够）
//	dFrozen < 0：解冻 frozen→balance（需 frozen 够）
//	dBalance + dFrozen 应当满足冻结净不变（冻结=Δfrozen），为了灵活允许传参，但校验 余额/冻结 >= 0。
//
// 返回变动后的 {balance, frozen}。
func WalletFreeze(ctx context.Context, userID int64, dBalance, dFrozen float64, txType, title, remark, refID string, operator int64) (map[string]float64, error) {
	if dBalance == 0 && dFrozen == 0 {
		return nil, errs.ParamError
	}
	var (
		nb float64
		nf float64
	)
	err := store.DB.Transaction(func(tx *gorm.DB) error {
		var u model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, userID).Error; err != nil {
			return errs.ParamError
		}
		nb = round2(u.Balance + dBalance)
		nf = round2(u.Frozen + dFrozen)
		if nb < 0 {
			return &errs.Err{Code: 4101, Msg: "余额不足"}
		}
		if nf < 0 {
			return &errs.Err{Code: 4102, Msg: "冻结金额不足"}
		}
		if err := tx.Model(&model.User{}).Where("id = ?", userID).
			Updates(map[string]any{"balance": nb, "frozen": nf}).Error; err != nil {
			return err
		}
		return tx.Create(&model.WalletTransaction{
			UserID:      userID,
			Type:        txType,
			Amount:      round2(dBalance),
			FrozenDelta: round2(dFrozen),
			Balance:     nb,
			Frozen:      nf,
			Title:       title,
			Remark:      remark,
			RefID:       refID,
			Operator:    operator,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return map[string]float64{"balance": nb, "frozen": nf}, nil
}

// WalletMutateInTx 在已有事务内改用户余额/冻结并写流水（给提现审核等组合事务用）。
// 行锁；余额或冻结不足会返回 4101/4102，调用方需要回滚整个事务。
func WalletMutateInTx(tx *gorm.DB, userID int64, dBalance, dFrozen float64,
	txType, title, remark, refID string, operator int64) error {
	if dBalance == 0 && dFrozen == 0 {
		return errs.ParamError
	}
	var u model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, userID).Error; err != nil {
		return errs.ParamError
	}
	nb := round2(u.Balance + dBalance)
	nf := round2(u.Frozen + dFrozen)
	if nb < 0 {
		return &errs.Err{Code: 4101, Msg: "余额不足"}
	}
	if nf < 0 {
		return &errs.Err{Code: 4102, Msg: "冻结金额不足"}
	}
	if err := tx.Model(&model.User{}).Where("id = ?", userID).
		Updates(map[string]any{"balance": nb, "frozen": nf}).Error; err != nil {
		return err
	}
	return tx.Create(&model.WalletTransaction{
		UserID:      userID,
		Type:        txType,
		Amount:      round2(dBalance),
		FrozenDelta: round2(dFrozen),
		Balance:     nb,
		Frozen:      nf,
		Title:       title,
		Remark:      remark,
		RefID:       refID,
		Operator:    operator,
	}).Error
}
