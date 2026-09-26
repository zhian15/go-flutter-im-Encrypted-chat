package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/service"

	"github.com/gin-gonic/gin"
)

// GetProfileHandler 我的资料（附带我的在线设备）
func GetProfileHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		u, err := service.GetProfile(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		// 附加在线状态（我当前在哪些设备在线）
		online, devs := service.IsUserOnline(c.Request.Context(), uid)
		// 是否已设置支付密码：前端据此在「发红包/转账」时决定弹输入窗还是「去设置」提示
		paySet, _ := service.HasPayPwd(c.Request.Context(), uid)
		uJSON, _ := json.Marshal(u)
		extra := map[string]interface{}{
			"online":       online,
			"onlineDevice": devs,
			// 靓号标识：short_id 来自后台靓号池（已分配）→ 客户端 ID 前显示红色「靓ID」徽标
			"vipShortId": service.IsVipShortID(c.Request.Context(), uid, u.ShortID),
			"payPwdSet":  paySet,
		}
		var body map[string]interface{}
		json.Unmarshal(uJSON, &body)
		for k, v := range extra {
			body[k] = v
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": body})
	}
}

// UpdateProfileHandler 更新资料
func UpdateProfileHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var req service.UpdateProfileReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.UpdateProfile(c.Request.Context(), uid, &req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// SearchUsersHandler 搜索用户（全员可见，附带在线状态）。
// 纯数字关键词时同时按会话 ID 精确匹配群/频道（用户原话：搜索群聊 id/频道 id 加入）：
// 群/频道以 {type:"group"|"channel", id(string), name, avatar, memberCount, isPublic} 卡
// 追加在用户结果之后；前端按 type 渲染并调现有加入端点（join / channel follow）。
func SearchUsersHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		kw := c.Query("kw")
		users, err := service.SearchUsers(c.Request.Context(), kw)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "搜索失败"})
			return
		}
		data := attachOnline(c, users)
		for _, item := range data {
			item["type"] = "user"
		}
		out := make([]map[string]interface{}, 0, len(data)+2)
		out = append(out, data...)
		for _, card := range service.SearchGroupsAndChannels(c.Request.Context(), kw) {
			b, _ := json.Marshal(card)
			var m map[string]interface{}
			_ = json.Unmarshal(b, &m)
			out = append(out, m)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": out})
	}
}

// GetUserDetailHandler 用户详情（附带在线状态；返回 PublicUser 脱敏白名单，
// 手机号按被查看者的 phone_visible 给掩码/全遮，不泄露明文）
func GetUserDetailHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		uid := middleware.CurrentUserID(c)
		// 内部取完整 model.User（含 phone_visible / online_visible 等设置列），
		// 对外序列化必须经 PublicUserOfForSet 脱敏，禁止直接返回 model.User。
		u, err := service.GetUserDetail(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		friends := service.FriendIDSet(c.Request.Context(), uid)
		pu := service.PublicUserOfForSet(uid, friends, u)
		b, _ := json.Marshal(pu)
		var m map[string]interface{}
		json.Unmarshal(b, &m)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": attachOnlineMark(c, uid, friends, u, m)})
	}
}

// MutualGroupsHandler 共同群组列表（好友资料页；目标用户存在且状态正常即可）
func MutualGroupsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		otherID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || otherID <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		uid := middleware.CurrentUserID(c)
		groups, err := service.MutualGroups(c.Request.Context(), uid, otherID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": groups})
	}
}

// attachOnline 批量附加在线状态（用户列表）。
// 查看者语境：好友集合只查一次，手机号与在线状态的可见性按被查看者的
// phone_visible / online_visible 三档判定。
func attachOnline(c *gin.Context, users []model.User) []map[string]interface{} {
	viewer := middleware.CurrentUserID(c)
	friends := service.FriendIDSet(c.Request.Context(), viewer)
	out := make([]map[string]interface{}, 0, len(users))
	for i := range users {
		out = append(out, attachOnlineOne(c, viewer, friends, &users[i]))
	}
	return out
}

// attachOnlineOne 单个用户附加在线状态。
// 经 PublicUserOfForSet 脱敏后序列化：直接 Marshal model.User 会把 balance/frozen/
// email/registerIP/lastLoginIP/myInviteCode/isGuest/guestDeviceId/registerDevice/
// lastLoginAt 全量带出，phone 也会绕过可见范围控制。
func attachOnlineOne(c *gin.Context, viewerID int64, friends map[int64]struct{}, u *model.User) map[string]interface{} {
	b, _ := json.Marshal(service.PublicUserOfForSet(viewerID, friends, u))
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return attachOnlineMark(c, viewerID, friends, u, m)
}

// attachOnlineMark 给已序列化的用户对象（map）附加在线状态字段。
// 目标 online_visible=nobody 或 contacts 且查看者非好友时不下发真实在线态
// （统一回 online=false / 空设备），避免「从在线列表推算在场」的旁路泄露。
func attachOnlineMark(c *gin.Context, viewerID int64, friends map[int64]struct{}, target *model.User, m map[string]interface{}) map[string]interface{} {
	if service.OnlineVisibleTo(viewerID, friends, target) {
		online, devs := service.IsUserOnline(c.Request.Context(), target.ID)
		m["online"] = online
		m["onlineDevice"] = devs
		m["onlineText"] = service.OnlineDeviceZh(devs)
	} else {
		m["online"] = false
		m["onlineDevice"] = []string{}
		m["onlineText"] = ""
	}
	return m
}

// GetPrivacyHandler 我的隐私设置
func GetPrivacyHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		s, err := service.GetPrivacy(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": s})
	}
}

// UpdatePrivacyHandler 更新隐私设置（指针字段 nil = 不修改；布尔 false 必须能落库）
func UpdatePrivacyHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var req service.UpdatePrivacyReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.UpdatePrivacy(c.Request.Context(), uid, &req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// DeptTreeHandler 部门树（双语字段）
func DeptTreeHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		depts, err := service.DeptTree(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "获取部门失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": depts})
	}
}

// DeptMembersHandler 部门成员。
// 【注意】本 handler 当前**未在 handler.go 注册路由**（死代码）。
// 若将来启用，必须先把返回值由 []model.User 改为 []service.PublicUser 脱敏，
// 否则会把成员余额/手机号/邮箱等隐私字段返回给调用方。
func DeptMembersHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		users, err := service.DeptMembers(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "获取成员失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": users})
	}
}

// ChangePasswordHandler 修改登录密码（需校验原密码）。
// encPriv 可选：E2EE 用户改密码时客户端用新密码 re-wrap 的私钥备份密文，
// 与密码 hash 同事务落库（service.ChangePassword）；已有备份但缺失 → 4401。
func ChangePasswordHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			OldPassword string `json:"oldPassword"`
			NewPassword string `json:"newPassword"`
			EncPriv     string `json:"encPriv"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.ChangePassword(c.Request.Context(), uid, body.OldPassword, body.NewPassword, body.EncPriv); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// DeleteAccountHandler 注销账户
func DeleteAccountHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		if err := service.DeleteAccount(c.Request.Context(), uid); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// PayPwdSetHandler 设置 / 修改支付密码。
// 已设置过 → 视为「修改」，必须校验 oldPassword；未设置过 → 首次设置，只需 newPassword。
// 支付密码格式在服务层校验（6 位数字）。
func PayPwdSetHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			OldPassword string `json:"oldPassword"`
			NewPassword string `json:"newPassword" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		set, err := service.HasPayPwd(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		if set {
			err = service.ChangePayPwd(c.Request.Context(), uid, body.OldPassword, body.NewPassword)
		} else {
			err = service.SetPayPwd(c.Request.Context(), uid, body.NewPassword)
		}
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// BindPhoneSendCodeHandler 绑定手机号：发送短信验证码（需登录 + 图形验证码）
func BindPhoneSendCodeHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var req struct {
			Phone       string `json:"phone" binding:"required"`
			CountryCode string `json:"countryCode"`
			CaptchaID   string `json:"captchaId" binding:"required"`
			CaptchaCode string `json:"captchaCode" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.SendBindPhoneCode(c.Request.Context(), cfg, uid, req.Phone, req.CountryCode, req.CaptchaID, req.CaptchaCode); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// BindPhoneHandler 绑定手机号：校验验证码后写入
func BindPhoneHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var req struct {
			Phone       string `json:"phone" binding:"required"`
			CountryCode string `json:"countryCode"`
			Code        string `json:"code" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.BindPhone(c.Request.Context(), uid, req.Phone, req.CountryCode, req.Code); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

var _ = errs.Forbidden
