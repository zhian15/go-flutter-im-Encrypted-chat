package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/service"
	"github.com/yourcompany/im-server/internal/store"

	"github.com/gin-gonic/gin"
)

// FriendListHandler 好友列表（附带在线状态 + 备注）
func FriendListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		friends, err := service.FriendList(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "获取好友失败"})
			return
		}
		// FriendInfo → User（attachOnline 需要）+ 备注映射
		us := make([]model.User, 0, len(friends))
		remarks := make(map[int64]string, len(friends))
		for _, fi := range friends {
			us = append(us, fi.User)
			if fi.Remark != "" {
				remarks[fi.User.ID] = fi.Remark
			}
		}
		data := attachOnline(c, us)
		// 共同群组数（第十批）：批量 JOIN 聚合一次查出，key=好友ID
		fIDs := make([]int64, 0, len(friends))
		for _, fi := range friends {
			fIDs = append(fIDs, fi.User.ID)
		}
		commonGroups := service.CommonGroupCounts(c.Request.Context(), uid, fIDs)
		for i, item := range data {
			if idStr, ok := item["id"].(string); ok {
				if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
					if r, ok := remarks[id]; ok {
						item["remark"] = r
					}
				}
			}
			// 最近上线（好友资料页展示）：PublicUser 链路刻意剥离 lastLoginAt（对陌生人敏感），
			// 好友列表的查看者与条目必然互为好友，这里显式回填；未登录过为 null
			item["lastLoginAt"] = friends[i].User.LastLoginAt
			// 共同群组数（好友资料页「N 个共同群组」卡）：无共同群为 0
			item["commonGroupCount"] = commonGroups[friends[i].User.ID]
			// 靓号标记：好友 short_id 在预留池且已绑定本人 → 客户端资料页显示「靓ID」徽标
			// （us/data/friends 三者同序，见 attachOnline 实现）
			data[i]["vipShortId"] = service.IsVipShortID(c.Request.Context(),
				friends[i].User.ID, friends[i].User.ShortID)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
	}
}

// FriendRequestAddHandler 发起申请
func FriendRequestAddHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			ToID    int64  `json:"toId,string" binding:"required"`
			Message string `json:"message"`
			Source  *int   `json:"source"` // 来源 1搜索 2扫码 3名片，可选默认1
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.ToID == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.FriendRequestAdd(c.Request.Context(), uid, body.ToID, body.Message, body.Source); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// FriendRequestIncomingHandler 我收到的申请
func FriendRequestIncomingHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		reqs, err := service.FriendRequestIncoming(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "获取失败"})
			return
		}
		// 附加对方信息（昵称/账号/头像）→ 修复 PC"新朋友"脏数据（不显示昵称账号）
		out := make([]map[string]interface{}, 0, len(reqs))
		for i := range reqs {
			b, _ := json.Marshal(reqs[i])
			var m map[string]interface{}
			json.Unmarshal(b, &m)
			var u model.User
			if err := store.DB.First(&u, reqs[i].FromUser).Error; err == nil {
				m["fromUserName"] = u.Nickname
				m["fromUserAccount"] = u.Account
				m["fromUserAvatar"] = u.Avatar
				m["online"] = true
			}
			out = append(out, m)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": out})
	}
}

// FriendRequestOutgoingHandler 我发出的申请
func FriendRequestOutgoingHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		reqs, err := service.FriendRequestOutgoing(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "获取失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": reqs})
	}
}

// FriendRequestHandleHandler 处理申请
func FriendRequestHandleHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		agree := c.Query("agree") != "0"
		if err := service.FriendRequestHandle(c.Request.Context(), uid, id, agree); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// FriendDeleteHandler 删除好友
func FriendDeleteHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.FriendDelete(c.Request.Context(), uid, id); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// FriendSetRemarkHandler 设置备注
func FriendSetRemarkHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		var body struct {
			Remark string `json:"remark"`
		}
		c.ShouldBindJSON(&body)
		if err := service.FriendSetRemark(c.Request.Context(), uid, id, body.Remark); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "设置失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// BlacklistAddHandler 拉黑
func BlacklistAddHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			BlockID int64 `json:"blockId,string" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.BlockID == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.BlacklistAdd(c.Request.Context(), uid, body.BlockID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "拉黑失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// BlacklistRemoveHandler 移出黑名单
func BlacklistRemoveHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		service.BlacklistRemove(c.Request.Context(), uid, id)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// BlacklistListHandler 黑名单列表
func BlacklistListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		users, err := service.BlacklistList(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "获取失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": users})
	}
}

var _ = errs.Forbidden
