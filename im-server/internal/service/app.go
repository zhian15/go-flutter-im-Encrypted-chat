package service

import (
	"context"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/store"
)

// AppList 已上架小程序列表（客户端"发现"页展示）
func AppList(ctx context.Context) ([]model.AppEntry, error) {
	var apps []model.AppEntry
	err := store.DB.Where("enabled = 1").Order("sort asc, id asc").Find(&apps).Error
	return apps, err
}
