package model

import "time"

// 资金包状态
const (
	MoneyPacketOpen     = 1 // 进行中（等待领取 / 未领完）
	MoneyPacketFinished = 2 // 已领完
	MoneyPacketExpired  = 3 // 24 小时到期，剩余金额已退回发送者
	MoneyPacketClosed   = 4 // 手工关闭（旧版本遗留数据清理）
)

// MoneyPacket 红包 / 转账的资金包（B-22）
//
// 存在意义：以前「发出即扣款、领取即入账」是两条互不相干的操作，
// 于是没人领的钱就凭空蒸发了（发送者余额少了，谁也没多），
// 而领取又是凭空入账 —— 只要没人领就能一直发、一直领，等于刷钱。
//
// 现在每一笔红包/转账都在本表登记一行，钱走 balance → frozen → 收款人 balance：
//   - 发送：建包 + 冻结 total（WalletFreeze）
//   - 领取：结算 claimed += amt（WalletSettle，发送者 frozen 减少、领取者 balance 增加）
//   - 到期：退回 total - claimed（WalletUnfreeze）
//
// 任意时刻都满足：Σ(frozen) == Σ(未结算包的 total - claimed)。
type MoneyPacket struct {
	ID         int64     `gorm:"primaryKey" json:"id,string"`
	MsgID      int64     `gorm:"uniqueIndex:uk_msg" json:"msgId,string"` // 关联消息 ID（红包 8 / 转账 9）
	SenderID   int64     `gorm:"index" json:"senderId,string"`
	Kind       int       `json:"kind"` // 8 红包 / 9 转账
	Total      float64   `gorm:"type:decimal(12,2)" json:"total"`
	Count      int       `json:"count"`                             // 红包个数（转账恒为 1）
	Claimed    float64   `gorm:"type:decimal(12,2)" json:"claimed"` // 已领金额
	ClaimedCnt int       `json:"claimedCnt"`                        // 已领人数
	Status     int       `gorm:"default:1;index" json:"status"`     // 1进行中 2已领完 3已过期退回 4已关闭
	ExpireAt   time.Time `gorm:"index" json:"expireAt"`             // 过期时间（创建 +24h）
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func (MoneyPacket) TableName() string { return "money_packet" }
