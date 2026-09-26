package handler

import (
	"net/http"

	"github.com/yourcompany/im-server/internal/service"

	"github.com/gin-gonic/gin"
)

// AppListHandler 已上架小程序列表（发现页）
func AppListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		apps, err := service.AppList(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "获取失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": apps})
	}
}
