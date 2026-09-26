package model

import "time"

// AppEntry 小程序（H5 容器）：后台上架网页 URL，客户端 WebView/新页打开
type AppEntry struct {
	ID        int64     `gorm:"primaryKey" json:"id,string"`
	NameZh    string    `gorm:"size:64" json:"nameZh"`
	NameEn    string    `gorm:"size:64" json:"nameEn"`
	Icon      string    `gorm:"size:512" json:"icon"`
	URL       string    `gorm:"size:1024" json:"url"`
	Category  string    `gorm:"size:32" json:"category"`
	Sort      int       `json:"sort"`
	Enabled   int       `gorm:"default:1" json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
}

func (AppEntry) TableName() string { return "app_entry" }
