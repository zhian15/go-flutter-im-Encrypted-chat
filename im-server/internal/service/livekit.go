// LiveKit 开源 SFU 音视频（第三种通话引擎：call_engine=livekit）
//
// 与 WebRTC Mesh / TRTC 的区别：客户端持后端签发的 JoinToken（JWT）直连
// LiveKit SFU 房间，媒体经 SFU 转发，**不需要 sdp/ice 信令**（信令通道仅保留
// 呼叫控制 invite/accept/hangup）。房间号 = 会话 ID，双方进同一房间即可互通。
//
// 后台 sys_config：
//   - livekit_url       wss://your-host（LiveKit 服务端 WebSocket 地址）
//   - livekit_api_key   LiveKit devkey 的 API Key
//   - livekit_api_secret LiveKit devkey 的 API Secret
//
// Token 规范（官方 access token，HS256 JWT）：
//
//	claims = {iss: apiKey, sub: identity, exp, nbf, name?, video: {roomJoin, room, canPublish, canSubscribe, canPublishData}}
//
// 参考 https://github.com/livekit/protocol/blob/main/auth/token.go
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// LiveKitConfigGet 读取生效的 LiveKit 配置（sys_config 优先，无 env 兜底——
// LiveKit 是自托管服务，url/key/secret 全部走后台「系统配置」）。
// ok=false 表示三项任一为空，客户端应提示「通话引擎未配置」。
func LiveKitConfigGet(ctx context.Context) (url, apiKey, apiSecret string, ok bool) {
	url = strings.TrimRight(strings.TrimSpace(SysConfigString(ctx, "livekit_url", "")), "/")
	apiKey = strings.TrimSpace(SysConfigString(ctx, "livekit_api_key", ""))
	apiSecret = strings.TrimSpace(SysConfigString(ctx, "livekit_api_secret", ""))
	ok = url != "" && apiKey != "" && apiSecret != ""
	return
}

// GenerateLiveKitToken 签发 LiveKit 房间加入令牌（HS256 JWT）
//
// identity：房间内参与者唯一标识（用用户 ID 字符串，对端据此渲染视频视图）；
// room：房间号（= 会话 ID）；name：参与者显示名（可空）；
// ttl：令牌有效期（通话页用完即弃，2 小时足够覆盖最长通话）。
func GenerateLiveKitToken(apiKey, apiSecret, identity, room, name string, ttl time.Duration) (string, error) {
	if apiKey == "" || apiSecret == "" || identity == "" || room == "" {
		return "", fmt.Errorf("LiveKit 未配置（url/api_key/api_secret）")
	}
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	now := time.Now()
	grants := map[string]interface{}{
		"roomJoin":       true,
		"room":           room,
		"canPublish":     true,
		"canSubscribe":   true,
		"canPublishData": true,
	}
	claims := jwt.MapClaims{
		"iss":   apiKey,
		"sub":   identity,
		"exp":   now.Add(ttl).Unix(),
		"nbf":   now.Add(-30 * time.Second).Unix(),
		"video": grants,
	}
	if name != "" {
		claims["name"] = name
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(apiSecret))
}
