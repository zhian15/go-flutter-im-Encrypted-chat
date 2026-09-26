package model

import "time"

// MomentsPost 朋友圈动态
// user_id = -1 表示小助手（管理员后台以小助手身份发布）
// hidden = true 屏蔽：仅发布者自己可见，好友不可见
type MomentsPost struct {
	ID        int64     `gorm:"primaryKey" json:"id,string"`
	UserID    int64     `gorm:"index" json:"userId,string"`
	Content   string    `gorm:"type:text" json:"content"`
	Images    string    `gorm:"type:text" json:"images"` // JSON 数组 ["url1","url2"]
	Likes     string    `gorm:"type:text" json:"likes"`  // JSON 数组 ["uid1","uid2"]
	Hidden    bool      `gorm:"default:false;index" json:"hidden"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (MomentsPost) TableName() string { return "moments_post" }

// MomentsComment 朋友圈评论
type MomentsComment struct {
	ID        int64     `gorm:"primaryKey" json:"id,string"`
	PostID    int64     `gorm:"index" json:"postId,string"`
	UserID    int64     `gorm:"index" json:"userId,string"`
	Content   string    `gorm:"size:500" json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

func (MomentsComment) TableName() string { return "moments_comment" }
