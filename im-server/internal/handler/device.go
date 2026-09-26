package handler

import (
	"net/http"
	"strings"

	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/service"

	"github.com/gin-gonic/gin"
)

// ============ 设备会话（活跃会话 / 注销设备） ============

// DeviceListHandler 活跃会话列表（正在登录的设备）
func DeviceListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		current := strings.TrimSpace(c.GetHeader("X-Device-ID"))
		list, err := service.DeviceSessions(c.Request.Context(), uid, current)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": list}})
	}
}

// DeviceLogoutHandler 注销单台设备（不能注销当前设备，请走 /auth/logout）
func DeviceLogoutHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		deviceID := strings.TrimSpace(c.Param("deviceId"))
		if deviceID == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if current := strings.TrimSpace(c.GetHeader("X-Device-ID")); current != "" && current == deviceID {
			c.JSON(http.StatusOK, gin.H{"code": 1003, "message": "不能注销当前设备，请使用退出登录"})
			return
		}
		if err := service.LogoutDevice(c.Request.Context(), uid, deviceID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// PushTokenHandler 上报推送 token（个推 client id）：
// 存 device.push_token 并按 alias（=用户 ID）在个推侧绑定；unbind=true 反向解绑（退出登录）。
// 请求体：{"provider":"getui","token":"<cid>"}（可选 "unbind":true）
func PushTokenHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			Provider string `json:"provider"`
			Token    string `json:"token"`
			Unbind   bool   `json:"unbind"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		deviceID := strings.TrimSpace(c.GetHeader("X-Device-ID"))
		if !body.Unbind && strings.TrimSpace(body.Token) == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "token 为空"})
			return
		}
		if err := service.SavePushToken(c.Request.Context(), uid, deviceID,
			body.Provider, strings.TrimSpace(body.Token), body.Unbind); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}
