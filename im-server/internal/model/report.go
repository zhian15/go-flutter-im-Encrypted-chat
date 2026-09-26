package model

import "time"

// Report 投诉（会话设置「投诉」入口上报；管理后台处理）
type Report struct {
	ID         int64     `gorm:"primaryKey" json:"id,string"` // 雪花 ID（表无 AUTO_INCREMENT，由应用层生成）
	ReporterID int64     `gorm:"index" json:"reporterId,string"`
	PeerID     int64     `gorm:"default:0" json:"peerId,string"` // 被投诉人（单聊=对方；群聊可空 0）
	ConvID     int64     `gorm:"default:0;index" json:"convId,string"`
	Category   string    `gorm:"size:64" json:"category"` // 投诉类型（客户端 l10n key：convSetReportSpam/Fraud/Harass/Impersonate/Other）
	Note       string    `gorm:"type:text" json:"note"`   // 补充说明（可空）
	Status     int       `gorm:"default:0" json:"status"` // 0 待处理 / 1 已处理
	HandledBy  int64     `gorm:"default:0" json:"handledBy,string"`
	HandledAt  *time.Time `json:"handledAt"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (Report) TableName() string { return "report" }
