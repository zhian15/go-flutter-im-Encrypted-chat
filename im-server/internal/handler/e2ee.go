package handler

// E2EE 端到端加密接口（2026-09-18 §36 定稿）。
// 路由注册在 handler.go 的登录态 user 组：
//   GET  /api/v1/e2ee/pubkey/:uid   取对方公钥（发消息前）
//   GET  /api/v1/e2ee/me            本人密钥状态（含备份密文，仅属主）
//   PUT  /api/v1/e2ee/keys          生成/轮换身份密钥对
//   PUT  /api/v1/e2ee/backup        只更新私钥备份（改密码 re-wrap）
//   PUT  /api/v1/e2ee/toggle        用户级开关（默认开、可关）

import (
	"net/http"
	"strconv"

	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/service"

	"github.com/gin-gonic/gin"
)

// E2eePubKeyHandler 取某用户的端到端公钥。
func E2eePubKeyHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		out, err := service.E2eePubKey(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": out})
	}
}

// E2eeMeHandler 本人密钥状态（模式 + 公钥 + 开关 + 备份密文）。
func E2eeMeHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		out, err := service.E2eeMe(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": out})
	}
}

// E2eePutKeysHandler 生成/轮换身份密钥对（客户端生成，服务端不接触私钥明文）。
func E2eePutKeysHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			PublicKey string `json:"publicKey"`
			EncPriv   string `json:"encPriv"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.E2eePutKeys(c.Request.Context(), uid, body.PublicKey, body.EncPriv); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// E2eePutBackupHandler 只更新私钥备份密文（改密码 re-wrap，公钥不变）。
func E2eePutBackupHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			EncPriv string `json:"encPriv"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.E2eePutBackup(c.Request.Context(), uid, body.EncPriv); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// E2eeToggleHandler 用户级端到端开关。
func E2eeToggleHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			On *bool `json:"on" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.E2eeToggle(c.Request.Context(), uid, *body.On); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// ============ 跨设备恢复（申请从旧设备恢复，2026-09-18）============

// E2eeRecoverRequestHandler 新设备发起恢复申请（临时公钥 + 设备名）。
func E2eeRecoverRequestHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			NewPub     string `json:"newPub"`
			DeviceName string `json:"deviceName"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		out, err := service.E2eeRecoverRequest(c.Request.Context(), uid, body.NewPub, body.DeviceName)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": out})
	}
}

// E2eeRecoverStatusHandler 新设备轮询恢复结果（payload 一次性读取）。
func E2eeRecoverStatusHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		out, err := service.E2eeRecoverStatus(c.Request.Context(), uid, c.Query("id"))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": out})
	}
}

// E2eeRecoverApproveHandler 旧设备批准/拒绝（批准带包裹后的私钥 payload）。
func E2eeRecoverApproveHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			RequestID string            `json:"requestId"`
			Approve   *bool             `json:"approve" binding:"required"`
			Payload   map[string]string `json:"payload"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.E2eeRecoverApprove(c.Request.Context(), uid, body.RequestID, *body.Approve, body.Payload); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}
