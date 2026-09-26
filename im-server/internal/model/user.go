package model

import "time"

// StrPtr 返回 s 的指针（构造 *string 用）
func StrPtr(s string) *string { return &s }

// StrVal 返回 *string 的值；nil 返回空串
func StrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

const (
	RoleUser  = 1
	RoleAdmin = 2
	RoleKefu  = 3 // 客服（后台可把普通用户设为客服；新注册可自动添加客服）

	StatusNormal   = 1
	StatusDisabled = 2
)

// User 用户表
type User struct {
	ID           int64  `gorm:"primaryKey" json:"id,string"`
	Account      string `gorm:"size:64;uniqueIndex" json:"account"`
	PasswordHash string `gorm:"size:255" json:"-"`
	// TokenVersion 令牌版本：改密码 / 被禁用 / 管理员重置密码时 +1，
	// 令所有已签发的 access/refresh token 立即失效（鉴权中间件与 Refresh 都会比对）。
	// 默认值必须为 0：存量令牌没有 v 声明，解析出来是 0，DB 默认 0 才能匹配上，避免上线瞬间全员 401。
	TokenVersion   int64      `gorm:"default:0" json:"-"`
	PayPwdHash     string     `gorm:"size:255" json:"-"` // 支付密码 hash（bcrypt；发红包/转账冻结前校验；json 不序列化，不泄露）
	Nickname       string     `gorm:"size:64" json:"nickname"`
	Avatar         string     `gorm:"size:512" json:"avatar"`
	Signature      string     `gorm:"size:200" json:"signature"` // 个人签名（AutoMigrate 自动加列）
	Phone          string     `gorm:"size:32" json:"phone"`
	Email          string     `gorm:"size:128" json:"email"`
	CountryCode    string     `gorm:"size:8" json:"countryCode"`
	// ---- 隐私设置（AutoMigrate 白名单内，加列自动；读写入口 GET/PUT /user/privacy）----
	// Gender：male / female / secret（默认）
	Gender string `gorm:"size:16;default:'secret'" json:"gender"`
	// 可见范围三档："all"=所有人 / "contacts"=仅联系人 / "nobody"=不公开。
	// phone_visible 默认 nobody = 保持历史行为（任何接口都不给别人看手机号）；
	// online_visible 默认 all = 保持历史行为（在线状态全员可见）。
	PhoneVisible  string `gorm:"size:16;default:'nobody'" json:"phoneVisible"`
	OnlineVisible string `gorm:"size:16;default:'all'" json:"onlineVisible"`
	// 开关类：1=开（默认，保持历史行为）0=关
	PhoneSearchable    int `gorm:"default:1" json:"phoneSearchable"`    // 允许他人按手机号搜到我
	ShortIDSearchable  int `gorm:"default:1" json:"shortIdSearchable"`  // 允许他人按平台短号（short_id）搜到我
	ReadReceiptEnabled int `gorm:"default:1" json:"readReceiptEnabled"` // 向对方展示我的已读状态（不影响 last_read_msg_id/未读数）
	TypingEnabled      int `gorm:"default:1" json:"typingEnabled"`      // 向对方展示「正在输入」
	ShortID        *string    `gorm:"size:32;uniqueIndex" json:"shortId"`          // 靓号/用户ID（可通过 ID 加好友，后台可预留）；nil 表示未分配，数据库写 NULL 不会触发 UNIQUE 冲突
	Balance        float64    `gorm:"type:decimal(12,2);default:0" json:"balance"` // 零钱余额（可自由使用）
	Frozen         float64    `gorm:"type:decimal(12,2);default:0" json:"frozen"`  // 冻结金额（发出的红包/转账尚未被领取的部分）
	DepartmentID   int64      `gorm:"index" json:"departmentId,string"`
	Status         int        `gorm:"default:1" json:"status"`
	Role           int        `gorm:"default:1" json:"role"`
	MyInviteCode   string     `gorm:"size:32" json:"myInviteCode"`             // 我的邀请码（后台关联：用户中心展示 + 点击复制）
	InvitedBy      int64      `gorm:"index;default:0" json:"invitedBy,string"` // 上级（邀请人）用户 ID，0=无
	InvitedCode    string     `gorm:"size:64;index" json:"invitedCode"`        // 注册/绑定时填写的邀请码原文
	RegisterIP     string     `gorm:"size:64" json:"registerIP"`               // 注册 IP（注册时捕获，AutoMigrate 自动加列）
	RegisterDevice string     `gorm:"size:255" json:"registerDevice"`          // 注册设备（Android/iOS/Web/... + 设备号）
	IsGuest        int        `gorm:"default:0" json:"isGuest"`                // 是否游客账号（1=游客，按设备号登录，无需密码）
	GuestDeviceID  string     `gorm:"size:255;index" json:"guestDeviceId"`     // 游客绑定设备号（幂等复用，避免重复建号）
	LastLoginIP    string     `gorm:"size:64" json:"lastLoginIP"`              // 最后登录 IP（登录成功时更新）
	LastLoginAt    *time.Time `json:"lastLoginAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

func (User) TableName() string { return "user" }

// Department 部门（双语字段，i18n）
type Department struct {
	ID        int64     `gorm:"primaryKey" json:"id,string"`
	NameZh    string    `gorm:"size:64" json:"nameZh"`
	NameEn    string    `gorm:"size:64" json:"nameEn"`
	ParentID  int64     `gorm:"index" json:"parentId,string"`
	Sort      int       `json:"sort"`
	CreatedAt time.Time `json:"createdAt"`
}

func (Department) TableName() string { return "department" }

// SysConfig 系统配置 KV
type SysConfig struct {
	ID          int64     `gorm:"primaryKey" json:"id,string"`
	ConfigKey   string    `gorm:"size:64;uniqueIndex" json:"key"`
	ConfigValue string    `gorm:"type:json" json:"value"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (SysConfig) TableName() string { return "sys_config" }

// 保留靓号状态
const (
	ReservedShortIDOpen   = 1 // 未分配
	ReservedShortIDFrozen = 2 // 冻结（暂不出售/分配）
	ReservedShortIDUsed   = 3 // 已分配
)

// 保留靓号来源
const (
	ReservedSourceManual = 1 // 手动录入
	ReservedSourceRange  = 2 // 范围生成
	ReservedSourceRule   = 3 // 规则生成
)

// ReservedShortID 预留给后台分配的短ID池（管理员可批量生成/冻结/备注，分配时与 user.short_id 联动）
type ReservedShortID struct {
	ID        int64      `gorm:"primaryKey;autoIncrement" json:"id,string"`
	ShortID   string     `gorm:"size:32;uniqueIndex" json:"shortId"` // 预留 short_id（唯一）
	Source    int        `gorm:"index;default:1" json:"source"`      // 1 手动 / 2 范围 / 3 规则
	Type      int        `gorm:"index;default:1" json:"type"`        // 1 普通 / 2 豹子号 / 3 顺子号 / 4 VIP
	Status    int        `gorm:"index;default:1" json:"status"`      // 1 未分配 / 2 冻结 / 3 已用
	Remark    string     `gorm:"size:255" json:"remark"`             // 备注（价格说明/来源等）
	Price     float64    `gorm:"type:decimal(12,2);default:0" json:"price"`
	UsedBy    int64      `gorm:"index;default:0" json:"usedBy,string"` // 被哪个用户占用（0 = 未占用）
	UsedAt    *time.Time `json:"usedAt"`
	CreatedAt time.Time  `json:"createdAt"`
}

func (ReservedShortID) TableName() string { return "reserved_short_id" }
