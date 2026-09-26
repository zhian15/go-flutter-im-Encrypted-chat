package handler

import (
	"net/http"
	"strings"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	jwtx "github.com/yourcompany/im-server/internal/pkg/jwt"
	"github.com/yourcompany/im-server/internal/service"

	"github.com/gin-gonic/gin"
)

// CaptchaHandler 图形验证码（防刷）
func CaptchaHandler(c *gin.Context) {
	cid, b64, err := service.Captcha(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": "验证码生成失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
		"captchaId": cid, "image": b64,
	}})
}

// SendCodeHandler 发送短信/邮箱验证码
func SendCodeHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.SendCodeReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		channel, err := service.SendCode(c.Request.Context(), cfg, &req)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"channel": channel}})
	}
}

// RegisterHandler 注册
func RegisterHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.RegisterReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		u, access, refresh, err := service.Register(c.Request.Context(), cfg, &req, c.ClientIP())
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"user": u, "accessToken": access, "refreshToken": refresh,
		}})
	}
}

// LoginHandler 登录
func LoginHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.LoginReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		res, err := service.Login(c.Request.Context(), cfg, &req, c.ClientIP(), c.GetHeader("X-Client-Version"))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		if res.VerifyRequired {
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
				"verifyRequired": true,
				"verifyTicket":   res.VerifyTicket,
				"method":         res.Method,
				"approveDevice":  res.ApproveDevice,
				"allowApprove":   res.AllowApprove,
				"expiresIn":      res.ExpiresIn,
			}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"user": res.User, "accessToken": res.AccessToken, "refreshToken": res.RefreshToken,
		}})
	}
}

// CheckAccountHandler 账号可用性查询（公开）。
// 前端注册页在用户填完用户名时调用，用于提示「用户名可用 / 该用户名已被使用」。
// 按 IP 限流（每分钟 30 次）；响应恒为 200，业务错误放在 body.code。
func CheckAccountHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.CheckAccountRateLimited(c.Request.Context(), c.ClientIP()); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		ok, err := service.AccountAvailable(c.Request.Context(), c.Query("account"))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"available": ok}})
	}
}

// GuestRegisterHandler 游客注册/登录（按设备号幂等：已存在游客则直接登录，否则新建）
func GuestRegisterHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.GuestRegisterReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		// snake_case 别名归一（im-pc 发 device_id）：camelCase 缺失时回退，再校验非空
		req.DeviceID, req.DeviceType = service.NormalizeDevice(req.DeviceID, req.DeviceType, req.DeviceIDAlias, req.DeviceTypeAlias)
		if req.DeviceID == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		u, access, refresh, isNew, err := service.GuestRegister(c.Request.Context(), cfg, &req, c.ClientIP())
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"user": u, "accessToken": access, "refreshToken": refresh, "isNewGuest": isNew,
		}})
	}
}

// InviteBindHandler 登录后补填邀请码（游客/普通用户通用）。
// 复用现有邀请码逻辑：一次性码 consume + 自定义好友码自动加好友，无需客户端关心细节。
func InviteBindHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Code string `json:"code"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Code) == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		uid := middleware.CurrentUserID(c)
		if err := service.BindInviteCode(c.Request.Context(), body.Code, uid); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// RefreshHandler 刷新 token
func RefreshHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			RefreshToken string `json:"refreshToken"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.RefreshToken == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		access, err := service.Refresh(c.Request.Context(), cfg, body.RefreshToken)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"accessToken": access}})
	}
}

// LogoutHandler 登出（只登出本设备）。
// 设备号来源优先级：请求头 X-Device-ID > body.refreshToken 的 did 声明 > "default"。
func LogoutHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		deviceID := strings.TrimSpace(c.GetHeader("X-Device-ID"))
		if deviceID == "" {
			var body struct {
				RefreshToken string `json:"refreshToken"`
			}
			if err := c.ShouldBindJSON(&body); err == nil && body.RefreshToken != "" {
				if claims, err := jwtx.Parse(cfg.JWTSecret, body.RefreshToken); err == nil {
					deviceID = claims.DeviceID
				}
			}
		}
		service.Logout(c.Request.Context(), uid, deviceID)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// 统一错误码提取
func errCode(err error) int {
	if e, ok := err.(*errs.Err); ok {
		return e.Code
	}
	return 500
}

// VerifyCodeHandler 验证码方式校验（PC 在小助手开启时收码输入）
func VerifyCodeHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			VerifyTicket string `json:"verifyTicket" binding:"required"`
			Code         string `json:"code" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		res, err := service.VerifyByCode(c.Request.Context(), cfg, req.VerifyTicket, req.Code)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"user": res.User, "accessToken": res.AccessToken, "refreshToken": res.RefreshToken,
		}})
	}
}

// VerifyApproveHandler 设备批准（审批方调用，需登录态且 X-Device-ID 命中审批设备）
func VerifyApproveHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			VerifyTicket string `json:"verifyTicket" binding:"required"`
			Decision     string `json:"decision" binding:"required"` // allow | reject
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		dev := strings.TrimSpace(c.GetHeader("X-Device-ID"))
		if err := service.ApproveLogin(c.Request.Context(), cfg, req.VerifyTicket, req.Decision, dev); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"ok": true}})
	}
}

// VerifyPollHandler 新设备轮询验证结果（公开，新设备尚未登录、无 token）
func VerifyPollHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		ticket := strings.TrimSpace(c.Query("ticket"))
		if ticket == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		res, err := service.VerifyPoll(c.Request.Context(), ticket)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		if res.VerifyRequired {
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"status": "pending"}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"status": "approved", "user": res.User,
			"accessToken": res.AccessToken, "refreshToken": res.RefreshToken,
		}})
	}
}

// VerifySwitchHandler 切换验证方式（新设备侧）：code/approve 互切。
// 公开（新设备尚未登录）；code 需小助手开启，approve 需存在最近可信设备。
func VerifySwitchHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			VerifyTicket string `json:"verifyTicket" binding:"required"`
			Method       string `json:"method" binding:"required"` // code | approve
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.VerifySwitch(c.Request.Context(), cfg, req.VerifyTicket, req.Method); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}
