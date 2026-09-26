package model

import "time"

// ConversationFolder 用户自定义会话文件夹（PC 端需求：文件夹 TAB 分页，纯个人视图分组，
// 不涉及成员/会话本体变更；chat_ids 存会话 ID 字符串数组 JSON）。
// 不在 store.go AutoMigrate 白名单（对齐 Report 先例：每用户多行小表，DDL 走迁移），
// 建表由 migrations/023_archived_folder.sql 负责。
type ConversationFolder struct {
	ID     int64  `gorm:"primaryKey" json:"id,string"`
	UserID int64  `gorm:"index:idx_folder_user" json:"userId,string"`
	Name   string `gorm:"size:64" json:"name"`
	Color  string `gorm:"size:16" json:"color"`
	// ChatIDs JSON 数组字符串：["convId1","convId2"]。会话 ID 雪花超 2^53，
	// 与全局 ID 契约一致按字符串存取。
	ChatIDs   string    `gorm:"type:text" json:"chatIds"`
	Sort      int       `gorm:"default:0" json:"sort"` // 预留排序位（客户端按创建顺序展示）
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (ConversationFolder) TableName() string { return "conversation_folder" }
