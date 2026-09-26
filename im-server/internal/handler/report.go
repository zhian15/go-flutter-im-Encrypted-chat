package handler

import (
	"log"
	"net/http"

	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/service"

	"github.com/gin-gonic/gin"
)

// ReportCreateHandler 用户端提交投诉（会话设置「投诉」入口）
func ReportCreateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var req service.ReportCreateReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.ReportCreate(c.Request.Context(), uid, &req); err != nil {
			// 业务错误透传；底层 DB 错误不抛英文原文给用户
			if e, ok := err.(*errs.Err); ok {
				c.JSON(http.StatusOK, gin.H{"code": e.Code, "message": e.Msg})
				return
			}
			log.Printf("[report] create failed uid=%d err=%v", uid, err)
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "提交失败，请稍后重试"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}
