package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// 消息类型
const (
	MsgText   = 1
	MsgImage  = 2
	MsgFile   = 3
	MsgVoice  = 4
	MsgVideo  = 5
	MsgSystem = 6
	MsgCall   = 7 // 音视频通话信令：content = JSON {"action":"invite|accept|reject|hangup","callType":"voice|video","roomId":xxx}
	// 8 红包 / 9 转账：content = JSON {"kind":"redpacket|transfer","amount":xx,"note":"","mode":"lucky|normal","count":n,"toUserId":""}
	// 发这类消息时**服务端必须原子扣款**（见 service.SendMoneyCharge），不能靠客户端事后记账
	MsgRedPacket = 8
	MsgTransfer  = 9
	// 10 名片：content = JSON，kind 三种（**服务端透传不校验内容**，展示/校验由客户端做）：
	//   用户名片（kind 缺省兼容旧数据，读 userId 字段）：
	//     {"userId":"<雪花字符串>","nickname":"..","avatar":"<可空>"}
	//   群/频道名片（前端「分享名片」构造；id=会话 ID 雪花字符串）：
	//     {"kind":"group","id":"<雪花字符串>","name":"..","avatar":"<可空>"}
	//     {"kind":"channel","id":"<雪花字符串>","name":"..","avatar":"<可空>"}
	// 点击解析端点：user→GET /user/:id；group→GET /conversation/:id/preview；
	// channel→GET /channel/:id。加入：group→POST /conversation/:id/join；
	// channel→POST /channel/:id/follow（详见 doc/API.md 名片契约节）。
	MsgCard = 10
	// 11 网页小程序卡片：content = JSON {"url":...,"title":...,"icon":...,"description":...,
	// "inApp":bool,"displayName":...,"matched":bool}，由 /link/meta 预览接口产出
	MsgLinkCard = 11
	// 12 合并转发（聊天记录）：content = JSON {"v":1,"count":n,"users":"A、B",
	// "items":[{"senderId","senderName","type","content","time"}]}，服务端透传不校验；
	// 点击由客户端解析进「聊天记录详情页」，支持二次转发（嵌套由客户端拍平）
	MsgMergeForward = 12
	// 13 端到端加密文本（2026-09-18 §36 定稿，仅单聊）：content = JSON
	// {"v":1,"kid":"<b64 16B>","n":"<b64 12B nonce>","c":"<b64 AES-256-GCM 密文>",
	//  "kw":[{"u":"<uid>","ep":"<b64 临时 X25519 公钥>","n":"<b64>","c":"<b64 包裹的会话密钥>"}, ...]}
	// kw 两份：对方公钥 + 自己公钥各一份（ECIES：临时密钥 ECDH→HKDF→AES-GCM 包 32B 会话密钥）。
	// 服务端透传不校验内容、不可解密；后台审计/搜索对 type=13 失效（定稿口径）。
	// 老客户端：default 分支渲染占位；会话摘要固定「[加密消息]」。
	MsgE2Text = 13
	// 14 位置消息（2026-09-21，对接谷歌地图）：content = JSON
	// {"lat":<纬度>,"lng":<经度>,"name":"<地名简称>","address":"<详细地址>"}
	// 服务端透传不校验内容；客户端气泡用静态地图图（Google Static Maps，
	// key 由 sys_config google_maps_api_key 下发），点击跳外部地图 App。
	MsgLocation = 14
)

// 消息状态
const (
	MsgStatusNormal   = 1
	MsgStatusRecalled = 2
)

// Message MongoDB 消息
type Message struct {
	ID             primitive.ObjectID     `bson:"_id,omitempty" json:"-"`
	ConversationID int64                  `bson:"conversation_id" json:"conversationId,string"`
	MsgID          int64                  `bson:"msg_id" json:"msgId,string"`       // 全局雪花 ID
	ClientMsgID    string                 `bson:"client_msg_id" json:"clientMsgId"` // 客户端幂等 ID（UUID）
	Seq            int64                  `bson:"seq" json:"seq"`                   // 会话内单调递增序号（服务端生成，用于顺序/补拉）
	SenderID       int64                  `bson:"sender_id" json:"senderId,string"`
	Type           int                    `bson:"type" json:"type"`
	Content        string                 `bson:"content" json:"content"`
	File           map[string]interface{} `bson:"file,omitempty" json:"file,omitempty"`
	Mention        []int64                `bson:"mention,omitempty" json:"mention,omitempty"`
	ReplyTo        int64                  `bson:"reply_to,omitempty" json:"replyTo,string"`
	// 引用快照：被引用消息内容/发送者（发送时冗余，前端引用条直接显示原内容，不查库）
	ReplySnapshot map[string]interface{} `bson:"reply_snapshot,omitempty" json:"replySnapshot,omitempty"`
	Recalled      bool                   `bson:"recalled" json:"recalled"`
	RecalledBy    int64                  `bson:"recalled_by,omitempty" json:"recalledBy,string"`
	Status        int                    `bson:"status" json:"status"`
	// 派送状态（需求4：单聊对方 last_read_msg_id ≥ msg_id → "read"，否则 "sent"；非持久化字段，查询时填充）
	Delivery string `bson:"-" json:"deliveryState,omitempty"`
	// E2E：开启时 content 为密文（服务端可解密）
	Encrypted bool `bson:"encrypted" json:"encrypted"`
	// 屏蔽：后台消息审计中被管理员屏蔽的消息（历史拉取/增量同步不再下发）
	Blocked   bool      `bson:"blocked,omitempty" json:"blocked"`
	CreatedAt time.Time `bson:"created_at" json:"createdAt"`
}

// MessageReceipt 已读回执
type MessageReceipt struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	ConversationID int64              `bson:"conversation_id" json:"conversationId,string"`
	MsgID          int64              `bson:"msg_id" json:"msgId,string"`
	UserID         int64              `bson:"user_id" json:"userId,string"`
	ReadAt         time.Time          `bson:"read_at" json:"readAt"`
}

// MessageFavorite 收藏
type MessageFavorite struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	UserID         int64              `bson:"user_id" json:"userId,string"`
	ConversationID int64              `bson:"conversation_id" json:"conversationId,string"`
	MsgID          int64              `bson:"msg_id" json:"msgId,string"`
	CreatedAt      time.Time          `bson:"created_at" json:"createdAt"`
}
