package service

import (
	"context"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/pkg/id"
	"github.com/yourcompany/im-server/internal/store"
)

// ============ 投诉（会话设置「投诉」入口） ============
//
// 用户端：POST /conversation/report 落库即成功（无审批流，管理后台人工处理）。
// 管理端：GET /admin/reports 分页列表 + POST /admin/reports/:id/handle 标记已处理；
//         一键封禁复用既有 PUT /admin/users/:id/status {status:2}
//         （AdminUserSetStatus：token_version+1 + 清 refresh 槽位 + forceLogout 推送，
//         见 service/admin.go）——不新增封禁端点。

// ReportCreateReq 用户端投诉请求（契约与 im-app ConversationService.reportConversation 一致）
type ReportCreateReq struct {
	ConvID   int64  `json:"convId,string"`
	PeerID   int64  `json:"peerId,string"`
	Category string `json:"category"`
	Note     string `json:"note"`
}

// ReportCreate 用户提交投诉（落库即成功）。
// category 为客户端 l10n key（convSetReportSpam/Fraud/Harass/Impersonate/Other），
// 服务端不校验枚举（管理后台原样展示），只做长度与必填校验。
func ReportCreate(ctx context.Context, reporterID int64, req *ReportCreateReq) error {
	if req.ConvID <= 0 || strings.TrimSpace(req.Category) == "" {
		return errs.ParamError
	}
	note := req.Note
	if len(note) > 2000 { // 字节截断，防滥用（TEXT 上限远大于此）
		note = note[:2000]
	}
	category := req.Category
	if len(category) > 64 {
		category = category[:64]
	}
	r := model.Report{
		ID:         id.Next(),
		ReporterID: reporterID,
		PeerID:     req.PeerID,
		ConvID:     req.ConvID,
		Category:   category,
		Note:       note,
		Status:     0,
		CreatedAt:  time.Now(),
	}
	if err := store.DB.Create(&r).Error; err != nil {
		return err
	}
	return nil
}

// AdminReportItem 管理端列表项（昵称按当前数据现场填充）
type AdminReportItem struct {
	model.Report
	ReporterName string `json:"reporterName"`
	PeerName     string `json:"peerName"`
}

// AdminReportList 管理端投诉分页列表（含举报人/被投诉人昵称）
func AdminReportList(ctx context.Context, status int, page, size int) ([]AdminReportItem, int64, error) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 100 {
		size = 20
	}
	q := store.DB.Model(&model.Report{})
	if status > 0 { // 0/不传=全部；1=待处理；2=已处理（前端习惯 1/2，映射库内 0/1）
		if status == 1 {
			q = q.Where("status = 0")
		} else if status == 2 {
			q = q.Where("status = 1")
		}
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.Report
	if err := q.Order("created_at DESC").
		Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	// 批量取昵称（举报人 + 被投诉人去重后一条 IN）
	ids := make([]int64, 0, len(rows)*2)
	seen := make(map[int64]bool, len(rows)*2)
	for _, r := range rows {
		for _, uid := range []int64{r.ReporterID, r.PeerID} {
			if uid > 0 && !seen[uid] {
				seen[uid] = true
				ids = append(ids, uid)
			}
		}
	}
	nameMap := make(map[int64]string, len(ids))
	if len(ids) > 0 {
		var users []model.User
		store.DB.Select("id", "nickname").Where("id IN ?", ids).Find(&users)
		for i := range users {
			nameMap[users[i].ID] = users[i].Nickname
		}
	}
	items := make([]AdminReportItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, AdminReportItem{
			Report:       r,
			ReporterName: nameMap[r.ReporterID],
			PeerName:     nameMap[r.PeerID],
		})
	}
	return items, total, nil
}

// AdminReportHandle 管理端标记已处理（幂等：已处理直接成功）
func AdminReportHandle(ctx context.Context, reportID, adminID int64) error {
	now := time.Now()
	res := store.DB.Model(&model.Report{}).
		Where("id = ? AND status = 0", reportID).
		Updates(map[string]interface{}{"status": 1, "handled_by": adminID, "handled_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// 不存在，或已处理（幂等放行后者）
		var cnt int64
		store.DB.Model(&model.Report{}).Where("id = ?", reportID).Count(&cnt)
		if cnt == 0 {
			return errs.NotFound
		}
	}
	return nil
}
