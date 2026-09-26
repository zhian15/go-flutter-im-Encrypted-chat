package handler

import (
	"log"
	"net/http"
	"strconv"

	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/service"

	"github.com/gin-gonic/gin"
)

// ============ 频道（Channel，type=3 会话） ============

// CreateChannelHandler 创建频道（创建者=频道主，唯一可发言者）
func CreateChannelHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var req service.CreateChannelReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		conv, err := service.CreateChannel(c.Request.Context(), uid, &req)
		if err != nil {
			// 业务错误透传 code/message；底层错误（DB 等）不把英文原始信息抛给用户
			if e, ok := err.(*errs.Err); ok {
				c.JSON(http.StatusOK, gin.H{"code": e.Code, "message": e.Msg})
				return
			}
			log.Printf("[channel] create failed uid=%d err=%v", uid, err)
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "创建失败，请稍后重试"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": conv})
	}
}

// ChannelInfoHandler 频道信息（按 ID 搜索/频道页；私密频道非成员返回 4001）。
// :id 两段式解析（service.ChannelInfoByRef）：先按雪花 int64，失败/查不到再按 short_id。
func ChannelInfoHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		ref := c.Param("id")
		if ref == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		d, err := service.ChannelInfoByRef(c.Request.Context(), uid, ref)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": d})
	}
}

// ChannelFollowHandler 关注频道（公开频道自助；幂等；私密 1003）
func ChannelFollowHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || convID <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		conv, err := service.ChannelFollow(c.Request.Context(), uid, convID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": conv})
	}
}

// ChannelUnfollowHandler 取消关注（频道主不可取关，需解散）
func ChannelUnfollowHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || convID <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.ChannelUnfollow(c.Request.Context(), uid, convID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}
