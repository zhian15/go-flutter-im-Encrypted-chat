// TRTC 实时音视频（腾讯云）配置 + UserSig 生成
// 需求7：PC 端和移动端通过后端拉取 SDKAppID 和 UserSig，避免密钥暴露在客户端
//
// UserSig 算法：官方 tls-sig-api-v2（HMAC-SHA256 + JSON + zlib + 自定义 base64url）
// 参考 https://github.com/Tencent-RTC/tls-sig-api-v2-golang
// 注意：网上流传的 TLV（0x00~0x06）写法是 2019.07.19 之前的旧算法，
// 2019.07.19 之后创建的 SDKAppID 一律用下面的 v2.0 算法，否则会报
// errorCode 70003 "The UserSig in use is illegal"。
package service

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/pkg/errs"
)

// RTCIceServer WebRTC STUN/TURN 服务器（webrtc_ice_servers JSON 结构，原样下发给客户端）
type RTCIceServer struct {
	URLs       interface{} `json:"urls"`                 // string 或 []string
	Username   string      `json:"username,omitempty"`   // TURN 用户名
	Credential string      `json:"credential,omitempty"` // TURN 凭证
}

// WebRTCConfig WebRTC 模式下发给客户端的配置
type WebRTCConfig struct {
	IceServers []RTCIceServer `json:"iceServers"`
}

// TRTCConfig 暴露给客户端的实时音视频配置（不含 secretKey）
//
// engine 字段（后台 call_engine）决定客户端用哪种媒体引擎：
//   - trtc（默认）：行为与老版本完全一致（enabled/appId）；
//   - webrtc：Enabled 恒为 false —— 老客户端不认识 engine 字段、只看 enabled，
//     会提示"TRTC 未配置"优雅拒绝（老客户端本来也不支持 WebRTC）；
//     新客户端优先看 engine，走 WebRTC 流程。
type TRTCConfig struct {
	Enabled bool          `json:"enabled"`
	Engine  string        `json:"engine"` // trtc | webrtc | livekit
	AppID   int           `json:"appId"`  // SDKAppID（engine=trtc 时有效）
	SdkURL  string        `json:"sdkUrl"` // TRTC SDK 入口
	WebRTC  *WebRTCConfig `json:"webrtc,omitempty"`
	LiveKit *LiveKitConf  `json:"livekit,omitempty"`
}

// LiveKitConf LiveKit 模式下发给客户端的配置（SFU 房间地址）
type LiveKitConf struct {
	URL string `json:"url"`
}

// GetTRTCConfig 读取后端配置（用户在「系统配置」页签设置 trtc_app_id / trtc_secret_key）
func GetTRTCConfig(ctx context.Context, cfg *config.Config) TRTCConfig {
	// SysConfigGet 返回 JSON 反序列化后的值：数字可能存成 float64，也可能存成字符串
	appID := 0
	switch v := SysConfigGet(ctx, "trtc_app_id", 0).(type) {
	case int:
		appID = v
	case float64:
		appID = int(v)
	case string:
		if s := strings.TrimSpace(v); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				appID = n
			}
		}
	}
	enabled := true
	switch e := SysConfigGet(ctx, "trtc_enabled", "1").(type) {
	case string:
		if e == "0" {
			enabled = false
		}
	case bool:
		if !e {
			enabled = false
		}
	case float64:
		if e == 0 {
			enabled = false
		}
	}

	// 通话引擎：后台 call_engine（trtc | webrtc | livekit），默认 trtc
	engine := strings.ToLower(strings.TrimSpace(SysConfigString(ctx, "call_engine", "trtc")))
	if engine != "webrtc" && engine != "livekit" {
		engine = "trtc"
	}

	conf := TRTCConfig{
		Enabled: enabled && appID > 0,
		Engine:  engine,
		AppID:   appID,
		SdkURL:  "https://web.sdk.qcloud.com/trtc",
	}

	// WebRTC 模式：解析并下发 ICE 服务器；Enabled 置 false（见 TRTCConfig 注释，
	// 让只认 enabled 的老客户端优雅拒绝）
	if engine == "webrtc" {
		conf.Enabled = false
		wc := &WebRTCConfig{IceServers: []RTCIceServer{}}
		if raw := strings.TrimSpace(SysConfigString(ctx, "webrtc_ice_servers", "")); raw != "" {
			var servers []RTCIceServer
			if err := json.Unmarshal([]byte(raw), &servers); err == nil && len(servers) > 0 {
				wc.IceServers = servers
			}
		}
		conf.WebRTC = wc
	}

	// LiveKit 模式：下发 SFU 房间地址；Enabled 同样置 false（老客户端优雅拒绝）
	if engine == "livekit" {
		conf.Enabled = false
		lkURL, _, _, ok := LiveKitConfigGet(ctx)
		if ok {
			conf.LiveKit = &LiveKitConf{URL: lkURL}
		}
	}
	return conf
}

// SysConfigString 读取系统配置并安全转为字符串（数字/布尔也转，避免类型断言失败）
func SysConfigString(ctx context.Context, key string, def string) string {
	switch v := SysConfigGet(ctx, key, def).(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// GenerateUserSig 生成腾讯云 TRTC UserSig（官方 tls-sig-api-v2 算法）
//
// 流程：
//  1. 待签内容 =
//     "TLS.identifier:<userid>\nTLS.sdkappid:<appid>\nTLS.time:<now>\nTLS.expire:<expire>\n"
//  2. HMAC-SHA256(secretKey, 待签内容) → base64 标准编码 → TLS.sig
//  3. 拼 JSON：{TLS.ver:"2.0", TLS.identifier, TLS.sdkappid, TLS.expire, TLS.time, TLS.sig}
//  4. zlib 压缩 → 自定义 base64url（+ → *，/ → -，= → _）
//
// expireSeconds 为「相对秒数」（如 604800 = 7 天），不是绝对时间戳。
// 返回：usersig、绝对过期时间戳、错误
func GenerateUserSig(appID int, secretKey, userID string, expireSeconds int64) (string, int64, error) {
	if appID == 0 || secretKey == "" || userID == "" {
		return "", 0, &errs.Err{Code: 500, Msg: "TRTC 未配置"}
	}
	if expireSeconds <= 0 {
		expireSeconds = 7 * 24 * 3600
	}
	now := time.Now().Unix()
	expire := int(expireSeconds)

	// 1. 计算签名
	sig := hmacsha256(appID, secretKey, userID, now, expire)

	// 2. 组装 JSON 文档
	sigDoc := map[string]interface{}{
		"TLS.ver":        "2.0",
		"TLS.identifier": userID,
		"TLS.sdkappid":   appID,
		"TLS.expire":     expire,
		"TLS.time":       now,
		"TLS.sig":        sig,
	}
	data, err := json.Marshal(sigDoc)
	if err != nil {
		return "", 0, err
	}

	// 3. zlib 压缩
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return "", 0, err
	}
	if err := w.Close(); err != nil {
		return "", 0, err
	}

	// 4. 自定义 base64url
	return base64urlEncode(buf.Bytes()), now + int64(expire), nil
}

// hmacsha256 官方待签内容格式（顺序固定：identifier → sdkappid → time → expire）
func hmacsha256(sdkAppID int, key, identifier string, currTime int64, expire int) string {
	var contentToBeSigned string
	contentToBeSigned = "TLS.identifier:" + identifier + "\n"
	contentToBeSigned += "TLS.sdkappid:" + strconv.Itoa(sdkAppID) + "\n"
	contentToBeSigned += "TLS.time:" + strconv.FormatInt(currTime, 10) + "\n"
	contentToBeSigned += "TLS.expire:" + strconv.Itoa(expire) + "\n"

	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(contentToBeSigned))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// base64urlEncode 腾讯云自定义 URL 安全 base64：+ → *，/ → -，= → _
// 注意：这不是 RFC 4648 标准 base64url，别用 base64.URLEncoding 替代
func base64urlEncode(data []byte) string {
	str := base64.StdEncoding.EncodeToString(data)
	str = strings.Replace(str, "+", "*", -1)
	str = strings.Replace(str, "/", "-", -1)
	str = strings.Replace(str, "=", "_", -1)
	return str
}

// base64urlDecode base64urlEncode 的逆运算（校验/调试用）
func base64urlDecode(str string) ([]byte, error) {
	str = strings.Replace(str, "_", "=", -1)
	str = strings.Replace(str, "-", "/", -1)
	str = strings.Replace(str, "*", "+", -1)
	return base64.StdEncoding.DecodeString(str)
}
