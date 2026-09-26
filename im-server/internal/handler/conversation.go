package handler

import (
	"net/http"
	"strconv"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/service"

	"github.com/gin-gonic/gin"
)

// ConvListHandler 会话列表
func ConvListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		items, err := service.ConvList(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "获取会话失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": items})
	}
}

// CreateDirectHandler 创建单聊
func CreateDirectHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			UserID int64 `json:"userId,string" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.UserID == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		conv, err := service.CreateDirect(c.Request.Context(), uid, body.UserID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": conv})
	}
}

// CreateGroupHandler 创建群聊（memberIds 为字符串数组，雪花 ID 精度安全）
func CreateGroupHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			NameZh    string   `json:"nameZh"`
			NameEn    string   `json:"nameEn"`
			MemberIDs []string `json:"memberIds"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		memberIDs := make([]int64, 0, len(body.MemberIDs))
		for _, s := range body.MemberIDs {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				memberIDs = append(memberIDs, v)
			}
		}
		conv, err := service.CreateGroup(c.Request.Context(), uid, body.NameZh, body.NameEn, memberIDs)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "创建失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": conv})
	}
}

// GroupInviteHandler 邀请成员（memberIds 字符串数组）
func GroupInviteHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			MemberIDs []string `json:"memberIds"`
		}
		c.ShouldBindJSON(&body)
		memberIDs := make([]int64, 0, len(body.MemberIDs))
		for _, s := range body.MemberIDs {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				memberIDs = append(memberIDs, v)
			}
		}
		if err := service.GroupInvite(c.Request.Context(), uid, convID, memberIDs); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// GroupRemoveHandler 移除成员
func GroupRemoveHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		targetID, _ := strconv.ParseInt(c.Param("userId"), 10, 64)
		if err := service.GroupRemove(c.Request.Context(), uid, convID, targetID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// GroupQuitHandler 退出群
func GroupQuitHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		service.GroupQuit(c.Request.Context(), uid, convID)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// GroupDisbandHandler 解散群
func GroupDisbandHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if err := service.GroupDisband(c.Request.Context(), uid, convID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// GroupUpdateHandler 更新群信息
func GroupUpdateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			NameZh         string `json:"nameZh"`
			NameEn         string `json:"nameEn"`
			AnnouncementZh string `json:"announcementZh"`
			AnnouncementEn string `json:"announcementEn"`
			Avatar         string `json:"avatar"` // 群头像 URL（先调 /upload 拿到）
		}
		c.ShouldBindJSON(&body)
		if err := service.GroupUpdate(c.Request.Context(), uid, convID, body.NameZh, body.NameEn, body.AnnouncementZh, body.AnnouncementEn, body.Avatar); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// GroupSettingsHandler 读取群管理设置（全员可读：成员页按"允许邀请"决定邀请入口）
func GroupSettingsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		s, err := service.GetGroupSettings(c.Request.Context(), uid, convID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": s})
	}
}

// SetGroupSettingsHandler 更新群管理设置（仅群主）
func SetGroupSettingsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			MuteAll           *bool `json:"muteAll"`
			PrivacyEnabled    *bool `json:"privacyEnabled"`
			AllowMemberInvite *bool `json:"allowMemberInvite"`
			QrJoinEnabled     *bool `json:"qrJoinEnabled"`
			ShowMembers       *bool `json:"showMembers"`
		}
		c.ShouldBindJSON(&body)
		// 未传的开关保持原值：先读旧值再覆盖
		old, err := service.GetGroupSettings(c.Request.Context(), uid, convID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		s := &service.GroupSettings{
			MuteAll:           old.MuteAll,
			PrivacyEnabled:    old.PrivacyEnabled,
			AllowMemberInvite: old.AllowMemberInvite,
			QrJoinEnabled:     old.QrJoinEnabled,
			ShowMembers:       old.ShowMembers,
		}
		if body.MuteAll != nil {
			s.MuteAll = *body.MuteAll
		}
		if body.PrivacyEnabled != nil {
			s.PrivacyEnabled = *body.PrivacyEnabled
		}
		if body.AllowMemberInvite != nil {
			s.AllowMemberInvite = *body.AllowMemberInvite
		}
		if body.QrJoinEnabled != nil {
			s.QrJoinEnabled = *body.QrJoinEnabled
		}
		if body.ShowMembers != nil {
			s.ShowMembers = *body.ShowMembers
		}
		if err := service.SetGroupSettings(c.Request.Context(), uid, convID, s); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// SetGroupAdminHandler 设置/取消管理员（仅群主）
func SetGroupAdminHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			UserID int64 `json:"userId,string" binding:"required"`
			Admin  bool  `json:"admin"`
		}
		c.ShouldBindJSON(&body)
		if err := service.SetGroupAdmin(c.Request.Context(), uid, convID, body.UserID, body.Admin); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// MuteMemberHandler 禁言/解除禁言成员（群主/管理员）
func MuteMemberHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			UserID  int64 `json:"userId,string" binding:"required"`
			Mute    bool  `json:"mute"`
			Minutes int   `json:"minutes"` // 禁言时长（分钟）；mute=false 时忽略
		}
		c.ShouldBindJSON(&body)
		if err := service.MuteMember(c.Request.Context(), uid, convID, body.UserID, body.Mute, body.Minutes); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// GroupJoinHandler 扫群二维码进群（需群开启"二维码进群"）
func GroupJoinHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		conv, err := service.GroupJoin(c.Request.Context(), uid, convID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": conv})
	}
}

// GroupPreviewHandler 扫码进群前的群信息预览（二次确认页：群名/头像/成员数）
func GroupPreviewHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		conv, cnt, err := service.GroupPreview(c.Request.Context(), convID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"conversation": conv,
			"memberCount":  cnt,
		}})
	}
}

// SetPinHandler 置顶
func SetPinHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			Pinned bool `json:"pinned"`
		}
		c.ShouldBindJSON(&body)
		service.SetPin(c.Request.Context(), uid, convID, body.Pinned)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// DeleteConvHandler 删除会话（从我的列表移除，不影响群成员身份）
func DeleteConvHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if err := service.DeleteConv(c.Request.Context(), uid, convID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 4004, "message": "会话不存在"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// SetMuteHandler 免打扰
func SetMuteHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			Mute bool `json:"mute"`
		}
		c.ShouldBindJSON(&body)
		service.SetMute(c.Request.Context(), uid, convID, body.Mute)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// SetArchiveHandler 归档 / 取消归档（个人开关，PUT /conversation/:id/archive）
func SetArchiveHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			Archived bool `json:"archived"`
		}
		c.ShouldBindJSON(&body)
		if err := service.SetArchive(c.Request.Context(), uid, convID, body.Archived); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// ConvMembersHandler 会话成员列表
// 可选 query：page/size（均 >0 时按页下发，memberCount 仍为全量计数；
// 不传=全量，向后兼容）。成员隐私开启时对普通成员忽略分页（只下发前 15 个）。
func ConvMembersHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		page, _ := strconv.Atoi(c.Query("page"))
		size, _ := strconv.Atoi(c.Query("size"))
		users, total, err := service.ConvMembers(c.Request.Context(), uid, convID, page, size)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		// 群顶栏「N 成员，M 在线」：在线数按全体成员统计（不受成员隐私截断影响，
		// 与 total 同语义——列表 data 可截断，计数照实下发）
		onlineCount := service.ConvOnlineCount(c.Request.Context(), convID)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": users,
			"memberCount": total, "onlineCount": onlineCount})
	}
}

// SetPinMessageHandler 置顶/取消置顶消息（群主/管理员）
func SetPinMessageHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			MsgID   int64  `json:"msgId,string"`
			Content string `json:"content"`
			Pinned  *bool  `json:"pinned"` // true=置顶 false=取消；缺省按 msgId>0 置顶
		}
		c.ShouldBindJSON(&body)
		pinned := true
		if body.Pinned != nil {
			pinned = *body.Pinned
		} else if body.MsgID <= 0 {
			pinned = false
		}
		if err := service.SetPinMessage(c.Request.Context(), uid, convID, body.MsgID, body.Content, pinned); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// PinnedMessagesHandler 置顶消息列表（返回 msgId 数组）
func PinnedMessagesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		ids, err := service.PinnedMessages(c.Request.Context(), uid, convID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": ids})
	}
}

// UpdateAnnouncementHandler 更新群公告（群主/管理员）
func UpdateAnnouncementHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			AnnouncementZh string `json:"announcementZh"`
			AnnouncementEn string `json:"announcementEn"`
		}
		c.ShouldBindJSON(&body)
		if err := service.UpdateAnnouncement(c.Request.Context(), uid, convID, body.AnnouncementZh, body.AnnouncementEn); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

var _ = errs.Forbidden
var _ = config.Config{}
