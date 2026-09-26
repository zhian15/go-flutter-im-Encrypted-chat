package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/service"
	"github.com/yourcompany/im-server/internal/store"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册 REST 路由
func RegisterRoutes(r *gin.Engine, cfg *config.Config) {
	r.Use(middleware.CORS())

	RegisterAdminRoutes(r, cfg)

	api := r.Group("/api/v1")
	{
		api.GET("/health", Health)
		api.GET("/access/nodes", func(c *gin.Context) { Nodes(c, cfg) })
		api.GET("/trtc/config", TRTCConfigHandler(cfg))
		// 文件原文代理（公开）：不挂鉴权，靠 HMAC 签名（fileId+exp）自证，
		// 供预览直出与下载共用——MinIO 内网地址在移动端不可达。
		api.GET("/files/:fileId/raw", FileRawHandler(cfg))

		// 认证（公开）
		api.GET("/auth/captcha", CaptchaHandler)
		api.POST("/auth/send-code", SendCodeHandler(cfg))
		api.POST("/auth/register", RegisterHandler(cfg))
		api.POST("/auth/login", middleware.LoginIPBan(), LoginHandler(cfg))
		// 登录设备批准（账户安全验证）
		api.POST("/auth/verify/code", VerifyCodeHandler(cfg))
		api.POST("/auth/verify/approve", middleware.Auth(cfg), VerifyApproveHandler(cfg))
		api.GET("/auth/verify/poll", VerifyPollHandler())
		api.POST("/auth/verify/switch", VerifySwitchHandler(cfg))
		api.POST("/auth/guest", GuestRegisterHandler(cfg))
		api.POST("/auth/refresh", RefreshHandler(cfg))
		api.GET("/auth/config", func(c *gin.Context) { AuthConfig(c, cfg) })
		api.GET("/auth/check-account", CheckAccountHandler())

		// 扫码登录：创建 ticket（公开）+ 手机确认（需登录）+ PC 轮询（公开）
		api.POST("/auth/qr/ticket", func(c *gin.Context) {
			// PC 端真实设备信息随 ticket 存 Redis：确认登录后据此落 device 表
			//（此前 PC 扫码登录的设备行写死 "qr-scan"/iOS，设备页显示错乱）
			var body struct {
				DeviceID   string `json:"device_id"`
				DeviceType int    `json:"device_type"`
				DeviceName string `json:"device_name"`
			}
			_ = c.ShouldBindJSON(&body) // 老客户端可能不带 body，不强制
			info, err := service.QrCreateTicket(c.Request.Context(), cfg,
				body.DeviceID, body.DeviceType, body.DeviceName)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "生成二维码失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": info})
		})
		api.GET("/auth/qr/status", func(c *gin.Context) {
			info, err := service.QrPollStatus(c.Request.Context(), cfg, c.Query("ticket"))
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": info})
		})

		// 用户（需登录）
		user := api.Group("", middleware.Auth(cfg))
		{
			user.POST("/auth/logout", LogoutHandler(cfg))
			// 登录后补填邀请码（游客/普通用户通用）：复用现有邀请码逻辑自动加好友
			user.POST("/invite/bind", InviteBindHandler())
			// AI 翻译：文本翻译（后台可配接口/模型/每日额度）+ 当日用量查询
			user.POST("/translate", TranslateHandler())
			user.GET("/translate/usage", TranslateUsageHandler())
			// 文件上传（MinIO）：multipart form，字段 file + 可选 dir(chat/avatar)
			user.POST("/upload", func(c *gin.Context) {
				file, header, err := c.Request.FormFile("file")
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "缺少文件"})
					return
				}
				defer file.Close()
				dir := c.PostForm("dir")
				if dir == "" {
					dir = "chat/"
				}
				if !strings.HasSuffix(dir, "/") {
					dir += "/"
				}
				url, name, size, err := service.UploadFile(c.Request.Context(), cfg, dir, file, header.Filename, header.Header.Get("Content-Type"))
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": 500, "message": "上传失败: " + err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
					"url": url, "name": header.Filename, "object": name, "size": size,
					"mimeType": header.Header.Get("Content-Type"),
				}})
			})
			// 手机端扫码成功：pending → scanned（PC 端轮询显示"已扫码，请在手机上确认"）
			user.POST("/auth/qr/scanned", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				var body struct {
					Ticket string `json:"ticket"`
				}
				c.ShouldBindJSON(&body)
				if err := service.QrMarkScanned(c.Request.Context(), cfg, body.Ticket, uid); err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
			})
			user.POST("/auth/qr/confirm", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				var body struct {
					Ticket string `json:"ticket"`
				}
				c.ShouldBindJSON(&body)
				if err := service.QrConfirm(c.Request.Context(), cfg, body.Ticket, uid); err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
			})
			user.GET("/user/profile", GetProfileHandler(cfg))
			user.PUT("/user/profile", UpdateProfileHandler())
			user.GET("/user/privacy", GetPrivacyHandler())
			user.PUT("/user/privacy", UpdatePrivacyHandler())
			user.POST("/user/paypwd/set", PayPwdSetHandler())
			user.PUT("/user/password", ChangePasswordHandler())
			// ===== E2EE 端到端加密（2026-09-18 §36 定稿）=====
			user.GET("/e2ee/pubkey/:uid", E2eePubKeyHandler())
			user.GET("/e2ee/me", E2eeMeHandler())
			user.PUT("/e2ee/keys", E2eePutKeysHandler())
			user.PUT("/e2ee/backup", E2eePutBackupHandler())
			user.PUT("/e2ee/toggle", E2eeToggleHandler())
			// 跨设备恢复（申请从旧设备恢复，2026-09-18）
			user.POST("/e2ee/recover/request", E2eeRecoverRequestHandler())
			user.GET("/e2ee/recover/status", E2eeRecoverStatusHandler())
			user.PUT("/e2ee/recover/approve", E2eeRecoverApproveHandler())
			user.DELETE("/user", DeleteAccountHandler())
			user.POST("/user/bind-phone/send-code", BindPhoneSendCodeHandler(cfg))
			user.POST("/user/bind-phone", BindPhoneHandler())

			// ===== 设备会话（活跃会话列表 / 注销单台设备）=====
			user.GET("/user/devices", DeviceListHandler())
			user.DELETE("/user/devices/:deviceId", DeviceLogoutHandler())
			// 推送 token 上报（个推 client id 绑定，见 service/getui.go）
			user.POST("/user/push-token", PushTokenHandler())

			// ===== 钱包（零钱）=====
			user.GET("/wallet/me", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				data, err := service.WalletMe(c.Request.Context(), uid)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
			})
			// 【已废弃 B-21】/wallet/record：金额完全由客户端上报，任何人都能给自己加钱，
			// 是个"自助充值"漏洞。现改为：
			//   - 入账类（red_in / tr_in）一律拒绝 → 走 /wallet/transfer/:msgId/accept 或 /wallet/redpacket/:msgId/claim，
			//     金额由服务端按消息内容核算；
			//   - 出账类（red_out / tr_out）改幂等 no-op：新版服务端发消息时已原子扣款，
			//     老客户端再调一次也不能造成**重复扣款**（同 ref_id 已有流水就直接返回当前余额）。
			user.POST("/wallet/record", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				var body struct {
					Type   string  `json:"type"`
					Amount float64 `json:"amount"`
					RefID  string  `json:"refId"`
				}
				c.ShouldBindJSON(&body)
				if body.Type == "red_in" || body.Type == "tr_in" {
					c.JSON(http.StatusOK, gin.H{
						"code":    1001,
						"message": "该记账接口已停用，请升级客户端后重新收款",
					})
					return
				}
				if body.Type != "red_out" && body.Type != "tr_out" {
					c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
					return
				}
				// 幂等：同 ref_id 已记过账就不再扣（防老客户端二次扣款）
				var dup int64
				store.DB.Model(&model.WalletTransaction{}).
					Where("user_id = ? AND type = ? AND ref_id = ?", uid, body.Type, body.RefID).
					Count(&dup)
				bal := service.CurrentBalance(uid)
				if dup > 0 {
					c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"balance": bal}})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"balance": bal}})
			})

			// 转账收款（服务端交叉校验：金额读消息内容 + 会话成员校验 + 唯一索引幂等）
			user.POST("/wallet/transfer/:msgId/accept", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				msgID, _ := strconv.ParseInt(c.Param("msgId"), 10, 64)
				data, err := service.WalletTransferAccept(c.Request.Context(), uid, msgID)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
			})

			// 转账状态（客户端气泡展示「您已领取/已被领取」）
			user.GET("/wallet/transfer/:msgId", func(c *gin.Context) {
				msgID, _ := strconv.ParseInt(c.Param("msgId"), 10, 64)
				data, err := service.WalletTransferStatus(c.Request.Context(), msgID)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
			})

			// 红包领取 + 详情
			user.POST("/wallet/redpacket/:msgId/claim", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				msgID, _ := strconv.ParseInt(c.Param("msgId"), 10, 64)
				data, err := service.WalletRedPacketClaim(c.Request.Context(), uid, msgID)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
			})
			user.GET("/wallet/redpacket/:msgId", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				msgID, _ := strconv.ParseInt(c.Param("msgId"), 10, 64)
				data, err := service.WalletRedPacketDetail(c.Request.Context(), uid, msgID)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
			})
			// 账单：时间筛选 + 分页
			user.GET("/wallet/records", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
				size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
				data, err := service.WalletRecords(c.Request.Context(), uid,
					c.Query("start"), c.Query("end"), page, size)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
			})

			// ===== 支付：充值通道配置（下发收款码/提示，不需要鉴权以外的权限）=====
			user.GET("/pay/config", func(c *gin.Context) {
				cfg := service.PayConfigGet(c.Request.Context())
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": cfg})
			})

			// ===== 充值订单（用户侧）=====
			user.POST("/wallet/recharge/submit", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				var body struct {
					Amount     float64 `json:"amount"`
					PayMethod  int     `json:"payMethod"` // 1微信 2支付宝 3银行卡
					ProofImage string  `json:"proofImage"`
					PayTxNo    string  `json:"payTxNo"`
					Remark     string  `json:"remark"`
				}
				if err := c.ShouldBindJSON(&body); err != nil {
					c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
					return
				}
				data, err := service.UserRechargeSubmit(c.Request.Context(), uid,
					body.Amount, body.PayMethod, body.ProofImage, body.PayTxNo, body.Remark)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
			})
			user.GET("/wallet/recharge/orders", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
				size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
				list, total, err := service.UserRechargeList(c.Request.Context(), uid, page, size)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": list, "total": total}})
			})

			// ===== 提现账户绑定（用户侧）=====
			user.GET("/wallet/withdraw-account", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				wa, err := service.UserWithdrawAccountGet(c.Request.Context(), uid)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": wa})
			})
			user.PUT("/wallet/withdraw-account", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				var wa model.WithdrawAccount
				// 宽松 bind：兼容前端可能传字符串数值等
				b, _ := io.ReadAll(c.Request.Body)
				if err := json.Unmarshal(b, &wa); err != nil {
					c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
					return
				}
				if err := service.UserWithdrawAccountSave(c.Request.Context(), uid, &wa); err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
			})

			// ===== 提现订单（用户侧）=====
			user.POST("/wallet/withdraw/submit", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				var body struct {
					Amount       float64 `json:"amount"`
					WithdrawType int     `json:"withdrawType"` // 1WeChat 2AliPay 3Bank
				}
				b, _ := io.ReadAll(c.Request.Body)
				if err := json.Unmarshal(b, &body); err != nil {
					c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
					return
				}
				data, err := service.UserWithdrawSubmit(c.Request.Context(), uid, body.Amount, body.WithdrawType)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
			})
			user.GET("/wallet/withdraw/orders", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
				size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
				list, total, err := service.UserWithdrawList(c.Request.Context(), uid, page, size)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": list, "total": total}})
			})

			// ===== 朋友圈 =====
			user.GET("/moments", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
				size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
				data, err := service.MomentsList(c.Request.Context(), uid, page, size)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
			})
			user.GET("/moments/:ownerId", MomentsByUserHandler(cfg))
			user.POST("/moments", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				var body struct {
					Content string   `json:"content"`
					Images  []string `json:"images"`
				}
				c.ShouldBindJSON(&body)
				if body.Content == "" && len(body.Images) == 0 {
					c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "内容不能为空"})
					return
				}
				post, err := service.MomentsPublish(c.Request.Context(), uid, body.Content, body.Images)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": post})
			})
			user.POST("/moments/:id/like", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
				liked, err := service.MomentLike(c.Request.Context(), uid, id)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"liked": liked}})
			})
			user.POST("/moments/:id/comment", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
				var body struct {
					Content string `json:"content"`
				}
				c.ShouldBindJSON(&body)
				cm, err := service.MomentCommentAdd(c.Request.Context(), uid, id, body.Content)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": cm})
			})
			user.DELETE("/moments/comment/:cid", func(c *gin.Context) {
				uid := middleware.CurrentUserID(c)
				cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
				if err := service.MomentCommentDelete(c.Request.Context(), uid, cid); err != nil {
					c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
			})
			user.GET("/user/search", SearchUsersHandler())
			user.GET("/user/:id", GetUserDetailHandler())
			// 共同群组列表（好友资料页）：我与目标用户共同加入的群
			user.GET("/user/:id/mutual-groups", MutualGroupsHandler())
			user.GET("/app/list", AppListHandler())
			user.GET("/trtc/usersig", TRTCUserSigHandler(cfg))
			user.GET("/livekit/token", LiveKitTokenHandler(cfg))

			// 好友（阶段 2）
			user.GET("/friend/list", FriendListHandler())
			user.POST("/friend/request", FriendRequestAddHandler())
			user.GET("/friend/request/incoming", FriendRequestIncomingHandler())
			user.GET("/friend/request/outgoing", FriendRequestOutgoingHandler())
			user.POST("/friend/request/:id/handle", FriendRequestHandleHandler())
			user.DELETE("/friend/:id", FriendDeleteHandler())
			user.PUT("/friend/:id/remark", FriendSetRemarkHandler())
			user.POST("/friend/blacklist", BlacklistAddHandler())
			user.DELETE("/friend/blacklist/:id", BlacklistRemoveHandler())
			user.GET("/friend/blacklist", BlacklistListHandler())

			// 会话（阶段 3）
			user.GET("/conversation/list", ConvListHandler())
			user.POST("/conversation/direct", CreateDirectHandler())
			user.POST("/conversation/group", CreateGroupHandler())
			user.GET("/conversation/:id/members", ConvMembersHandler())
			user.PUT("/conversation/:id/pin-message", SetPinMessageHandler())
			user.GET("/conversation/:id/pins", PinnedMessagesHandler())
			user.PUT("/conversation/:id/announcement", UpdateAnnouncementHandler())
			user.POST("/conversation/:id/invite", GroupInviteHandler())
			user.DELETE("/conversation/:id/members/:userId", GroupRemoveHandler())
			user.POST("/conversation/:id/quit", GroupQuitHandler())
			user.POST("/conversation/:id/disband", GroupDisbandHandler())
			user.PUT("/conversation/:id", GroupUpdateHandler())
			user.PUT("/conversation/:id/pin", SetPinHandler())
			user.PUT("/conversation/:id/mute", SetMuteHandler())
			user.PUT("/conversation/:id/archive", SetArchiveHandler())
			// 会话文件夹（个人视图分组）：顶层 /folders，避开 /conversation/:id 参数路由树
			user.GET("/folders", FolderListHandler())
			user.POST("/folders", FolderSaveHandler())
			user.PUT("/folders/:id", FolderSaveHandler())
			user.DELETE("/folders/:id", FolderDeleteHandler())
			user.DELETE("/conversation/:id", DeleteConvHandler())
			// 群聊管理（阶段：群主改头像/群名、群设置、管理员、禁言、二维码进群）
			user.GET("/conversation/:id/settings", GroupSettingsHandler())
			user.PUT("/conversation/:id/settings", SetGroupSettingsHandler())
			user.PUT("/conversation/:id/admin", SetGroupAdminHandler())
			user.PUT("/conversation/:id/mute-member", MuteMemberHandler())
			user.POST("/conversation/:id/join", GroupJoinHandler())
			// 删除双方聊天记录（单聊软删位点，消息本体保留；服务端 History/ConvList 按位点过滤）
			user.POST("/conversation/:id/clear-history", ClearHistoryHandler())
			user.GET("/conversation/:id/preview", GroupPreviewHandler())
			// 投诉（会话设置「投诉」入口）：落库即成功，管理后台处理
			user.POST("/conversation/report", ReportCreateHandler())

			// 频道（type=3 会话：类公众号，只有频道主能发言；关注=成员行，会话列表自动收录）
			user.POST("/channel", CreateChannelHandler())
			user.GET("/channel/:id", ChannelInfoHandler())
			user.POST("/channel/:id/follow", ChannelFollowHandler())
			user.POST("/channel/:id/unfollow", ChannelUnfollowHandler())

			// 网页小程序卡片：链接元数据预览（需登录 + 限流中间件）
			user.GET("/link/meta", middleware.LinkMetaRateLimit(), LinkMetaHandler())
			// 群文件云盘（功能 A）：列表 + 预览
			user.GET("/conversation/:id/files", GroupFileListHandler())
			user.GET("/files/:fileId/preview", FilePreviewHandler(cfg))
			// 视频首帧封面（ffmpeg 抽帧，大视频也能有真实首帧，像微信）
			user.GET("/files/:fileId/thumb", FileThumbHandler(cfg))
			// 文件下载：返回带签名的 raw 代理地址（需登录 + 会话成员校验）
			user.GET("/files/:fileId/download", FileDownloadHandler(cfg))

			// 消息（阶段 3）
			// 发送限流（R-22）：per-user 令牌桶 + per-conversation，挂在这条路由上
			user.POST("/message/send", middleware.SendRateLimit(), SendMessageHandler())
			user.GET("/message/history", HistoryHandler())
			// 消息类型筛选（相册/文件/链接/语音页）：type=image|video|voice|file|link，seq 倒序游标分页
			user.GET("/message/filter", FilterMessagesHandler())
			// 消息类型计数（资料页媒体角标）：同 filter 的 type 取值，出参 {"count":N}
			user.GET("/message/media-count", FilterMessageCountHandler())
			user.GET("/message/sync", SyncHandler())
			// 水位查询：客户端进页对齐断点用（修 R-05 的配套）
			user.GET("/message/lastSeq", LastSeqHandler())
			user.GET("/message/search", SearchMessagesHandler())
			user.POST("/message/:id/recall", RecallMessageHandler())
			user.POST("/message/read", MarkReadHandler())
			user.GET("/message/receipts", ReceiptsHandler())
			user.POST("/message/favorite", FavoriteAddHandler())
			user.GET("/message/favorites", FavoriteListHandler())
		}
	}
}

// MomentsByUserHandler 查看指定用户的朋友圈（好友资料页"朋友圈"入口）
func MomentsByUserHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		viewer := middleware.CurrentUserID(c)
		owner, err := strconv.ParseInt(c.Param("ownerId"), 10, 64)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
		data, err := service.MomentsListByUser(c.Request.Context(), viewer, owner, page, size)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
	}
}

func Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"status": "up"}})
}

// Nodes 就近接入节点列表（客户端测速选路）
func Nodes(c *gin.Context, cfg *config.Config) {
	// 节点列表：后台 sys_config access_nodes 优先，回退环境变量（需求：后台可增删节点）
	if v := service.SysConfigGet(c.Request.Context(), "access_nodes", cfg.AccessNodes); v != nil {
		if s, ok := v.(string); ok && s != "" && s != "null" {
			var nodes []map[string]interface{}
			if json.Unmarshal([]byte(s), &nodes) == nil && len(nodes) > 0 {
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": nodes})
				return
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": cfg.AccessNodes})
}

// AuthConfig 注册配置（数据库优先，回退环境变量）——客户端据此渲染注册页
func AuthConfig(c *gin.Context, cfg *config.Config) {
	flags := service.GetAuthFlags(c.Request.Context(), cfg)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": flags})
}

// TRTCConfigHandler 返回 TRTC 配置（不含 secretKey）
func TRTCConfigHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		conf := service.GetTRTCConfig(c.Request.Context(), cfg)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": conf})
	}
}

// TRTCUserSigHandler 为调用方生成 TRTC UserSig（后端用 secretKey 签名后下发）
// 请求：GET /trtc/usersig?room=room123 （user 来自 token）
func TRTCUserSigHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		conf := service.GetTRTCConfig(c.Request.Context(), cfg)
		if !conf.Enabled {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "TRTC 未配置", "data": gin.H{}})
			return
		}
		secretKey := service.SysConfigString(c.Request.Context(), "trtc_secret_key", "")
		userIDStr := fmt.Sprintf("%d", uid)
		sig, exp, err := service.GenerateUserSig(conf.AppID, secretKey, userIDStr, 7*24*3600)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"appId":   conf.AppID,
			"userId":  userIDStr,
			"userSig": sig,
			"expire":  exp,
			"roomId":  c.Query("room"),
		}})
	}
}

// LiveKitTokenHandler 为调用方签发 LiveKit 房间 JoinToken（engine=livekit 时使用）
// 请求：GET /livekit/token?room=conv123 （user 来自 token；房间号 = 会话 ID）
// 返回 data：{url, token, identity, room} —— 客户端拿 url+token 直连 LiveKit SFU。
// media control 信令（sdp/ice）在 LiveKit 模式下不需要：SFU 负责媒体转发。
func LiveKitTokenHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		room := strings.TrimSpace(c.Query("room"))
		if room == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "room 不能为空"})
			return
		}
		url, apiKey, apiSecret, ok := service.LiveKitConfigGet(c.Request.Context())
		if !ok {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "LiveKit 未配置（后台需填 livekit_url / livekit_api_key / livekit_api_secret）"})
			return
		}
		userIDStr := fmt.Sprintf("%d", uid)
		token, err := service.GenerateLiveKitToken(apiKey, apiSecret, userIDStr, room, "", 2*time.Hour)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"url":      url,
			"token":    token,
			"identity": userIDStr,
			"roomId":   room,
		}})
	}
}
