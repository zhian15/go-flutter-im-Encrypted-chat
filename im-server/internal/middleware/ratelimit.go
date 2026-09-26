package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yourcompany/im-server/internal/pkg/errs"
)

const (
	rlLimitPerMin = 10 // 每 (user,domain) 10/min
	rlBurst       = 10 // 令牌桶容量（允许初始突发）
	rlGlobalConc  = 20 // 全局并发上限
)

// ===== 发送限流（R-22）=====
//
// 旧代码只对 /link/meta 限流，发消息**完全无限流**：一个脚本/失控客户端就能
// 把 Mongo 写满、把 Redis 扇出打爆，进而拖慢所有人的发送与接收。
//
// 策略（与 Prometheus 风格一致的两级令牌桶）：
//   - per-user：20 msg/s，突发 50 —— 真人打字不可能超，但允许批量重发（幂等，安全）
//   - per-conversation：5 msg/s，突发 20 —— 大群里多人同时发言时给 Mongo 留出余量，
//     阈值按「2000 人群」设计：单人连续刷屏会被拦，正常群聊不会
//
// 超限返回 429（errs.TooManyRequests）。客户端退避后重发是安全的：
// clientMsgId 幂等，重复提交不会造成重复消息。

const (
	sendUserRate   = 20.0    // per-user 令牌/秒
	sendUserBurst  = 50.0    // per-user 桶容量
	sendConvRate   = 5.0     // per-conversation 令牌/秒
	sendConvBurst  = 20.0    // per-conversation 桶容量
	sendBodyMaxLen = 1 << 20 // 读取 body 的上限（1MB），防止被大 body 拖死
)

var (
	rlMu        sync.Mutex
	rlBuckets   = map[string]*tokenBucket{}
	rlGlobalSem = make(chan struct{}, rlGlobalConc)
)

// tokenBucket 简单令牌桶（标准库实现，等价于 golang.org/x/time/rate）。
type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	last     time.Time
	rate     float64 // tokens/秒
	capacity float64
}

// newBucket 构造一个指定速率/容量的令牌桶
func newBucket(rate, capacity float64) *tokenBucket {
	return &tokenBucket{
		tokens:   capacity,
		last:     time.Now(),
		rate:     rate,
		capacity: capacity,
	}
}

func getBucket(key string) *tokenBucket {
	rlMu.Lock()
	defer rlMu.Unlock()
	b, ok := rlBuckets[key]
	if !ok {
		b = newBucket(float64(rlLimitPerMin)/60.0, rlBurst)
		rlBuckets[key] = b
	}
	return b
}

// sendBuckets 发送限流专用桶（与 link meta 桶分开，避免参数互相覆盖）
var (
	sendMu      sync.Mutex
	sendBuckets = map[string]*tokenBucket{}
)

func sendBucket(key string, rate, capacity float64) *tokenBucket {
	sendMu.Lock()
	defer sendMu.Unlock()
	b, ok := sendBuckets[key]
	if !ok {
		b = newBucket(rate, capacity)
		sendBuckets[key] = b
	}
	return b
}

// SendRateLimit 发送消息限流：per-user 20/s(突发50) + per-conversation 5/s(突发20)。
// 挂在 POST /api/v1/message/send 上（在 Auth 之后，因此 CurrentUserID 可用）。
func SendRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := CurrentUserID(c)
		uKey := "send:u:" + strconv.FormatInt(uid, 10)
		if !sendBucket(uKey, sendUserRate, sendUserBurst).allow() {
			c.JSON(http.StatusOK, gin.H{"code": errs.TooManyRequests.Code, "message": errs.TooManyRequests.Msg})
			c.Abort()
			return
		}
		// 会话维度：需要从 body 里取 conversationId。
		// 读完后必须把 body 塞回去，否则后续 ShouldBindJSON 读到一个已关闭的 reader。
		if conv := peekConversationID(c); conv > 0 {
			cKey := "send:c:" + strconv.FormatInt(conv, 10)
			if !sendBucket(cKey, sendConvRate, sendConvBurst).allow() {
				c.JSON(http.StatusOK, gin.H{"code": errs.TooManyRequests.Code, "message": errs.TooManyRequests.Msg})
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

// peekConversationID 从请求体里只读出 conversationId，并把 body 复原。
// 兼容 "123" 与 123 两种写法（前端雪花 ID 常以字符串发送）。
func peekConversationID(c *gin.Context) int64 {
	if c.Request.Body == nil {
		return 0
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, sendBodyMaxLen))
	// 无论成败都要复原：读失败时给一个空 body，让下游的参数校验去报错，
	// 绝不能让限流中间件自己吞掉请求体导致后续接口读到半个 JSON。
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil || len(raw) == 0 {
		return 0
	}
	var body struct {
		ConversationID json.RawMessage `json:"conversationId"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.ConversationID) == 0 {
		return 0
	}
	s := string(body.ConversationID)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// LinkMetaRateLimit 链接元数据抓取限流中间件：
// 每 (user,domain) 10/min 令牌桶 + 全局并发 20。超限返回 429（TooManyRequests）。
// 全局信号量先获取（控制并发），再按 (user,domain) 限流（控制频率）。
func LinkMetaRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 全局并发
		select {
		case rlGlobalSem <- struct{}{}:
		default:
			c.JSON(http.StatusOK, gin.H{"code": errs.TooManyRequests.Code, "message": errs.TooManyRequests.Msg})
			c.Abort()
			return
		}
		defer func() { <-rlGlobalSem }()

		uid := CurrentUserID(c)
		domain := ""
		if u, err := url.Parse(c.Query("url")); err == nil {
			domain = u.Hostname()
		}
		if domain != "" {
			key := strconv.FormatInt(uid, 10) + ":" + domain
			if !getBucket(key).allow() {
				c.JSON(http.StatusOK, gin.H{"code": errs.TooManyRequests.Code, "message": errs.TooManyRequests.Msg})
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
