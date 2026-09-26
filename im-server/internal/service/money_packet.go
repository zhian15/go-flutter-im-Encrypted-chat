package service

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"strconv"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============ 资金包：冻结 → 结算 / 到期退回（B-22）============
//
// 旧模型的三个致命问题：
//  1. 发出即扣款、领取即入账，两件事互不相干 → **没人领的钱凭空蒸发**
//     （发送者余额少了，但谁也没多），后台财务流水永远对不上；
//  2. 领取是「凭空入账」（WalletApply +amt），发送时没冻结校验 →
//     只要没人领就能一直发一直领，等于**刷钱**；
//  3. 红包没领完、转账没人收，钱就卡在中间态，不回发送者也不到收款人。
//
// 新模型：钱走 balance → frozen → 收款人 balance 三段，全程有 money_packet 记账：
//   发送：建包 + 冻结 total（balance -X / frozen +X）
//   领取：结算（发送者 frozen -amt；收款人 balance +amt），包记 claimed
//   到期：24h 后退回 total - claimed（frozen -R / balance +R）
// 恒等式：Σ(user.frozen) == Σ(未结算包的 total - claimed)

const moneyPacketTTL = 24 * time.Hour

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// mutateUser 在事务内对单个用户的 balance / frozen 做变动并写一条流水。
// 行锁 SELECT ... FOR UPDATE，余额或冻结不足直接报错（由事务整体回滚）。
func mutateUser(tx *gorm.DB, userID int64, dBalance, dFrozen float64,
	txType, title, remark, refID string, operator int64) error {
	var u model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, userID).Error; err != nil {
		return errs.ParamError
	}
	nb, nf := round2(u.Balance+dBalance), round2(u.Frozen+dFrozen)
	if nb < 0 {
		return &errs.Err{Code: 4101, Msg: "余额不足"}
	}
	if nf < 0 {
		return &errs.Err{Code: 4102, Msg: "冻结金额不足"}
	}
	if err := tx.Model(&model.User{}).Where("id = ?", userID).
		Updates(map[string]interface{}{"balance": nb, "frozen": nf}).Error; err != nil {
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

// moneyMsgTotal 解析红包/转账消息的**冻结总额**与个数
//
//	红包：mode=lucky 时 amount 是总金额；mode=normal 时 amount 是单个金额，总额 = amount × count
//	转账：总额 = amount，个数恒为 1
func moneyMsgTotal(kind int, content string) (total float64, count int, note string, err error) {
	var d struct {
		Amount float64 `json:"amount"`
		Count  float64 `json:"count"`
		Mode   string  `json:"mode"`
		Note   string  `json:"note"`
	}
	if e := json.Unmarshal([]byte(content), &d); e != nil {
		return 0, 0, "", errs.ParamError
	}
	if d.Amount <= 0 {
		return 0, 0, "", errs.ParamError
	}
	count = 1
	if kind == model.MsgRedPacket {
		count = int(d.Count)
		if count < 1 {
			count = 1
		}
		total = d.Amount
		if d.Mode != "lucky" {
			total = d.Amount * float64(count) // 普通红包：amount 是单个金额
		}
	} else {
		total = d.Amount
	}
	return round2(total), count, d.Note, nil
}

// SendMoneyFreeze 发红包 / 转账：登记资金包 + 冻结总额（钱从 balance 挪到 frozen，不离开用户账上）
// 必须在消息落库**之前**调用，成功后调用方若落库失败要调 RefundMoneyPacket 补偿。
func SendMoneyFreeze(ctx context.Context, senderID, msgID int64, kind int, content string) error {
	total, count, _, err := moneyMsgTotal(kind, content)
	if err != nil {
		return err
	}
	if msgID <= 0 {
		return errs.ParamError
	}
	// 展示文案不带「冻结」二字：对用户就是「发红包 -10」，
	// 冻结/解冻细节由 frozen 字段与 WalletTxUnfreeze 记录体现
	title := "发红包"
	if kind == model.MsgTransfer {
		title = "转账"
	}
	p := model.MoneyPacket{
		MsgID:    msgID,
		SenderID: senderID,
		Kind:     kind,
		Total:    total,
		Count:    count,
		Status:   model.MoneyPacketOpen,
		ExpireAt: time.Now().Add(moneyPacketTTL),
	}
	err = store.DB.Transaction(func(tx *gorm.DB) error {
		var dup int64
		tx.Model(&model.MoneyPacket{}).Where("msg_id = ?", msgID).Count(&dup)
		if dup > 0 {
			return nil // 幂等：同一条消息只冻结一次
		}
		if err := mutateUser(tx, senderID, -total, total, model.WalletTxFreeze,
			title, "", strconv.FormatInt(msgID, 10), 0); err != nil {
			return err
		}
		return tx.Create(&p).Error
	})
	if err == nil {
		go PublishWalletUpdate(context.Background(), senderID)
	}
	return err
}

// RefundMoneyPacket 补偿解冻：消息落库失败时把刚冻结的钱退回去（幂等）。
// 若包已不存在（说明从未冻结成功）则直接返回 nil。
func RefundMoneyPacket(ctx context.Context, msgID int64) {
	var p model.MoneyPacket
	if err := store.DB.Where("msg_id = ?", msgID).First(&p).Error; err != nil {
		return
	}
	if err := store.DB.Transaction(func(tx *gorm.DB) error {
		var cur model.MoneyPacket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", p.ID).First(&cur).Error; err != nil {
			return err
		}
		if cur.Status != model.MoneyPacketOpen || cur.Claimed > 0 {
			return nil
		}
		title := "红包发送失败退回"
		if cur.Kind == model.MsgTransfer {
			title = "转账发送失败退回"
		}
		if err := mutateUser(tx, cur.SenderID, cur.Total, -cur.Total,
			model.WalletTxUnfreeze, title, "", strconv.FormatInt(msgID, 10), 0); err != nil {
			return err
		}
		return tx.Model(&model.MoneyPacket{}).Where("id = ?", cur.ID).
			Updates(map[string]interface{}{"status": model.MoneyPacketClosed}).Error
	}); err == nil {
		go PublishWalletUpdate(context.Background(), p.SenderID)
	}
}

// loadPacket 取资金包；旧版本遗留消息（没有包）返回 nil
func loadPacket(msgID int64) *model.MoneyPacket {
	var p model.MoneyPacket
	if err := store.DB.Where("msg_id = ?", msgID).First(&p).Error; err != nil {
		return nil
	}
	return &p
}

// ClaimHook 领取结算成功后的追加操作（与结算在同一个事务里提交，保证"领了就一定有记录"）
// amt 为实际结算金额，seq 为第几个领取（从 1 开始）
type ClaimHook func(tx *gorm.DB, amt float64, seq int) error

// SettleClaim 领取结算：发送者 frozen -amt，领取者 balance +amt
// 全程行锁 + 事务，金额以资金包剩余额为准（不信任何客户端传入的金额）。
func SettleClaim(ctx context.Context, msgID, claimerID int64, amount float64, hook ClaimHook) (float64, error) {
	if msgID <= 0 || claimerID <= 0 || amount <= 0 {
		return 0, errs.ParamError
	}
	var settled float64
	var senderID int64
	err := store.DB.Transaction(func(tx *gorm.DB) error {
		var p model.MoneyPacket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("msg_id = ?", msgID).First(&p).Error; err != nil {
			// 旧版本遗留数据：发送时没有冻结，现在领取就是凭空印钱 → 一律拒绝
			return &errs.Err{Code: 4206, Msg: "该笔为旧版本数据，已无法领取"}
		}
		senderID = p.SenderID
		if p.Status != model.MoneyPacketOpen {
			return &errs.Err{Code: 4202, Msg: "已被领取或已退回"}
		}
		remain := round2(p.Total - p.Claimed)
		if p.ClaimedCnt >= p.Count || remain < 0.01 {
			return &errs.Err{Code: 4202, Msg: "已被领完"}
		}
		amt := round2(amount)
		if amt > remain {
			amt = remain // 兜底：最后一份只退剩余，绝不超发
		}
		refID := strconv.FormatInt(msgID, 10)
		outTitle, inTitle, inType := "红包被领取", "领取红包", model.WalletTxRedIn
		if p.Kind == model.MsgTransfer {
			outTitle, inTitle, inType = "转账被收款", "转账收款", model.WalletTxTrIn
		}
		// 自领（自己抢自己的群红包，微信语义允许）**不做特殊文案**：
		// 用户要的账单只有三种——发红包 / 领取红包 / 退回红包，
		// 标成「自领」反而多出一个概念。自领在账上就是一笔「领取红包 +X」，
		// 与 24h 后剩余部分的「退回红包 +Y」相加 = 发出去的总额，净 0。
		// 先扣发送者冻结
		if err := mutateUser(tx, p.SenderID, 0, -amt, model.WalletTxSettle,
			outTitle, "", refID, 0); err != nil {
			return err
		}
		// 再给领取者入账
		if err := mutateUser(tx, claimerID, amt, 0, inType,
			inTitle, "", refID, 0); err != nil {
			return err
		}
		if hook != nil {
			if err := hook(tx, amt, p.ClaimedCnt+1); err != nil {
				return err
			}
		}
		// 更新包
		claimed := round2(p.Claimed + amt)
		cnt := p.ClaimedCnt + 1
		status := model.MoneyPacketOpen
		if cnt >= p.Count || round2(p.Total-claimed) < 0.01 {
			status = model.MoneyPacketFinished
		}
		if err := tx.Model(&model.MoneyPacket{}).Where("id = ?", p.ID).
			Updates(map[string]interface{}{
				"claimed": claimed, "claimed_cnt": cnt, "status": status,
			}).Error; err != nil {
			return err
		}
		settled = amt
		return nil
	})
	if err == nil {
		// 发方看到冻结减少、收方看到余额增加，两端都实时刷新
		go PublishWalletUpdate(context.Background(), senderID, claimerID)
	}
	return settled, err
}

// RefundExpiredPackets 扫描到期未领完的资金包并退回剩余金额（转账退全额、红包退剩余）
func RefundExpiredPackets(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var list []model.MoneyPacket
	if err := store.DB.Where("status = ? AND expire_at < ?", model.MoneyPacketOpen, time.Now()).
		Order("expire_at ASC").Limit(limit).Find(&list).Error; err != nil {
		return 0, err
	}
	n := 0
	for i := range list {
		if err := refundOne(ctx, &list[i]); err != nil {
			log.Printf("[money] 到期退回失败 msgID=%d err=%v", list[i].MsgID, err)
			continue
		}
		n++
	}
	return n, nil
}

// refundOne 到期退回（文案用默认的「超时退回」）
func refundOne(ctx context.Context, p *model.MoneyPacket) error {
	return refundPacket(ctx, p, "")
}

// refundPacket 解冻退回：把资金包里还没被领走的钱退回发送者（frozen -R / balance +R）
//
// 幂等：非 Open 状态（已领完 / 已退回 / 已关闭）直接返回 nil，重复调用不会二次退钱 ——
// 撤回消息与 24h 到期扫描都会调它，两者并发时靠这个判断保证只退一次。
//
// title 为空时用默认文案「退回红包 / 退回转账」；撤回场景由调用方传入
// 「红包撤回退回 / 转账撤回退回」以区分流水来源。
//
// 面向用户的账单只有三种：发红包（−） / 领取红包（+） / 退回红包（+）。
// 「超时」这类内部触发原因不写进标题，用户不需要知道是 24h 到期还是手动撤回。
func refundPacket(ctx context.Context, p *model.MoneyPacket, title string) error {
	err := store.DB.Transaction(func(tx *gorm.DB) error {
		var cur model.MoneyPacket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", p.ID).First(&cur).Error; err != nil {
			return err
		}
		if cur.Status != model.MoneyPacketOpen {
			return nil
		}
		remain := round2(cur.Total - cur.Claimed)
		txTitle := title
		if txTitle == "" {
			txTitle = "退回红包"
			if cur.Kind == model.MsgTransfer {
				txTitle = "退回转账"
			}
		}
		if remain > 0.005 {
			if err := mutateUser(tx, cur.SenderID, remain, -remain,
				model.WalletTxUnfreeze, txTitle, "", strconv.FormatInt(cur.MsgID, 10), 0); err != nil {
				return err
			}
		}
		return tx.Model(&model.MoneyPacket{}).Where("id = ?", cur.ID).
			Updates(map[string]interface{}{"status": model.MoneyPacketExpired}).Error
	})
	if err == nil {
		go PublishWalletUpdate(context.Background(), p.SenderID)
	}
	return err
}

// StartMoneyPacketExpiryWorker 24 小时到期退回的后台任务（启动时先跑一次，之后每分钟扫一次）
func StartMoneyPacketExpiryWorker(ctx context.Context) {
	run := func() {
		n, err := RefundExpiredPackets(ctx, 200)
		if err != nil {
			log.Printf("[money] 到期退回扫描失败: %v", err)
		} else if n > 0 {
			log.Printf("[money] 到期退回 %d 笔", n)
		}
	}
	go run()
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				run()
			}
		}
	}()
}

// ============ 对账（B-22：后台金额与用户端、财务记录对不上）============

// WalletReconcile 全量对账，返回四项汇总 + 三项差额 + 是否平衡
func WalletReconcile(ctx context.Context) (map[string]interface{}, error) {
	var balanceSum, frozenSum, txAmountSum, txFrozenSum, pendingSum float64
	store.DB.Model(&model.User{}).Select("COALESCE(SUM(balance),0)").Scan(&balanceSum)
	store.DB.Model(&model.User{}).Select("COALESCE(SUM(frozen),0)").Scan(&frozenSum)
	store.DB.Model(&model.WalletTransaction{}).Select("COALESCE(SUM(amount),0)").Scan(&txAmountSum)
	store.DB.Model(&model.WalletTransaction{}).Select("COALESCE(SUM(frozen_delta),0)").Scan(&txFrozenSum)
	store.DB.Model(&model.MoneyPacket{}).Where("status = ?", model.MoneyPacketOpen).
		Select("COALESCE(SUM(total - claimed),0)").Scan(&pendingSum)

	balanceDiff := round2(balanceSum - txAmountSum) // 应为 0；非 0 = 有人直接改了 user.balance 没写流水
	frozenDiff := round2(frozenSum - txFrozenSum)
	frozenVsPacket := round2(frozenSum - pendingSum) // 应为 0；非 0 = 冻结的钱没对应到未结算的资金包

	type badUser struct {
		ID        int64   `json:"id,string"`
		Account   string  `json:"account"`
		Nickname  string  `json:"nickname"`
		Balance   float64 `json:"balance"`
		Frozen    float64 `json:"frozen"`
		TxAmount  float64 `json:"txAmount"`
		TxFrozen  float64 `json:"txFrozen"`
		AmountGap float64 `json:"amountGap"`
		FrozenGap float64 `json:"frozenGap"`
	}
	var bads []badUser
	store.DB.Raw(`
		SELECT u.id, u.account, u.nickname, u.balance, u.frozen,
		       COALESCE(SUM(t.amount),0)       AS tx_amount,
		       COALESCE(SUM(t.frozen_delta),0) AS tx_frozen,
		       ROUND(u.balance - COALESCE(SUM(t.amount),0), 2)       AS amount_gap,
		       ROUND(u.frozen  - COALESCE(SUM(t.frozen_delta),0), 2) AS frozen_gap
		FROM user u LEFT JOIN wallet_transaction t ON t.user_id = u.id
		GROUP BY u.id, u.account, u.nickname, u.balance, u.frozen
		HAVING ABS(u.balance - COALESCE(SUM(t.amount),0)) > 0.01
		    OR ABS(u.frozen  - COALESCE(SUM(t.frozen_delta),0)) > 0.01
		ORDER BY ABS(u.balance - COALESCE(SUM(t.amount),0)) DESC
		LIMIT 50`).Scan(&bads)

	ok := math.Abs(balanceDiff) < 0.01 && math.Abs(frozenDiff) < 0.01 && math.Abs(frozenVsPacket) < 0.01
	return map[string]interface{}{
		"balanceSum":     round2(balanceSum),
		"frozenSum":      round2(frozenSum),
		"txAmountSum":    round2(txAmountSum),
		"txFrozenSum":    round2(txFrozenSum),
		"pendingSum":     round2(pendingSum),
		"balanceDiff":    balanceDiff,
		"frozenDiff":     frozenDiff,
		"frozenVsPacket": frozenVsPacket,
		"mismatchUsers":  bads,
		"ok":             ok,
		"checkedAt":      time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}
