package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/repo"
	"github.com/yourcompany/im-server/internal/service"
)

// GroupFileListHandler GET /api/v1/conversation/:id/files （功能 A-1）
// Query: category(image|document|video|all) keyword sort(time|size) order(asc|desc) page page_size
// 出参：list(file_id,name,size,mime,category,uploader_id,uploader_name,created_at,preview_status) +
//
//	total（分页）
//
// 错误：403 非成员 / 400 参数非法。
func GroupFileListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		// 路由注册为 /conversation/:id/files（handler.go:457，与同组 /conversation/:id/* 一致
		// 用 :id——gin 同段参数名不可并存，注册成 :convId 会 panic）。旧代码误读 c.Param("convId")
		// 恒为空串 → ParseInt 失败 → 恒 400「参数错误」，前端表现为「文件列表加载失败」。
		convID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || convID <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误"})
			return
		}
		if !service.IsConvMember(c.Request.Context(), convID, uid) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "非群成员无权访问"})
			return
		}
		q := repo.ConvFileQuery{
			ConvID:   convID,
			Category: c.DefaultQuery("category", "all"),
			Keyword:  c.Query("keyword"),
			Sort:     c.DefaultQuery("sort", "time"),
			Order:    c.DefaultQuery("order", "desc"),
			Page:     atoiDefault(c.Query("page"), 1),
			PageSize: atoiDefault(c.Query("page_size"), 20),
		}
		if q.Page <= 0 {
			q.Page = 1
		}
		if q.PageSize <= 0 || q.PageSize > 100 {
			q.PageSize = 20
		}

		res, err := repo.ListConvFiles(c.Request.Context(), q)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
			return
		}

		// 上传者昵称批量映射
		ids := make([]int64, 0, len(res.List))
		for _, f := range res.List {
			ids = append(ids, f.UploaderID)
		}
		names := service.UserNameMap(c.Request.Context(), ids)

		list := make([]gin.H, 0, len(res.List))
		for _, f := range res.List {
			list = append(list, gin.H{
				"fileId":        f.FileID,
				"name":          f.Name,
				"size":          f.Size,
				"mime":          f.Mime,
				"category":      f.Category,
				"uploaderId":    strconv.FormatInt(f.UploaderID, 10),
				"uploaderName":  names[f.UploaderID],
				"createdAt":     f.CreatedAt.Format(time.RFC3339),
				"previewStatus": f.PreviewStatus,
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "ok",
			"data": gin.H{
				"list":  list,
				"total": res.Total,
			},
		})
	}
}

// atoiDefault 解析整数，失败/缺省返回 def。
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
