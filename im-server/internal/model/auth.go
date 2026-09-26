package model

import "time"

// Device 设备（多端登录 / 离线推送 token）
type Device struct {
	ID           int64     `gorm:"primaryKey" json:"id,string"`
	UserID       int64     `gorm:"index" json:"userId,string"`
	DeviceType   int       `gorm:"default:1" json:"deviceType"` // 1 Android / 2 iOS / 3 Web / 4 Windows / 5 macOS
	DeviceID     string    `gorm:"size:128" json:"deviceId"`
	PushToken    string    `gorm:"size:512" json:"-"`
	Status       int       `gorm:"default:1" json:"status"` // 1 在线 / 0 离线
	LastActiveAt time.Time `json:"lastActiveAt"`
	// LastIP 最后活跃出口 IP：WS 建连时随 last_active_at 一起落库（ws.go handleWS）。
	// 列由 migrations/019_device_meta.sql 补（Device 不在 AutoMigrate 白名单）。
	LastIP string `gorm:"size:64" json:"lastIp"`
	// DeviceName 客户端上报的设备型号（如 "Xiaomi 2201123G" / "手机网页" / "Windows PC"），
	// 登录/注册/游客/扫码登录时随 issueTokens 落库。列由 migrations/026_device_name.sql 补。
	DeviceName string    `gorm:"size:128" json:"deviceName"`
	// Trusted 设备信任标记（登录设备批准功能，2026-09-23）：1=已信任(免二次验证)，
	// 0=曾被手动注销、下次登录需重新批准。列由 migrations/028_device_trusted.sql 补。
	Trusted bool `gorm:"default:1" json:"trusted"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (Device) TableName() string { return "device" }

// LoginLog 登录日志（后台可查）
type LoginLog struct {
	ID        int64     `gorm:"primaryKey" json:"id,string"`
	UserID    int64     `gorm:"index" json:"userId,string"`
	IP        string    `gorm:"size:64" json:"ip"`
	Device    string    `gorm:"size:255" json:"device"`
	Result    int       `gorm:"default:1" json:"result"` // 1 成功 / 0 失败
	CreatedAt time.Time `json:"createdAt"`
}

func (LoginLog) TableName() string { return "login_log" }

// InviteCode 邀请码（后台批量生成）
type InviteCode struct {
	ID        int64      `gorm:"primaryKey" json:"id,string"`
	Code      string     `gorm:"size:32;uniqueIndex" json:"code"`
	Batch     string     `gorm:"size:64" json:"batch"`
	UsedBy    *int64     `gorm:"index" json:"usedBy,string"`
	UsedAt    *time.Time `json:"usedAt"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Enabled   int        `gorm:"default:1" json:"enabled"`
	CreatedAt time.Time  `json:"createdAt"`
}

func (InviteCode) TableName() string { return "invite_code" }

// InviteFriendCode 自定义邀请码（后台创建）：一个码可关联多个好友，
// 通过该码注册的用户会自动添加这些好友（双向好友关系，多用不限次数）
type InviteFriendCode struct {
	ID        int64     `gorm:"primaryKey" json:"id,string"`
	Code      string    `gorm:"size:32;uniqueIndex" json:"code"`
	FriendIDs string    `gorm:"type:json" json:"friendIds"` // JSON 数组字符串，如 ["123","456"]
	Remark    string    `gorm:"size:255" json:"remark"`     // 备注（这个码发给谁用）
	Enabled   int       `gorm:"default:1" json:"enabled"`   // 1 启用 / 0 停用
	UsedCount int       `gorm:"default:0" json:"usedCount"` // 使用次数（注册成功并绑定好友 +1）
	CreatedAt time.Time `json:"createdAt"`
}

func (InviteFriendCode) TableName() string { return "invite_friend_code" }

// AdminLog 后台操作日志
type AdminLog struct {
	ID        int64     `gorm:"primaryKey" json:"id,string"`
	AdminID   int64     `gorm:"index" json:"adminId,string"`
	Action    string    `gorm:"size:64" json:"action"`
	Target    string    `gorm:"size:255" json:"target"`
	Detail    string    `gorm:"type:json" json:"detail"`
	IP        string    `gorm:"size:64" json:"ip"`
	CreatedAt time.Time `json:"createdAt"`
}

func (AdminLog) TableName() string { return "admin_log" }

// UserKey E2E 用户密钥（阶段 1 预留表，V1.0 启用加密时使用）
//
// E2EE（type=13 端到端文本，2026-09-18 §36 定稿）字段语义：
//   - PublicKey：X25519 公钥，base64 标准编码的 32 字节裸值（客户端生成）；
//   - EncryptedPrivateKey：私钥备份密文，**客户端**用登录密码派生 KEK 加密的
//     JSON 字符串 {v,kdf,it,salt,hkdf,pub,n,ct}（服务端不可解密，原样存储）；
//   - E2eeOn：用户级开关（默认开），仅后台模式 e2ee_mode="e2ee" 时有意义；
//   - 改密码时客户端 re-wrap 备份（同事务，见 service.ChangePassword）；
//     忘记密码/后台重置 = 烧备份（删整行，见 AdminUserResetPassword）。
type UserKey struct {
	ID                  int64  `gorm:"primaryKey" json:"id,string"`
	UserID              int64  `gorm:"uniqueIndex" json:"userId,string"`
	PublicKey           string `gorm:"type:text" json:"-"`
	EncryptedPrivateKey string `gorm:"type:text" json:"-"`
	KeyVersion          int    `gorm:"default:1" json:"keyVersion"`
	// E2eeOn 用户级端到端开关：1 开（默认）/ 0 关。列由 migrations/008_e2ee.sql 补
	//（UserKey 不在 AutoMigrate 白名单）。
	E2eeOn    int       `gorm:"default:1" json:"e2eeOn"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (UserKey) TableName() string { return "user_keys" }
