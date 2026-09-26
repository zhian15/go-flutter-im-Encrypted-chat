package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"
)

// LoginIPBan 登录接口前置拦截：被临时封锁（密码错误 5 次）的 IP 直接拒绝，
// 省去进 service 做密码校验。计数与封锁标记在 service.Login（失败累加、成功清除），
// 此处只查 Redis 封禁键；Redis 异常时放行，不误伤全体。
//
// 依赖 cmd/api/main.go 的 SetTrustedProxies：反代场景下 c.ClientIP() 才能拿到
// 真实客户端 IP，否则会拿到代理 IP、把代理本身拉黑。
func LoginIPBan() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if ip == "" {
			c.Next()
			return
		}
		n, err := store.RDB.Exists(c.Request.Context(), "login:ban:ip:"+ip).Result()
		if err == nil && n > 0 {
			c.JSON(http.StatusOK, gin.H{"code": errs.IPBanned.Code, "message": errs.IPBanned.Msg})
			c.Abort()
			return
		}
		c.Next()
	}
}
