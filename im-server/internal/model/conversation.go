package model

import "time"

const (
	ConvDirect  = 1
	ConvGroup   = 2
	ConvChannel = 3 // 频道（类公众号）：复用会话/群表，只有频道主（MemberOwner）能发言

	ConvNormal  = 1
	ConvDisband = 2

	MemberOwner  = 1
	MemberAdmin  = 2
	MemberNormal = 3
)

// Conversation 会话（单聊也建一条，member 2 人，统一会话列表）
type Conversation struct {
	ID                int64  `gorm:"primaryKey" json:"id,string"`
	Type              int    `gorm:"default:1" json:"type"`
	NameZh            string `gorm:"size:64" json:"nameZh"`
	NameEn            string `gorm:"size:64" json:"nameEn"`
	Avatar            string `gorm:"size:512" json:"avatar"`
	OwnerID           int64  `gorm:"index" json:"ownerId,string"`
	AnnouncementZh    string `gorm:"type:text" json:"announcementZh"`
	AnnouncementEn    string `gorm:"type:text" json:"announcementEn"`
	MaxMembers        int    `gorm:"default:500" json:"maxMembers"`
	Status            int    `gorm:"default:1" json:"status"`
	PinnedMsgID       int64  `gorm:"default:0" json:"pinnedMsgId,string"`
	PinnedMsgContent  string `gorm:"size:512" json:"pinnedMsgContent"`
	PinnedMsgIDs      string `gorm:"type:text" json:"pinnedMsgIds"`      // 多条置顶：JSON 数组 ["id1","id2"]（兼容旧单条字段）
	MuteAll           int    `gorm:"default:0" json:"muteAll"`           // 全员禁言：1=开启（仅群主/管理员可发言）
	PrivacyEnabled    int    `gorm:"default:0" json:"privacyEnabled"`    // 成员隐私：1=开启（普通成员不可查看成员列表）
	AllowMemberInvite int    `gorm:"default:1" json:"allowMemberInvite"` // 允许群成员邀请成员：1=允许
	QrJoinEnabled     int    `gorm:"default:1" json:"qrJoinEnabled"`     // 二维码进群：1=开启
	// ShowMembers 显示群成员人数/在线人数（2026-09-22 需求5）：1=显示（默认，保持历史行为）
	// 0=关闭（群聊标题下方不显示「N 成员, M 在线」，聊天信息页不显示群成员卡片）。
	// Conversation 在 AutoMigrate 白名单内自动加列，migrations/027 兜底老库。
	ShowMembers int `gorm:"default:1" json:"showMembers"`
	// ShortID 会话自定义唯一 ID（类公众号微信号，频道用）：3-20 位字母/数字/下划线，
	// 全表唯一（一个自定义 ID 只能属于一个会话），凭它可搜索/打开频道
	// （GET /channel/:id 两段式解析、/user/search 精确命中）。nil=未设置。
	// 指针类型：未设置写 NULL，避免空串占用唯一索引（对齐 user.ShortID 先例）；
	// Conversation 在 store.go AutoMigrate 白名单内，migrations/021 兜底老库。
	ShortID *string `gorm:"size:32;uniqueIndex:uk_conv_short_id" json:"shortId"`
	// DirectKey 单聊配对键（仅 type=1 单聊写 "小UID:大UID"，群/频道恒 NULL）。
	// 防 CreateDirect「先查后建」的并发竞态给同一对人建出两条单聊（会话列表
	// 同一个人显示两条）。创建时携带该键，撞 uk_direct_key 唯一索引即改查
	// 已有会话原样返回。指针类型：NULL 不占唯一索引（对齐 ShortID 先例）。
	// 注意：**唯一索引不写 tag**，由 migrations/025 建立（redpacket.go 同款原因：
	// AutoMigrate 先于迁移跑，存量重复行会让 tag 唯一索引建失败 → 服务起不来）。
	DirectKey *string   `gorm:"size:64" json:"-"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// 非持久化展示字段：单聊接口（CreateDirect）实时填充对方最近上线时间，不落库
	LastLoginAt *time.Time `gorm:"-" json:"lastLoginAt,omitempty"`
}

func (Conversation) TableName() string { return "conversation" }

// ConversationMember 会话成员
type ConversationMember struct {
	ID             int64  `gorm:"primaryKey" json:"id,string"`
	ConversationID int64  `gorm:"index:uk_conv_user,unique" json:"conversationId,string"`
	UserID         int64  `gorm:"index:uk_conv_user,unique;index:idx_user" json:"userId,string"`
	Role           int    `gorm:"default:3" json:"role"`
	Nickname       string `gorm:"size:64" json:"nickname"`
	Mute           int    `gorm:"default:0" json:"mute"`
	// 禁言（发言）：群主/管理员对成员设置，unix 秒时间戳，0=未禁言。
	// 与 Mute（免打扰，个人开关）互不相干。
	SpeakMutedUntil int64 `gorm:"default:0" json:"speakMutedUntil"`
	Pinned          int   `gorm:"default:0" json:"pinned"`
	// Hidden：删除会话（从我的会话列表移除）。1=已删除。
	// 注意这是**软删除**——不能物理删这行：群聊下删 member 行等于把人踢出群，
	// 而「删除会话」的语义只是不在我列表里显示，群成员身份必须保留。
	// ConversationMember 在 store.go 的 AutoMigrate 白名单里，加字段会自动建列。
	Hidden        int   `gorm:"default:0" json:"hidden"`
	LastReadMsgID int64 `gorm:"default:0" json:"lastReadMsgId,string"`
	// ClearedMsgID 「删除聊天记录」软删位点（POST /conversation/:id/clear-history）：
	// 单聊任一方触发时把**双方**成员行的该位点推进到触发时刻的最大 msg_id；
	// History/ConvList 只下发 msg_id > 位点的消息。消息本体不删（后台可查原文），
	// 白名单内加列自动建列，migrations/024 幂等兜底老库。
	// ClearedSeq 同款语义的 seq 位点：Sync 补拉按 seq 增量，断点落在被删窗口内时
	// 靠它过滤（无 seq 位点会复现「已删消息 + 新消息一起补拉回来」）。
	ClearedMsgID int64     `gorm:"default:0" json:"clearedMsgId,string"`
	ClearedSeq   int64     `gorm:"default:0" json:"clearedSeq"`
	JoinedAt     time.Time `json:"joinedAt"`
	// Archived 个人归档开关（PUT /conversation/:id/archive）：1=已归档。
	// 与 Mute/Pinned 同属「我的会话个人状态」，白名单内加列自动，migrations/023 兜底老库。
	Archived int `gorm:"default:0" json:"archived"`
}

func (ConversationMember) TableName() string { return "conversation_member" }
