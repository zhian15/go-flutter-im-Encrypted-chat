package model

import "time"

// RedPacketClaim 红包领取记录（一个红包多条领取，msg_id 关联 mongo 消息）
//
// 注意：(msg_id, user_id) 的**联合唯一约束不在 model tag 里**，而是由
// migrations/017_red_packet_claim_unique.sql 建立（索引名 uk_msg_user）。
// 原因：cmd/api/main.go 先跑 AutoMigrate 再跑 MigrateMySQL，若把 uniqueIndex 写进 tag，
// 存量库中存在重复行时 AutoMigrate 建唯一索引会失败 → 服务起不来 → 迁移脚本永远没机会
// 先清理重复行，形成死锁。因此这里只保留普通索引，唯一性交给迁移脚本。
type RedPacketClaim struct {
	ID        int64     `gorm:"primaryKey" json:"id,string"`
	MsgID     int64     `gorm:"index" json:"msgId,string"` // 红包消息 ID
	UserID    int64     `gorm:"index" json:"userId,string"`
	Amount    float64   `gorm:"type:decimal(12,2)" json:"amount"`
	Seq       int       `gorm:"default:0" json:"seq"` // 第几个领取（从 1 开始）
	CreatedAt time.Time `json:"createdAt"`
}

func (RedPacketClaim) TableName() string { return "red_packet_claim" }

// TransferClaim 转账领取记录（一笔转账只能被领一次，msg_id 唯一索引兜底并发重复领取）
// 作用：收款入账改为服务端按消息内容入账，客户端不能再自己报金额给自己加钱（B-21）
type TransferClaim struct {
	ID        int64     `gorm:"primaryKey" json:"id,string"`
	MsgID     int64     `gorm:"uniqueIndex:uk_msg;index" json:"msgId,string"` // 转账消息 ID
	UserID    int64     `gorm:"index" json:"userId,string"`                   // 收款人
	Amount    float64   `gorm:"type:decimal(12,2)" json:"amount"`
	CreatedAt time.Time `json:"createdAt"`
}

func (TransferClaim) TableName() string { return "transfer_claim" }
