package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/service"
)

// TranslateHandler POST /api/v1/translate
// body: { "text": "原文", "targetLang": "zh|zhT|en|ja", "auto": false }
// auto=true 消耗自动翻译额度（后台可配每日次数），false 走手动翻译额度
func TranslateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			Text       string `json:"text"`
			TargetLang string `json:"targetLang"`
			Auto       bool   `json:"auto"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Text == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		out, err := service.Translate(c.Request.Context(), uid, body.Text, body.TargetLang, body.Auto)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"text":       out,
			"targetLang": body.TargetLang,
		}})
	}
}

// TranslateUsageHandler GET /api/v1/translate/usage
// 返回当日用量与限额，客户端「AI 翻译」设置页展示
func TranslateUsageHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		data := service.TranslateUsage(c.Request.Context(), uid)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
	}
}
