package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 客户端可能在预检里声明的自定义头兜底白名单。
// 仅当请求没带 Access-Control-Request-Headers 时才用（见下方说明）。
const corsDefaultAllowHeaders = "Origin, Content-Type, Authorization, X-Client-Platform, X-Device-ID"

// CORS 跨域中间件（Flutter H5 / 多端 Web / Electron 桌面端访问 API 必需）
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			origin = "*"
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")

		// Access-Control-Allow-Headers **回显客户端在预检中声明的字段**，而不是写死白名单。
		//
		// 背景（2026-09-08 桌面端 exe 踩坑，勿改回写死列表）：
		//   前端 im-pc 每个请求都会带 X-Client-Platform / X-Device-ID 两个自定义头
		//   （src/api/client.js 的 http()）。原先这里写死
		//   "Origin, Content-Type, Authorization"，不含这两个头，于是：
		//     - 网页版走 nginx 同源反代，**同源请求根本不触发 CORS**，一切正常；
		//     - Electron exe 的页面来源是 http://127.0.0.1:<随机端口>，属跨源，
		//       预检被拒 → 浏览器直接拦掉真实请求 → 页面只看到一句 "Failed to fetch"。
		//   极具迷惑性的一点：预检仍然返回 **204**，用 curl 看 status 会以为通过了，
		//   必须逐项比对 Access-Control-Allow-Headers 才能发现。
		//
		// 回显而非写死，是为了以后前端再加自定义头时不用再动后端。
		// 注意：Allow-Credentials 为 true 时，Allow-Headers 不能用 "*"，
		// 所以这里回显具体值（这也是各主流 CORS 中间件的默认行为）。
		allowHeaders := c.GetHeader("Access-Control-Request-Headers")
		if allowHeaders == "" {
			allowHeaders = corsDefaultAllowHeaders
		}
		c.Header("Access-Control-Allow-Headers", allowHeaders)

		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
