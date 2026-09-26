package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"
)

// ============ 会话文件夹（PC 归档/文件夹需求：服务端同步版） ============
// 文件夹是用户级个人视图分组：名称 + 颜色 + 包含的会话 ID 列表。
// 不在 AutoMigrate 白名单，表由 migrations/023_archived_folder.sql 建。

// FolderItem 文件夹出参（chatIds 恒为数组，空文件夹给 []）
type FolderItem struct {
	ID      int64    `json:"id,string"`
	Name    string   `json:"name"`
	Color   string   `json:"color"`
	ChatIDs []string `json:"chatIds"` // 字符串化会话 ID（雪花超 2^53，沿用全局 ,string 契约）
}

func folderToItem(f model.ConversationFolder) FolderItem {
	item := FolderItem{ID: f.ID, Name: f.Name, Color: f.Color, ChatIDs: []string{}}
	_ = json.Unmarshal([]byte(f.ChatIDs), &item.ChatIDs)
	if item.ChatIDs == nil {
		item.ChatIDs = []string{}
	}
	return item
}

// ListFolders 当前用户的文件夹列表（按创建顺序）
func ListFolders(ctx context.Context, userID int64) ([]FolderItem, error) {
	var rows []model.ConversationFolder
	if err := store.DB.WithContext(ctx).
		Where("user_id = ?", userID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]FolderItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, folderToItem(r))
	}
	return out, nil
}

// SaveFolder 新建（id=0）或更新（id>0）。归属校验：只能动自己的文件夹。
func SaveFolder(ctx context.Context, userID, id int64, name, color string, chatIDs []string) (*FolderItem, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errs.FolderNameRequired
	}
	if len(name) > 64 {
		name = name[:64]
	}
	if len(color) > 16 {
		color = color[:16]
	}
	chatJSON := "[]"
	if chatIDs != nil {
		if b, err := json.Marshal(chatIDs); err == nil {
			chatJSON = string(b)
		}
	}
	if id > 0 {
		res := store.DB.WithContext(ctx).Model(&model.ConversationFolder{}).
			Where("id = ? AND user_id = ?", id, userID).
			Updates(map[string]interface{}{"name": name, "color": color, "chat_ids": chatJSON})
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 {
			return nil, errs.FolderNotFound
		}
		var f model.ConversationFolder
		if err := store.DB.WithContext(ctx).Where("id = ?", id).First(&f).Error; err != nil {
			return nil, err
		}
		out := folderToItem(f)
		return &out, nil
	}
	f := model.ConversationFolder{
		UserID: userID, Name: name, Color: color, ChatIDs: chatJSON,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.DB.WithContext(ctx).Create(&f).Error; err != nil {
		return nil, err
	}
	out := folderToItem(f)
	return &out, nil
}

// DeleteFolder 删除文件夹（只删文件夹本身，不影响会话）
func DeleteFolder(ctx context.Context, userID, id int64) error {
	res := store.DB.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&model.ConversationFolder{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.FolderNotFound
	}
	return nil
}
