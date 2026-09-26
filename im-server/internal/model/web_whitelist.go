package model

import "time"

// WebWhitelist 网页小程序白名单（MySQL 表 web_whitelist）。
// 由迁移 003_add_web_whitelist.sql 建表，004_add_web_whitelist_media.sql 补 logo/cover 列，
// 不进 store.go 的 AutoMigrate 白名单，
// 以符合项目约定：非白名单 model 加表必须另写 migrations SQL。
//
// JSON tag 统一 camelCase：管理后台接口（admin/web-whitelist）直接序列化本结构体返回，
// 前端 WebWhitelist.vue 读取 domain/displayName/enabled/nativeBridge/logo/cover/id。
// 注意：whitelist.Match 走 Go 结构体字段、不经过 JSON，改 tag 不影响匹配逻辑。
type WebWhitelist struct {
	ID           int64     `gorm:"primaryKey" json:"id,string"`
	Domain       string    `gorm:"size:255;uniqueIndex:uk_domain" json:"domain"`
	DisplayName  string    `gorm:"size:128" json:"displayName"`
	Enabled      int       `gorm:"default:1" json:"enabled"` // 1 启用 0 禁用
	NativeBridge int       `gorm:"default:0" json:"nativeBridge"`
	Logo         string    `gorm:"size:512" json:"logo"`  // 小程序图标，非空时覆盖 /link/meta 的 icon
	Cover        string    `gorm:"size:512" json:"cover"` // 封面大图，非空时覆盖 /link/meta 的 image
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// TableName 显式指定表名
func (WebWhitelist) TableName() string { return "web_whitelist" }
