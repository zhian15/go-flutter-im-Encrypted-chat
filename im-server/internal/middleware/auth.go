package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	jwtx "github.com/yourcompany/im-server/internal/pkg/jwt"
	"github.com/yourcompany/im-server/internal/service"
	"github.com/yourcompany/im-server/internal/store"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	CtxUserID = "uid"
	CtxRole   = "role"
)

// Auth JWT 鉴权中间件
// 注意：鉴权失败必须返回 HTTP 401（不是 200+code）——
// 三端（App dio onError / PC fetch response.ok / 后台 axios 拦截器）的
// 自动刷新 token 逻辑全部依赖 HTTP 状态码 401 触发。
// 之前返回 200 导致 token 过期后客户端静默拿到空数据：
// 列表页全显示"暂无会话/暂无好友"，空列表还会污染本地缓存（bug：过段时间打开 App 全空）。
func Auth(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errs.Unauthorized)
			return
		}
		token := strings.TrimPrefix(header, "Bearer ")
		claims, err := jwtx.Parse(cfg.JWTSecret, token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errs.Unauthorized)
			return
		}
		// 复核账号状态：禁用账号的 access token 在有效期内本应继续可用，
		// 但后台"禁用"必须即时生效 —— 任何请求都返回 401，
		// 三端据此清登录态并跳登录页（见各端 onUnauthorized / forceLogout 处理）。
		// 同时复核令牌版本：改密码 / 被禁用 / 管理员重置密码会令 token_version +1，
		// 携带旧版本的已签发 access token 立即失效（同样只在查到用户时才判断）。
		// 仅在能确认"用户不存在或状态非正常"时才拦截；真正的 DB 故障则放行，
		// 避免一次数据库抖动把全体在线用户踢下线。
		//
		// 关于 store.DB 为 nil：这里**故意不加** nil 短路，因为 api 进程启动即
		// InitMySQL + Fatalf（见 cmd/api/main.go），DB 连不上进程根本不会起来，
		// 因此本处 store.DB 恒非 nil。真的为 nil 时对 nil *gorm.DB 调 Select 会 panic，
		// 被 gin.Recovery 转成 500 —— 即**全部请求 fail-closed**，对鉴权中间件是正确的默认方向。
		// ⚠ 若将来把 api 的 MySQL 初始化也改成"只告警不退出"（像 gateway 那样，见 store.InitMySQLNoMigrate），
		// 就必须在此显式处理 nil，并且要明确选 fail-closed 还是 fail-open ——
		// 注意 WS 侧 handleWS 的同类分支是 fail-open（放行 + 计入 wsRecheck.skippedNoDb），
		// 两侧在这个状态下**语义不同**，是有意为之，不要为"看起来一致"而无脑照搬。
		var u model.User
		err = store.DB.Select("status", "token_version").Where("id = ?", claims.UserID).First(&u).Error
		// 注意 ErrRecordNotFound 必须与"DB 故障"区分开：
		// 它是"查询成功但没有这一行"，即账号已被物理删除（如后台 AdminDataClear scope=users，
		// 见 service/admin.go:447 的 Delete(&model.User{})）。此时必须拦 ——
		// 否则被删用户凭删号前签发的 token 仍能在 access token 剩余有效期（默认 24h）内
		// 继续调用全部 API，成为没有数据库记录的幽灵账号。
		// ⚠ WS 侧 service/ws.go 的 handleWS 有同一套判定，其中 **ErrRecordNotFound / status /
		// token_version 这三条必须同向**（改一处务必核对另一处）。
		// 唯一例外是上面说的 `store.DB == nil` 分支：两侧**有意不等价**，不要为"看起来一致"去改。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errs.Unauthorized)
			return
		}
		if err == nil && (u.Status != model.StatusNormal || u.TokenVersion != claims.Ver) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errs.Unauthorized)
			return
		}
		// 设备级令牌失效（二期落地，2026-09-24）：access token 携带 device_id 时，复核该设备的
		// refresh 槽位是否仍存在（被注销 / 单设备登出后槽位被删）。槽位不存在 → 401，
		// 三端据此清登录态并跳登录页（见各端 onUnauthorized / forceLogout 处理）。
		// 仅当 token 带 device_id 时校验（老 token 无 did 放行）；Redis 异常 fail-open，避免抖动误伤。
		if claims.DeviceID != "" && store.RDB != nil {
			if !service.DeviceHasSlot(c.Request.Context(), claims.UserID, claims.DeviceID) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, errs.Unauthorized)
				return
			}
		}
		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxRole, claims.Role)
		c.Next()
	}
}

// RequireAdmin 管理员中间件（配合 Auth 使用）
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if role, ok := c.Get(CtxRole); !ok || role.(int) != model.RoleAdmin {
			c.AbortWithStatusJSON(http.StatusOK, errs.Forbidden)
			return
		}
		c.Next()
	}
}

// CurrentUserID 从上下文取当前用户 ID
func CurrentUserID(c *gin.Context) int64 {
	if v, ok := c.Get(CtxUserID); ok {
		return v.(int64)
	}
	return 0
}
