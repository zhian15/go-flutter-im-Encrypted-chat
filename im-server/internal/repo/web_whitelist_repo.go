package repo

import (
	"context"
	"errors"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"

	"gorm.io/gorm"
)

// WebWhitelistList 分页/关键词搜索白名单。
func WebWhitelistList(ctx context.Context, keyword string, page, pageSize int) ([]model.WebWhitelist, int64, error) {
	var list []model.WebWhitelist
	q := store.DB.Model(&model.WebWhitelist{})
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("domain LIKE ? OR display_name LIKE ?", like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	if list == nil {
		list = []model.WebWhitelist{}
	}
	return list, total, nil
}

// WebWhitelistCreate 新增白名单；domain 重复返回 errs.DomainExists(409)。
func WebWhitelistCreate(ctx context.Context, w *model.WebWhitelist) (*model.WebWhitelist, error) {
	var cnt int64
	store.DB.Model(&model.WebWhitelist{}).Where("domain = ?", w.Domain).Count(&cnt)
	if cnt > 0 {
		return nil, errs.DomainExists
	}
	if err := store.DB.Create(w).Error; err != nil {
		return nil, err
	}
	return w, nil
}

// WebWhitelistUpdate 局部更新白名单。各字段指针为空表示不修改。
// 注意（跨端契约）：指针非空即写入，即使值是空串也会落库为空——
// 后台「清除」logo/cover 时显式传 ""，必须能清空，不能走 omitempty 语义。
// 修改 domain 时检测与其它记录冲突，冲突返回 errs.DomainExists(409)；
// 记录不存在返回 errs.NotFound(404)。
func WebWhitelistUpdate(ctx context.Context, id int64, domain, displayName, logo, cover *string, enabled, nativeBridge *int) (*model.WebWhitelist, error) {
	var exist model.WebWhitelist
	if err := store.DB.First(&exist, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.NotFound
		}
		return nil, err
	}
	upd := map[string]interface{}{}
	if domain != nil && *domain != exist.Domain {
		var cnt int64
		store.DB.Model(&model.WebWhitelist{}).Where("domain = ? AND id <> ?", *domain, id).Count(&cnt)
		if cnt > 0 {
			return nil, errs.DomainExists
		}
		upd["domain"] = *domain
	}
	if displayName != nil {
		upd["display_name"] = *displayName
	}
	if enabled != nil {
		upd["enabled"] = *enabled
	}
	if nativeBridge != nil {
		upd["native_bridge"] = *nativeBridge
	}
	if logo != nil {
		upd["logo"] = *logo
	}
	if cover != nil {
		upd["cover"] = *cover
	}
	if len(upd) > 0 {
		if err := store.DB.Model(&exist).Updates(upd).Error; err != nil {
			return nil, err
		}
	}
	// 同步内存字段：保证响应体返回更新后的最新值（含清空为空串的 logo/cover），
	// 不依赖 GORM 的回写行为。
	if domain != nil {
		exist.Domain = *domain
	}
	if displayName != nil {
		exist.DisplayName = *displayName
	}
	if logo != nil {
		exist.Logo = *logo
	}
	if cover != nil {
		exist.Cover = *cover
	}
	if enabled != nil {
		exist.Enabled = *enabled
	}
	if nativeBridge != nil {
		exist.NativeBridge = *nativeBridge
	}
	return &exist, nil
}

// WebWhitelistDelete 硬删除白名单记录。
func WebWhitelistDelete(ctx context.Context, id int64) error {
	return store.DB.Delete(&model.WebWhitelist{}, id).Error
}
