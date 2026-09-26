package model

import "time"

const (
	FriendReqPending  = 0
	FriendReqAgreed   = 1
	FriendReqRejected = 2
)

// FriendRelation 好友关系（双向：A-B 一条记录存 user_id=A；查询时同时查反向）
type FriendRelation struct {
	ID       int64  `gorm:"primaryKey" json:"id,string"`
	UserID   int64  `gorm:"index:uk_user_friend,unique" json:"userId,string"`
	FriendID int64  `gorm:"index:uk_user_friend,unique;index:idx_friend" json:"friendId,string"`
	Remark   string `gorm:"size:64" json:"remark"`
	// 来源：1 搜索添加 2 扫码添加 3 名片
	Source    int       `gorm:"default:1" json:"source"`
	CreatedAt time.Time `json:"createdAt"`
}

func (FriendRelation) TableName() string { return "friend_relation" }

// FriendRequest 好友申请
type FriendRequest struct {
	ID       int64  `gorm:"primaryKey" json:"id,string"`
	FromUser int64  `gorm:"index:idx_from" json:"fromUser,string"`
	ToUser   int64  `gorm:"index:idx_to" json:"toUser,string"`
	Message  string `gorm:"size:255" json:"message"`
	Status   int    `gorm:"default:0" json:"status"`
	// 来源：1 搜索添加 2 扫码添加 3 名片（可选项，默认 1；加列走 migrations/018）
	Source    int       `gorm:"default:1" json:"source"`
	CreatedAt time.Time `json:"createdAt"`
}

func (FriendRequest) TableName() string { return "friend_request" }

// Blacklist 黑名单
type Blacklist struct {
	ID          int64     `gorm:"primaryKey" json:"id,string"`
	UserID      int64     `gorm:"index:uk_user_block,unique" json:"userId,string"`
	BlockUserID int64     `gorm:"index:uk_user_block,unique" json:"blockUserId,string"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (Blacklist) TableName() string { return "blacklist" }
