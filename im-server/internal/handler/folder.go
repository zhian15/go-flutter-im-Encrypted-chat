package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/service"
)

// ============ 会话文件夹（PC 归档/文件夹需求：服务端同步版） ============
// 路由用顶层 /folders，避开 /conversation/:id 的参数路由树（gin 通配冲突风险）。
// 数据模型见 internal/model/folder.go；表由 migrations/023_archived_folder.sql 建。

// FolderListHandler GET /folders —— 当前用户文件夹列表（按创建顺序）
func FolderListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		items, err := service.ListFolders(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": items})
	}
}

// FolderSaveHandler POST /folders（新建，id 不传）| PUT /folders/:id（更新）
// body: {name, color, chatIds: ["会话id",...]}，chatIds 全量覆盖
func FolderSaveHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		// POST 时无 :id 段，ParseInt("") 恒为 0 → 走新建分支
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			Name    string   `json:"name"`
			Color   string   `json:"color"`
			ChatIDs []string `json:"chatIds"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		item, err := service.SaveFolder(c.Request.Context(), uid, id, body.Name, body.Color, body.ChatIDs)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": item})
	}
}

// FolderDeleteHandler DELETE /folders/:id —— 删除文件夹（不影响会话）
func FolderDeleteHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if err := service.DeleteFolder(c.Request.Context(), uid, id); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}
