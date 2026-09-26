package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/repo"
	"github.com/yourcompany/im-server/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterAdminRoutes 管理后台路由（Auth + RequireAdmin）
func RegisterAdminRoutes(r *gin.Engine, cfg *config.Config) {
	admin := r.Group("/api/v1/admin", middleware.Auth(cfg), middleware.RequireAdmin())
	{
		// 用户管理
		admin.GET("/users", func(c *gin.Context) {
			kw := c.Query("kw")
			status, _ := strconv.Atoi(c.Query("status"))
			deptID, _ := strconv.ParseInt(c.Query("dept"), 10, 64)
			role, _ := strconv.Atoi(c.Query("role"))                       // 1 用户 / 2 管理员 / 3 客服；0 或不传=全部
			inviteCode := strings.TrimSpace(c.Query("inviteCode"))         // 按用户填写的邀请码精确搜索（空=不传）
			inviterID, _ := strconv.ParseInt(c.Query("inviterId"), 10, 64) // 查某人邀请了哪些人（>0 生效）
			page, _ := strconv.Atoi(c.Query("page"))
			size, _ := strconv.Atoi(c.Query("size"))
			res, err := service.AdminUserList(c.Request.Context(), kw, status, deptID, role, inviteCode, inviterID, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": res})
		})
		admin.POST("/users", func(c *gin.Context) {
			var body struct {
				Account  string `json:"account"`  // 用户名（三选一）
				Phone    string `json:"phone"`    // 手机号（三选一）
				Email    string `json:"email"`    // 邮箱（三选一）
				Password string `json:"password"` // 空 → 123456
				Nickname string `json:"nickname"`
				Avatar   string `json:"avatar"`  // 空 → 后台默认头像
				ShortID  string `json:"shortId"` // 空 → 自动分配随机靓号
				DeptID   int64  `json:"departmentId"`
				Role     int    `json:"role"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			u, err := service.AdminUserCreate(c.Request.Context(), service.AdminUserCreateReq{
				Account:  body.Account,
				Phone:    body.Phone,
				Email:    body.Email,
				Password: body.Password,
				Nickname: body.Nickname,
				Avatar:   body.Avatar,
				ShortID:  body.ShortID,
				DeptID:   body.DeptID,
				Role:     body.Role,
			})
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "user.create", u.Account, c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": u})
		})
		// 批量生成账号（2026-09-25 需求）：按前端一键注册（游客）规则建号，
		// 密码固定 123456；返回生成的账号/密码/昵称/靓号清单供展示与导出。
		admin.POST("/users/batch", func(c *gin.Context) {
			var body struct {
				Count int `json:"count" binding:"required"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			list, err := service.AdminUserBatchCreate(c.Request.Context(), cfg, body.Count)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "user.batchCreate", strconv.Itoa(len(list)), c.ClientIP(), gin.H{"count": body.Count})
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"count": len(list), "list": list}})
		})
		admin.PUT("/users/:id/status", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			var body struct {
				Status int `json:"status"` // 1 正常 2 禁用
			}
			c.ShouldBindJSON(&body)
			service.AdminUserSetStatus(c.Request.Context(), id, body.Status)
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "user.status", strconv.FormatInt(id, 10), c.ClientIP(), body)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		admin.PUT("/users/:id/password", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			var body struct {
				Password string `json:"password" binding:"required"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			service.AdminUserResetPassword(c.Request.Context(), id, body.Password)
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "user.password", strconv.FormatInt(id, 10), c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// 用户详情（查看详情弹窗）：资料 + 钱包汇总 + 注册/登录审计 + 统计
		admin.GET("/users/:id/detail", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || id <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			data, err := service.AdminUserDetail(c.Request.Context(), id)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
		})

		// 用户设备列表（含最近登录 IP 与信任状态）—— 账户安全验证保底展示
		admin.GET("/users/:id/devices", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || id <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			devs, err := service.AdminUserDevices(c.Request.Context(), id)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"devices": devs}})
		})
		// 清除用户全部设备的信任标记（强制下次重新登录验证，不会锁死账号）
		admin.POST("/users/:id/devices/clear-trust", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || id <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			n, err := service.AdminClearTrust(c.Request.Context(), id)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "user.clearTrust", strconv.FormatInt(id, 10), c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"cleared": n}})
		})

		// 清空数据（危险操作；前端已有二次确认，这里再校验 scope 合法性）
		admin.POST("/data/clear", func(c *gin.Context) {
			var body struct {
				Scope string `json:"scope" binding:"required"` // users/chats/groups/recharge/withdraw/all
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			data, err := service.AdminDataClear(c.Request.Context(), body.Scope)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "data.clear", body.Scope, c.ClientIP(), data)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
		})

		// 系统配置
		admin.GET("/configs/:key", func(c *gin.Context) {
			key := c.Param("key")
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": service.SysConfigGet(c.Request.Context(), key, nil)})
		})
		admin.PUT("/configs/:key", func(c *gin.Context) {
			key := c.Param("key")
			var body struct {
				Value interface{} `json:"value"`
			}
			c.ShouldBindJSON(&body)
			// 不能吞错：写库失败必须返回错误，否则后台显示"已保存"但配置实际没落库
			if err := service.SysConfigSet(c.Request.Context(), key, body.Value); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "保存失败: " + err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "config.set", key, c.ClientIP(), body)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// 小程序管理（H5 容器）
		admin.GET("/app-entries", func(c *gin.Context) {
			apps, err := service.AdminAppList(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": apps})
		})
		admin.POST("/app-entries", func(c *gin.Context) {
			var body struct {
				NameZh   string `json:"nameZh"`
				NameEn   string `json:"nameEn"`
				Icon     string `json:"icon"`
				URL      string `json:"url"`
				Category string `json:"category"`
				Sort     int    `json:"sort"`
				Enabled  bool   `json:"enabled"`
			}
			c.ShouldBindJSON(&body)
			a, err := service.AdminAppCreate(c.Request.Context(), body.NameZh, body.NameEn, body.Icon, body.URL, body.Category, body.Sort, body.Enabled)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "创建失败"})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "app.create", body.NameZh, c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": a})
		})
		admin.PUT("/app-entries/:id", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			var body struct {
				NameZh   string `json:"nameZh"`
				NameEn   string `json:"nameEn"`
				Icon     string `json:"icon"`
				URL      string `json:"url"`
				Category string `json:"category"`
				Sort     int    `json:"sort"`
				Enabled  bool   `json:"enabled"`
			}
			c.ShouldBindJSON(&body)
			service.AdminAppUpdate(c.Request.Context(), id, body.NameZh, body.NameEn, body.Icon, body.URL, body.Category, body.Sort, body.Enabled)
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "app.update", strconv.FormatInt(id, 10), c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		admin.DELETE("/app-entries/:id", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			service.AdminAppDelete(c.Request.Context(), id)
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "app.delete", strconv.FormatInt(id, 10), c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// 智能小助手：配置读写 + 推送
		admin.GET("/assistant/config", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": service.GetAssistantConfig(c.Request.Context(), cfg)})
		})
		admin.POST("/assistant/config", func(c *gin.Context) {
			var ac service.AssistantConfig
			c.ShouldBindJSON(&ac)
			if err := service.SaveAssistantConfig(c.Request.Context(), ac); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "保存失败"})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "assistant.config", "save", c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		admin.GET("/assistant/conversations", func(c *gin.Context) {
			// 默认最近 50 个会话（按最近消息倒序），?limit= 可调（上限 200）
			limit := 50
			if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 200 {
				limit = v
			}
			page, err := service.AssistantConvList(c.Request.Context(), limit)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": page})
		})
		admin.GET("/assistant/messages", func(c *gin.Context) {
			uid, _ := strconv.ParseInt(c.Query("userId"), 10, 64)
			before, _ := strconv.ParseInt(c.Query("beforeMsgId"), 10, 64)
			limit := int64(50)
			if v, err := strconv.ParseInt(c.Query("limit"), 10, 64); err == nil && v > 0 && v <= 100 {
				limit = v
			}
			list, err := service.AssistantMessages(c.Request.Context(), uid, before, limit)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": list})
		})
		admin.POST("/assistant/push", func(c *gin.Context) {
			var body struct {
				UserID  int64    `json:"userId,string"` // 兼容单选
				UserIDs []string `json:"userIds"`       // 多选/全选（字符串，防精度丢失）
				Content string   `json:"content"`
				FileURL string   `json:"fileUrl"`
			}
			c.ShouldBindJSON(&body)
			ids := make([]int64, 0, len(body.UserIDs))
			for _, s := range body.UserIDs {
				if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
					ids = append(ids, v)
				}
			}
			if len(ids) == 0 && body.UserID > 0 {
				ids = []int64{body.UserID}
			}
			if len(ids) == 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "请选择目标用户"})
				return
			}
			for _, uid := range ids {
				if err := service.AssistantPush(c.Request.Context(), cfg, uid, body.Content, body.FileURL); err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "assistant.push", strconv.FormatInt(int64(len(ids)), 10), c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// ===== 钱包管理：手工调整余额 + 流水查询 =====
		admin.POST("/wallet/adjust", func(c *gin.Context) {
			var body struct {
				UserID string  `json:"userId"` // 目标用户 ID（字符串防精度丢失）
				Delta  float64 `json:"delta"`  // 正=加款 负=扣款
				Reason string  `json:"reason"` // 调整原因
			}
			c.ShouldBindJSON(&body)
			uid, _ := strconv.ParseInt(body.UserID, 10, 64)
			if uid <= 0 || body.Delta == 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			balance, err := service.AdminWalletAdjust(c.Request.Context(), uid, body.Delta, body.Reason, middleware.CurrentUserID(c))
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "wallet.adjust", body.UserID, c.ClientIP(), gin.H{"delta": body.Delta, "reason": body.Reason})
			// B-24：加/扣款后立刻推给在线客户端，否则用户必须杀进程重进才能看到新余额
			service.PublishWalletUpdate(c.Request.Context(), uid)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"balance": balance}})
		})

		// ===== 用户管理页的「充值」能力（B-24）=====
		// 以前后台充值调的是 /admin/users/:id/recharge，但后端根本没注册这个路由，
		// 前端 catch 后静默 fallback 成「本地记账（写用户 meta）」，
		// 结果后台显示充值成功、user.balance 却没变 → App 端永远看到老余额。
		// 这里把路由补齐，充值一律走服务端原子入账 + 实时推送。
		admin.GET("/users/:id/wallet", func(c *gin.Context) {
			uid, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			if uid <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			data, err := service.AdminUserWallet(c.Request.Context(), uid)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
		})
		admin.POST("/users/:id/recharge", func(c *gin.Context) {
			var body struct {
				Amount float64 `json:"amount"` // 充值/扣款金额：正=充值 负=扣款（0=参数错；扣款需余额充足，否则返回 4101）
				Remark string  `json:"remark"` // 备注
			}
			c.ShouldBindJSON(&body)
			uid, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			if uid <= 0 || body.Amount == 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			data, err := service.AdminUserRecharge(c.Request.Context(), uid, body.Amount, body.Remark, middleware.CurrentUserID(c))
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			act := "wallet.recharge"
			if body.Amount < 0 {
				act = "wallet.deduct"
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), act, c.Param("id"), c.ClientIP(),
				gin.H{"amount": body.Amount, "remark": body.Remark})
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
		})

		// 财务记录（B-24）：以前前端调 /admin/finances 是 404，页面退化成随机 mock 假数据，
		// 于是「财务记录对不上」。这里给出真实数据源，只读 wallet_transaction。
		admin.GET("/finances", func(c *gin.Context) {
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			size, _ := strconv.Atoi(c.DefaultQuery("size", "15"))
			from, _ := strconv.ParseInt(c.DefaultQuery("from", "0"), 10, 64)
			to, _ := strconv.ParseInt(c.DefaultQuery("to", "0"), 10, 64)
			list, total, err := service.AdminFinanceList(c.Request.Context(),
				c.Query("kw"), c.Query("side"), c.Query("type"), from, to, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"total": total, "list": list}})
		})

		admin.GET("/wallet/transactions", func(c *gin.Context) {
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
			list, total, err := service.AdminWalletTxList(c.Request.Context(), c.Query("type"), page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"total": total, "list": list}})
		})

		// ===== 钱包对账（B-22）：后台金额 / 用户端余额 / 财务流水三方是否一致 =====
		admin.GET("/wallet/reconcile", func(c *gin.Context) {
			data, err := service.WalletReconcile(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "对账失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
		})
		// 手工触发一次到期退回（正常情况后台任务每分钟自动跑，这里给运维一个兜底开关）
		admin.POST("/wallet/refund-expired", func(c *gin.Context) {
			n, err := service.RefundExpiredPackets(c.Request.Context(), 500)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "执行失败"})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "wallet.refundExpired", "-", c.ClientIP(), gin.H{"count": n})
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"count": n}})
		})

		// ===== 支付配置 / 充值订单 / 提现订单 =====
		// 支付配置：读/写（sys_config pay_config），后端做范围校验
		admin.GET("/pay-config", func(c *gin.Context) {
			cfg := service.PayConfigGet(c.Request.Context())
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": cfg})
		})
		admin.PUT("/pay-config", func(c *gin.Context) {
			var body service.PayConfig
			b, _ := io.ReadAll(c.Request.Body)
			if err := json.Unmarshal(b, &body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.PayConfigSet(c.Request.Context(), body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "pay.config.update", "-", c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// 充值订单
		admin.GET("/recharge-orders", func(c *gin.Context) {
			kw := c.Query("kw")
			status, _ := strconv.Atoi(c.Query("status"))
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
			list, total, err := service.AdminRechargeOrderList(c.Request.Context(), kw, status, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": list, "total": total}})
		})
		admin.PUT("/recharge-orders/:id/approve", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			if id <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			op := middleware.CurrentUserID(c)
			data, err := service.AdminRechargeOrderApprove(c.Request.Context(), id, op)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), op, "rechargeOrder.approve", c.Param("id"), c.ClientIP(), data)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
		})
		admin.PUT("/recharge-orders/:id/reject", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			if id <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			var body struct {
				Reason string `json:"reason"`
			}
			b, _ := io.ReadAll(c.Request.Body)
			_ = json.Unmarshal(b, &body)
			op := middleware.CurrentUserID(c)
			if err := service.AdminRechargeOrderReject(c.Request.Context(), id, op, body.Reason); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), op, "rechargeOrder.reject", c.Param("id"), c.ClientIP(), gin.H{"reason": body.Reason})
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// 提现订单
		admin.GET("/withdraw-orders", func(c *gin.Context) {
			kw := c.Query("kw")
			status, _ := strconv.Atoi(c.Query("status"))
			wType, _ := strconv.Atoi(c.Query("type"))
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
			list, total, err := service.AdminWithdrawOrderList(c.Request.Context(), kw, status, wType, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": list, "total": total}})
		})
		admin.PUT("/withdraw-orders/:id/approve", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			if id <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			op := middleware.CurrentUserID(c)
			data, err := service.AdminWithdrawOrderApprove(c.Request.Context(), id, op)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), op, "withdrawOrder.approve", c.Param("id"), c.ClientIP(), data)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
		})
		admin.PUT("/withdraw-orders/:id/reject", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			if id <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			var body struct {
				Reason string `json:"reason"`
			}
			b, _ := io.ReadAll(c.Request.Body)
			_ = json.Unmarshal(b, &body)
			op := middleware.CurrentUserID(c)
			if err := service.AdminWithdrawOrderReject(c.Request.Context(), id, op, body.Reason); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), op, "withdrawOrder.reject", c.Param("id"), c.ClientIP(), gin.H{"reason": body.Reason})
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// ===== 朋友圈管理：列表 / 以小助手身份发布 / 屏蔽 / 删除 =====
		admin.GET("/moments", func(c *gin.Context) {
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
			data, err := service.AdminMomentList(c.Request.Context(), page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
		})
		admin.POST("/moments", func(c *gin.Context) {
			var body struct {
				Content string   `json:"content"`
				Images  []string `json:"images"`
			}
			c.ShouldBindJSON(&body)
			if body.Content == "" && len(body.Images) == 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "内容不能为空"})
				return
			}
			post, err := service.AdminMomentCreate(c.Request.Context(), body.Content, body.Images, middleware.CurrentUserID(c))
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": post})
		})
		admin.PUT("/moments/:id/hidden", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			var body struct {
				Hidden bool `json:"hidden"`
			}
			c.ShouldBindJSON(&body)
			if err := service.AdminMomentHidden(c.Request.Context(), id, body.Hidden, middleware.CurrentUserID(c)); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "操作失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		admin.DELETE("/moments/:id", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			if err := service.AdminMomentDelete(c.Request.Context(), id, middleware.CurrentUserID(c)); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "删除失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		admin.DELETE("/moments/comments/:cid", func(c *gin.Context) {
			cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
			if cid <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.AdminMomentCommentDelete(c.Request.Context(), cid, middleware.CurrentUserID(c)); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "删除失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// 群组管理
		admin.GET("/groups", func(c *gin.Context) {
			kw := c.Query("kw")
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
			// 注意：返回结构已从「裸数组」改为 {list,total}（破坏性变更，前端需同步）
			res, err := service.AdminGroupList(c.Request.Context(), kw, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": res})
		})
		// 编辑群资料：只更新显式传入的字段（指针为 nil 表示不改）
		admin.PUT("/groups/:id", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || id <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			var body struct {
				Name         *string `json:"name"`
				Avatar       *string `json:"avatar"`
				Announcement *string `json:"announcement"`
				MaxMembers   *int    `json:"maxMembers"` // 0=不限
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.AdminGroupUpdate(c.Request.Context(), id, body.Name, body.Avatar, body.Announcement, body.MaxMembers); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "group.update", strconv.FormatInt(id, 10), c.ClientIP(), body)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		// 转移群主：事务内 原群主降为普通成员 / 新群主提为 owner / 改 owner_id
		admin.PUT("/groups/:id/owner", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || id <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			var body struct {
				NewOwnerID int64 `json:"newOwnerId"`
			}
			if err := c.ShouldBindJSON(&body); err != nil || body.NewOwnerID <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.AdminGroupTransferOwner(c.Request.Context(), id, body.NewOwnerID); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "group.transferOwner", strconv.FormatInt(id, 10), c.ClientIP(), body)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		admin.DELETE("/groups/:id", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			service.AdminGroupDisband(c.Request.Context(), id)
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "group.disband", strconv.FormatInt(id, 10), c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// 会话/频道通用列表（后台「默认关注频道」下拉用 type=3&size=200）
		admin.GET("/conversations", func(c *gin.Context) {
			typ, _ := strconv.Atoi(c.Query("type"))
			kw := c.Query("kw")
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
			res, err := service.AdminConversationList(c.Request.Context(), typ, kw, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": res})
		})

		// 消息记录（审计）
		admin.GET("/messages", func(c *gin.Context) {
			var q service.AdminMsgQuery
			q.ConvID, _ = strconv.ParseInt(c.Query("convId"), 10, 64)
			q.UserID, _ = strconv.ParseInt(c.Query("userId"), 10, 64)
			q.UserKw = c.Query("userKw") // 发送者关键字：用户 ID / 昵称 / 账号 / 手机号
			q.Kw = c.Query("kw")
			q.From, _ = strconv.ParseInt(c.Query("from"), 10, 64)
			q.To, _ = strconv.ParseInt(c.Query("to"), 10, 64)
			q.Type, _ = strconv.Atoi(c.Query("type"))
			page, _ := strconv.Atoi(c.Query("page"))
			size, _ := strconv.Atoi(c.Query("size"))
			msgs, total, err := service.AdminMessageQuery(c.Request.Context(), &q, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": msgs, "total": total}})
		})

		// 屏蔽/恢复消息（后台审计：屏蔽后用户端历史/同步不再下发）
		admin.POST("/messages/:msgId/block", func(c *gin.Context) {
			msgID, err := strconv.ParseInt(c.Param("msgId"), 10, 64)
			if err != nil || msgID <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误"})
				return
			}
			var body struct {
				Blocked *bool `json:"blocked"`
			}
			_ = c.ShouldBindJSON(&body)
			blocked := body.Blocked == nil || *body.Blocked // 默认 true=屏蔽
			if err := service.AdminMessageBlock(c.Request.Context(), msgID, blocked); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "操作失败"})
				return
			}
			action := "message.block"
			if !blocked {
				action = "message.unblock"
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), action, c.Param("msgId"), c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// 数据统计
		admin.GET("/stats/overview", func(c *gin.Context) {
			st, err := service.AdminStatsOverview(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "统计失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": st})
		})
		admin.GET("/stats/messages", func(c *gin.Context) {
			days, _ := strconv.Atoi(c.Query("days"))
			st, err := service.AdminStatsMessages(c.Request.Context(), days)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "统计失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": st})
		})

		// 投诉处理
		admin.GET("/reports", func(c *gin.Context) {
			status, _ := strconv.Atoi(c.Query("status")) // 0/不传=全部 1=待处理 2=已处理
			page, _ := strconv.Atoi(c.Query("page"))
			size, _ := strconv.Atoi(c.Query("size"))
			list, total, err := service.AdminReportList(c.Request.Context(), status, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": list, "total": total}})
		})
		admin.POST("/reports/:id/handle", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			if id <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.AdminReportHandle(c.Request.Context(), id, middleware.CurrentUserID(c)); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "report.handle", strconv.FormatInt(id, 10), c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// 日志
		admin.GET("/logs", func(c *gin.Context) {
			page, _ := strconv.Atoi(c.Query("page"))
			size, _ := strconv.Atoi(c.Query("size"))
			logs, total, err := service.AdminLogList(c.Request.Context(), page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": logs, "total": total}})
		})
		admin.GET("/logs/login", func(c *gin.Context) {
			page, _ := strconv.Atoi(c.Query("page"))
			size, _ := strconv.Atoi(c.Query("size"))
			logs, total, err := service.AdminLoginLogList(c.Request.Context(), page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": logs, "total": total}})
		})

		// ===== 编辑用户资料（PUT /admin/users/:id）—— 解决后台改昵称/头像/角色/短ID不落库 =====
		admin.PUT("/users/:id", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			var body struct {
				Nickname string  `json:"nickname"`
				Avatar   string  `json:"avatar"`
				Role     int     `json:"role"`
				ShortID  *string `json:"shortId"` // null=不改；""=清空；否则赋值
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.AdminUserUpdate(c.Request.Context(), id, body.Nickname, body.Avatar, body.Role, body.ShortID); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			go service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "user.update", strconv.FormatInt(id, 10), c.ClientIP(), body)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// ===== 群组成员 / 群消息详情 =====
		admin.GET("/groups/:id/members", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
			list, total, err := service.AdminGroupMembers(c.Request.Context(), id, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": list, "total": total}})
		})
		admin.GET("/groups/:id/messages", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
			kw := c.Query("kw")
			msgs, total, err := service.AdminGroupMessages(c.Request.Context(), id, kw, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": msgs, "total": total}})
		})

		// ===== 保留靓号 reserved_short_id =====
		admin.GET("/reserved-short-ids", func(c *gin.Context) {
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
			status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))
			source, _ := strconv.Atoi(c.DefaultQuery("source", "0"))
			kw := c.Query("kw")
			res, err := service.AdminReservedShortIDList(c.Request.Context(), kw, status, source, page, size)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": res})
		})
		admin.POST("/reserved-short-ids/batch", func(c *gin.Context) {
			var body struct {
				// 两种 payload 都兼容：
				//   A) 新版标准：mode in {range|list|rule} + 对应字段
				//   B) 前端旧版：ids []string + type + remark（统一视为「列表录入」）
				Mode   string   `json:"mode"` // range | list | rule
				From   int64    `json:"from"`
				To     int64    `json:"to"`
				List   []string `json:"list"`
				Prefix string   `json:"prefix"`
				Digits int      `json:"digits"`
				Count  int      `json:"count"`
				Remark string   `json:"remark"`
				Price  float64  `json:"price"`
				Source int      `json:"source"`
				Type   int      `json:"type"` // 1 普通 / 2 豹子号 / 3 顺子号 / 4 VIP

				// 兼容字段：ids 与 list 等价
				Ids []string `json:"ids"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
				return
			}
			// 兼容：送了 ids 但没送 list → 合并
			if len(body.List) == 0 && len(body.Ids) > 0 {
				body.List = body.Ids
			}
			// 兼容：旧前端没送 mode、但有 list → 补成 list 模式
			if body.Mode == "" && len(body.List) > 0 {
				body.Mode = "list"
			}
			source := body.Source
			if source <= 0 {
				switch body.Mode {
				case "range":
					source = model.ReservedSourceRange
				case "list", "manual":
					source = model.ReservedSourceManual
				case "rule":
					source = model.ReservedSourceRule
				default:
					source = model.ReservedSourceManual
				}
			}
			typeID := body.Type
			if typeID < 1 || typeID > 4 {
				typeID = 1
			}
			cnt, err := service.AdminReservedShortIDBatch(c.Request.Context(),
				body.From, body.To, body.List, body.Prefix, body.Digits, body.Count,
				body.Remark, body.Price, typeID, source)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"added": cnt, "count": cnt}})
		})
		admin.PUT("/reserved-short-ids/:id/remark", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			var body struct {
				Remark string  `json:"remark"`
				Price  float64 `json:"price"`
				Type   int     `json:"type"` // 1 普通 / 2 豹子号 / 3 顺子号 / 4 VIP
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.AdminReservedShortIDRemark(c.Request.Context(), id, body.Remark, body.Price, body.Type); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		admin.PUT("/reserved-short-ids/:id/frozen", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			var body struct {
				Frozen bool `json:"frozen"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.AdminReservedShortIDFreeze(c.Request.Context(), id, body.Frozen); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		admin.DELETE("/reserved-short-ids/:id", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.AdminReservedShortIDDelete(c.Request.Context(), id); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		admin.PUT("/reserved-short-ids/:id/assign", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			// 兼容 number / string / "12345" 三种 userId 传法（前端 bigint 常以字符串发送）
			raw, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			var payload map[string]any
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &payload); err != nil {
					c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误：JSON 解析失败"})
					return
				}
			}
			var userID int64
			switch v := payload["userId"].(type) {
			case float64:
				userID = int64(v)
			case string:
				if n, pe := strconv.ParseInt(v, 10, 64); pe == nil {
					userID = n
				}
			}
			if userID <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误：缺少 userId"})
				return
			}
			info, err := service.AdminReservedShortIDAssign(c.Request.Context(), id, userID)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": info})
		})
		admin.PUT("/reserved-short-ids/:id/relieve", func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.AdminReservedShortIDRelieve(c.Request.Context(), id); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// ===== 自定义邀请码（一码关联多好友，注册自动加好友） =====
		admin.GET("/invite-friend-codes", func(c *gin.Context) {
			list, err := service.InviteFriendList(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": list})
		})
		admin.POST("/invite-friend-codes", func(c *gin.Context) {
			var body struct {
				Code      string   `json:"code" binding:"required"`
				FriendIDs []string `json:"friendIds"` // 字符串形式的用户 ID（int64 精度安全）
				Remark    string   `json:"remark"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			ic, err := service.InviteFriendCreate(c.Request.Context(), body.Code, body.FriendIDs, body.Remark)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "invite_code.create", body.Code, c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": ic})
		})
		admin.PUT("/invite-friend-codes/:id", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			var body struct {
				Code      *string  `json:"code"`
				FriendIDs []string `json:"friendIds"`
				Remark    *string  `json:"remark"`
				Enabled   *int     `json:"enabled"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			if err := service.InviteFriendUpdate(c.Request.Context(), id, body.Code, body.FriendIDs, body.Remark, body.Enabled); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "invite_code.update", strconv.FormatInt(id, 10), c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})
		admin.DELETE("/invite-friend-codes/:id", func(c *gin.Context) {
			id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
			if err := service.InviteFriendDelete(c.Request.Context(), id); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "删除失败"})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "invite_code.delete", strconv.FormatInt(id, 10), c.ClientIP(), nil)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
		})

		// ===== 会话 seq 水位自愈（运维兜底，修 R-04）=====
		// 场景：Redis 丢数据/实例重建后 conv:seq:{id} 从小值重来，客户端 lastSeq
		// 却还停在大值 → 该会话**永久收不到新消息**。这里给运维一个手工修复入口。
		// convId 省略（或 0）时扫描全部会话（上限 20000）。
		admin.POST("/seq/repair", func(c *gin.Context) {
			convID, _ := strconv.ParseInt(c.Query("convId"), 10, 64)
			fixed, scanned, err := service.RepairSeqAdmin(c.Request.Context(), convID)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "修复失败: " + err.Error()})
				return
			}
			service.AdminLog(c.Request.Context(), middleware.CurrentUserID(c), "seq.repair",
				strconv.FormatInt(convID, 10), c.ClientIP(), gin.H{"scanned": scanned, "fixed": len(fixed)})
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
				"scanned": scanned,
				"fixed":   fixed,
			}})
		})

		// ===== 系统健康检测 =====
		admin.GET("/health/:key", func(c *gin.Context) {
			key := c.Param("key")
			res, err := service.AdminHealthCheck(c.Request.Context(), cfg, key)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": res})
		})

		// ===== 服务重启（系统检测页按钮） =====
		// 前提：服务器用 systemd 托管（deploy/bt_deploy.sh 生成的 im-api / im-gateway 单元，
		// Restart=always），systemctl restart 由守护进程拉起新进程。
		// 本地 Windows 开发环境没有 systemctl，直接返回提示。
		admin.POST("/system/restart", func(c *gin.Context) {
			var body struct {
				Target string `json:"target"` // api | gateway
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
				return
			}
			var unit string
			switch strings.ToLower(strings.TrimSpace(body.Target)) {
			case "api":
				unit = "im-api"
			case "gateway", "wss":
				unit = "im-gateway"
			default:
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误：target 须为 api 或 gateway"})
				return
			}
			if runtime.GOOS == "windows" {
				c.JSON(http.StatusOK, gin.H{"code": 400, "message": "当前是 Windows 开发环境（非 systemd 托管），请在服务管理器/启动脚本中手动重启"})
				return
			}
			// 先返回响应再重启：restart 会杀掉本进程（target=api 时），
			// 延迟 800ms 确保响应已经写回客户端。
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "重启指令已下发，服务将在数秒内恢复（systemd Restart=always 自动拉起）"})
			go func() {
				time.Sleep(800 * time.Millisecond)
				if out, err := exec.Command("systemctl", "restart", unit).CombinedOutput(); err != nil {
					jsonLog := "restart " + unit + " failed: " + err.Error() + " " + string(out)
					println(jsonLog)
				}
			}()
		})

		// 文件上传（管理员）：接收 file 字段 + dir；与用户 /api/v1/upload 逻辑一致
		// 网页小程序白名单（功能 B-7）：管理员 CRUD（Auth + RequireAdmin 已在 admin 组生效）
		admin.GET("/web-whitelist", AdminWebWhitelistListHandler())
		admin.POST("/web-whitelist", AdminWebWhitelistCreateHandler())
		admin.PUT("/web-whitelist/:id", AdminWebWhitelistUpdateHandler())
		admin.DELETE("/web-whitelist/:id", AdminWebWhitelistDeleteHandler())

		admin.POST("/upload", func(c *gin.Context) {
			file, header, err := c.Request.FormFile("file")
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "缺少文件"})
				return
			}
			defer file.Close()
			dir := c.PostForm("dir")
			if dir == "" {
				dir = "common/"
			}
			if !strings.HasSuffix(dir, "/") {
				dir += "/"
			}
			url, name, size, err := service.UploadFile(c.Request.Context(), cfg, dir, file, header.Filename, header.Header.Get("Content-Type"))
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "上传失败: " + err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"code":    0,
				"message": "ok",
				"data": gin.H{
					"url":      url,
					"name":     header.Filename,
					"object":   name,
					"size":     size,
					"mimeType": header.Header.Get("Content-Type"),
				},
			})
		})

		// 文件下载（管理员）：审计场景下要能取任意会话的附件
		admin.GET("/files/:fileId/download", AdminFileDownloadHandler(cfg))
	}
}

// AdminFileDownloadHandler GET /api/v1/admin/files/:fileId/download （需管理员）
// 与 FileDownloadHandler 的唯一区别：不做 service.IsConvMember 校验 ——
// 管理员本来就不可能是所有群的成员，普通下载接口会在这一步返回 403。
//
// 签名 ttl 取 5 分钟（普通用户端是 1 小时）：管理员链接一旦泄露，影响面是全库文件，
// 缩短有效期把暴露窗口压到最小，5 分钟足够前端完成一次跳转下载。
//
// 错误：400 参数错误 / 404 文件不存在（含已软删除）。
func AdminFileDownloadHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		fileID := c.Param("fileId")
		if fileID == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误"})
			return
		}
		cf, err := repo.GetConvFileByFileID(c.Request.Context(), fileID)
		if err != nil || cf.Deleted {
			c.JSON(http.StatusOK, gin.H{"code": 404, "message": "文件不存在"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "ok",
			"data": gin.H{
				"url":  signFileRawURL(cfg, cf.FileID, "", 5*time.Minute),
				"name": cf.Name,
				"size": cf.Size,
				"mime": cf.Mime,
			},
		})
	}
}
