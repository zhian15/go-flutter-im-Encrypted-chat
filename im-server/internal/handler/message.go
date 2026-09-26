package handler

import (
	"net/http"
	"strconv"

	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/service"

	"github.com/gin-gonic/gin"
)

// SendMessageHandler 发送消息
func SendMessageHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var req service.SendMsgReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		msg, err := service.SendMessage(c.Request.Context(), uid, &req)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": msg})
	}
}

// HistoryHandler 历史消息（?convId=&beforeMsgId=&limit=）
func HistoryHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Query("convId"), 10, 64)
		before, _ := strconv.ParseInt(c.Query("beforeMsgId"), 10, 64)
		limit, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
		msgs, err := service.History(c.Request.Context(), uid, convID, before, limit)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": msgs})
	}
}

// RecallMessageHandler 撤回
func RecallMessageHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		msgID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if err := service.RecallMessage(c.Request.Context(), uid, msgID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// MarkReadHandler 上报已读
func MarkReadHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			ConversationID int64 `json:"conversationId,string" binding:"required"`
			MsgID          int64 `json:"msgId,string"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.ConversationID == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		service.MarkRead(c.Request.Context(), uid, body.ConversationID, body.MsgID)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// ReceiptsHandler 已读成员列表（群按人）
func ReceiptsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Query("convId"), 10, 64)
		msgID, _ := strconv.ParseInt(c.Query("msgId"), 10, 64)
		receipts, err := service.Receipts(c.Request.Context(), uid, convID, msgID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": receipts})
	}
}

// ClearHistoryHandler 删除聊天记录（仅单聊；软删位点，消息本体保留）。
// body.scope: "self" 仅为我删除 / "both" 为双方删除（缺省 both，兼容旧调用）
func ClearHistoryHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if convID == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		var body struct {
			Scope string `json:"scope"`
		}
		_ = c.ShouldBindJSON(&body)
		if body.Scope != "self" && body.Scope != "both" {
			body.Scope = "both"
		}
		clearedMsgId, err := service.ClearDirectHistory(c.Request.Context(), uid, convID, body.Scope)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"clearedMsgId": strconv.FormatInt(clearedMsgId, 10),
		}})
	}
}

// SyncHandler 增量补拉（重连补偿/上线拉取）：?convId=&afterSeq=&limit=
//
// 返回体由「裸数组」升级为 SyncResult：{list, hasMore, maxSeq, serverSeq, reset}。
// 客户端据此循环翻页（修 R-07）与水位回退自愈（修 R-04）。
// 老客户端读 data.list 会拿到 null —— 因此这里**同时**兼容输出 data.list 之外
// 不再提供裸数组；App 端本次同步改造（见 im-app/lib/services/conversation_service.dart）。
func SyncHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Query("convId"), 10, 64)
		afterSeq, _ := strconv.ParseInt(c.Query("afterSeq"), 10, 64)
		limit, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
		res, err := service.Sync(c.Request.Context(), uid, convID, afterSeq, limit)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": res})
	}
}

// LastSeqHandler 会话当前最新 seq（客户端进页时对齐服务端水位一次，成本可忽略）。
// ?convId= → {seq}
func LastSeqHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Query("convId"), 10, 64)
		seq, err := service.LastSeqForUser(c.Request.Context(), uid, convID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"seq": seq}})
	}
}

// SearchMessagesHandler 消息搜索：?kw=&convId=&page=&size=（仅自己参与的会话）
func SearchMessagesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		kw := c.Query("kw")
		convID, _ := strconv.ParseInt(c.Query("convId"), 10, 64)
		page, _ := strconv.Atoi(c.Query("page"))
		size, _ := strconv.Atoi(c.Query("size"))
		msgs, total, err := service.SearchMessages(c.Request.Context(), uid, kw, convID, page, size)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": msgs, "total": total}})
	}
}

// FavoriteAddHandler 收藏
func FavoriteAddHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		var body struct {
			ConversationID int64 `json:"conversationId,string" binding:"required"`
			MsgID          int64 `json:"msgId,string" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "参数错误"})
			return
		}
		if err := service.FavoriteAdd(c.Request.Context(), uid, body.ConversationID, body.MsgID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "收藏失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// FavoriteListHandler 我的收藏
func FavoriteListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		limit, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
		msgs, err := service.FavoriteList(c.Request.Context(), uid, limit)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "获取失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": msgs})
	}
}

// FilterMessagesHandler 消息类型筛选（会话相册/文件/链接/语音页）
func FilterMessagesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Query("conversationId"), 10, 64)
		beforeSeq, _ := strconv.ParseInt(c.Query("beforeSeq"), 10, 64)
		limit, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
		kind := c.Query("type")
		msgs, err := service.FilterMessages(c.Request.Context(), uid, convID, kind, beforeSeq, limit)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": msgs})
	}
}

// FilterMessageCountHandler 消息类型计数（资料页「照片和视频/文件/共享链接/语音消息」角标）
// GET /api/v1/message/media-count?conversationId=&type=image|video|file|link|voice
// 出参 data: {"count": N}。数据源与 /message/filter 同链路（按 type 统计消息表），
// 不能用群文件接口的 total——conv_file 只在文件消息落库时写入，图片/视频从不进集合。
func FilterMessageCountHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		convID, _ := strconv.ParseInt(c.Query("conversationId"), 10, 64)
		kind := c.Query("type")
		count, err := service.FilterMessageCount(c.Request.Context(), uid, convID, kind)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"count": count}})
	}
}

var _ = errs.Forbidden
