package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/store"
)

// ============ 极光推送（离线消息兜底，后台可配置） ============
//
// 设计：
//   - alias = 用户 ID 字符串（客户端登录后 setAlias(uid)，登出 deleteAlias），
//     服务端不需要维护 registration_id 映射表，一个 alias 天然覆盖用户的多台设备。
//   - 配置存 sys_config（后台「推送配置」页可改），回退环境变量 JPUSH_ENABLED /
//     JPUSH_APP_KEY / JPUSH_MASTER_SECRET；读库结果缓存 60s，改配置最迟 1 分钟生效。
//   - 推送时机：SendMessage 落库 + WS 广播之后，只推「当前不在 WS 在线集合」的接收者，
//     在线用户走长连接不需要系统通知。异步 goroutine 执行，失败只记日志不影响消息主流程。

// JPushConfig 推送配置（sys_config 键：jpush_enabled / jpush_app_key / jpush_master_secret / jpush_apns_production）
type JPushConfig struct {
	Enabled        bool   `json:"enabled"`
	AppKey         string `json:"appKey"`
	MasterSecret   string `json:"masterSecret"`
	ApnsProduction bool   `json:"apnsProduction"` // iOS APNs 生产环境（开发调试应为 false）
}

var (
	jpushMu     sync.Mutex
	jpushCache  *JPushConfig
	jpushLoaded time.Time
)

// GetJPushConfig 读取推送配置（数据库优先，回退环境变量；60s 缓存）
func GetJPushConfig(ctx context.Context) *JPushConfig {
	jpushMu.Lock()
	defer jpushMu.Unlock()
	if jpushCache != nil && time.Since(jpushLoaded) < 60*time.Second {
		return jpushCache
	}
	c := &JPushConfig{
		Enabled: boolVal(SysConfigGet(ctx, "jpush_enabled", os.Getenv("JPUSH_ENABLED") == "true")),
		AppKey:  strVal(SysConfigGet(ctx, "jpush_app_key", os.Getenv("JPUSH_APP_KEY"))),
		MasterSecret: strVal(SysConfigGet(ctx, "jpush_master_secret",
			os.Getenv("JPUSH_MASTER_SECRET"))),
		ApnsProduction: boolVal(SysConfigGet(ctx, "jpush_apns_production", false)),
	}
	jpushCache = c
	jpushLoaded = time.Now()
	// 诊断日志：打印服务端实际读到的生效值（与后台页面/数据库截图对照用）。
	// MasterSecret 只打长度，避免泄密。
	log.Printf("[jpush] config loaded: enabled=%v appKey=%q(%.4s...) secretLen=%d production=%v",
		c.Enabled, c.AppKey, c.AppKey, len(c.MasterSecret), c.ApnsProduction)
	return c
}

// PushMessageOffline 离线消息推送（异步调用）：只推不在线的接收者。
// 标题/内容：单聊 = 昵称 + 内容预览；群聊 = 群名 + 「昵称：预览」。
func PushMessageOffline(ctx context.Context, senderID int64, receiverIDs []int64, msg *model.Message) {
	jc := GetJPushConfig(ctx)
	gc := GetGetuiConfig(ctx)
	gtActive := iOSPushProvider(ctx) == "getui" && gc.ready()
	jpushReady := jc.Enabled && jc.AppKey != "" && jc.MasterSecret != ""
	if !jpushReady && !gtActive {
		// 诊断日志：这里曾是静默 return，配置问题无法从日志区分「未调用」和「被配置拦截」
		log.Printf("[push] skip: config not ready (jpush: enabled=%v appKeySet=%v secretSet=%v; getui: enabled=%v appIdSet=%v keySet=%v secretSet=%v) msg_id=%d type=%d",
			jc.Enabled, jc.AppKey != "", jc.MasterSecret != "",
			gc.Enabled, gc.AppID != "", gc.AppKey != "", gc.AppSecret != "",
			msg.MsgID, msg.Type)
		return
	}
	// 通话信令（type=7）：只有 invite（呼叫邀请）值得推送；
	// hangup/reject/cancel 等结束信令对用户只是历史记录，推送纯属噪音，静默跳过。
	isCallInviteMsg := msg.Type == model.MsgCall && isCallInvite(msg.Content)
	if msg.Type == model.MsgCall && !isCallInviteMsg {
		log.Printf("[jpush] skip: call signal not invite, msg_id=%d", msg.MsgID)
		return
	}
	// 过滤出离线接收者（在线用户已通过 WS 实时收到）。
	// 【例外：通话邀请无条件推送】——安卓保活的设备退后台后 WS 仍连着，服务端
	// 会判定「在线」而跳过推送；但后台进程被系统冻结，WS 帧收不到也响不了铃，
	// 通话邀请在这个场景两头落空（2026-09-17 用户实测）。推送是唯一可靠触达
	// 通道，invite 按 voip-push 惯例跳过在线过滤：后台设备收到通知+提示音，
	// 前台在线用户会同时收到 WS 响铃 + 推送通知（重复但可靠，点通知进会话）。
	aliases := make([]string, 0, len(receiverIDs))
	for _, uid := range receiverIDs {
		if online, _ := IsUserOnline(ctx, uid); online && !isCallInviteMsg {
			continue
		}
		aliases = append(aliases, fmt.Sprintf("%d", uid))
	}
	if len(aliases) == 0 {
		log.Printf("[jpush] skip: no offline receivers (all online), msg_id=%d receivers=%v", msg.MsgID, receiverIDs)
		return
	}

	// 标题：群聊用群名，单聊用发送者昵称
	var conv model.Conversation
	_ = store.DB.First(&conv, msg.ConversationID).Error
	var sender model.User
	senderName := ""
	if err := store.DB.First(&sender, senderID).Error; err == nil {
		senderName = sender.Nickname
	}
	title := senderName
	preview := msgPushPreview(msg)
	if conv.ID != 0 && conv.Type == model.ConvGroup {
		title = conv.NameZh
		if title == "" {
			title = conv.NameEn
		}
		if senderName != "" {
			preview = senderName + ": " + preview
		}
	}
	// 通话邀请：文案改成「来电」语义（正文 [语音通话] 太像普通消息，2026-09-17 用户反馈），
	// 单聊「邀请你进行语音通话」；群聊带发起人「昵称 邀请你进行视频通话」。
	if isCallInviteMsg {
		callLabel := "语音通话"
		var sig struct {
			CallType string `json:"callType"`
		}
		_ = json.Unmarshal([]byte(msg.Content), &sig)
		if sig.CallType == "video" {
			callLabel = "视频通话"
		}
		if conv.ID != 0 && conv.Type == model.ConvGroup && senderName != "" {
			preview = senderName + " 邀请你进行" + callLabel
		} else {
			preview = "邀请你进行" + callLabel
		}
	}

	// 会话名：群聊用群名，单聊用发送者昵称（客户端点击通知跳会话页显示标题）
	convName := senderName
	if conv.ID != 0 && conv.Type == model.ConvGroup && conv.NameZh != "" {
		convName = conv.NameZh
	}
	extras := map[string]string{
		"conversationId": fmt.Sprintf("%d", msg.ConversationID),
		"msgId":          fmt.Sprintf("%d", msg.MsgID),
		"senderId":       fmt.Sprintf("%d", senderID),
		"convType":       fmt.Sprintf("%d", conv.Type),
		"convName":       convName,
	}
	if isCallInviteMsg {
		// 通话邀请额外带 callType（voice/video），App 点通知可区分语音/视频来电
		var sig struct {
			CallType string `json:"callType"`
		}
		_ = json.Unmarshal([]byte(msg.Content), &sig)
		extras["msgType"] = "call"
		extras["callType"] = sig.CallType
	}
	// 分平台路由：iOS 服务商可切换（极光 / 个推），Android 固定走极光厂商通道。
	// 各分腿的成功/失败日志由 dispatchOfflinePush 内部输出（grep [getui]/[jpush]）。
	log.Printf("[push] dispatch msg_id=%d type=%d callInvite=%v conv=%d title=%q alert=%q",
		msg.MsgID, msg.Type, isCallInviteMsg, msg.ConversationID, title, preview)
	dispatchOfflinePush(ctx, aliases, title, preview, extras, isCallInviteMsg)
}

// jpushToAliases 调极光 REST API v3/push 按 alias 群发通知。
// androidOnly=true 时 platform 限 ["android"]（iOS 已由个推负责，防止双推）；
// callInvite=true 时走「来电」样式：Android 最高优先级 + category=call（横幅弹出、
// 部分机型可全屏来电 UI），与普通消息通知明确区分（2026-09-17 用户反馈）。
func jpushToAliases(ctx context.Context, c *JPushConfig, aliases []string, title, alert string, extras map[string]string, callInvite, androidOnly bool) error {
	platform := interface{}("all")
	if androidOnly {
		platform = []string{"android"}
	}
	android := map[string]interface{}{
		"alert":  alert,
		"title":  title,
		"extras": extras,
	}
	ios := map[string]interface{}{
		"alert":  alert,
		"sound":  "default",
		"badge":  "+1",
		"extras": extras,
	}
	if callInvite {
		// Android：priority 2 = 最高（横幅+响铃）；category "call" 声明通话类通知
		android["priority"] = 2
		android["category"] = "call"
		// iOS：aps category（配合客户端 UNNotificationCategory 可定义接听/挂断动作，
		// 未注册 category 时系统按普通通知展示，无害）
		ios["category"] = "call"
		ios["sound"] = "default"
		ios["interruption-level"] = "time-sensitive"
	}
	body := map[string]interface{}{
		"platform": platform,
		"audience": map[string]interface{}{"alias": aliases},
		"notification": map[string]interface{}{
			"android": android,
			"ios":     ios,
		},
		"options": map[string]interface{}{
			"apns_production": c.ApnsProduction,
			"time_to_live":    86400,
		},
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.jpush.cn/v3/push", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// 极光 HTTP Basic 认证：base64(appKey:masterSecret)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.
		EncodeToString([]byte(c.AppKey+":"+c.MasterSecret)))

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var r struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		MsgID int64 `json:"msg_id"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("jpush bad response: %s", strings.TrimSpace(string(raw)))
	}
	if r.Error != nil {
		return fmt.Errorf("jpush error %d: %s", r.Error.Code, r.Error.Message)
	}
	return nil
}

// isCallInvite 判断 type=7 通话信令 content 是否为 invite（呼叫邀请）。
// action 缺省视为 invite（与客户端 handleCallMessage 的 `sig.action || 'invite'` 语义一致）。
func isCallInvite(content string) bool {
	var sig struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(content), &sig); err != nil {
		return false
	}
	return sig.Action == "" || sig.Action == "invite"
}

// msgPushPreview 消息内容 → 通知预览文案
func msgPushPreview(msg *model.Message) string {
	const maxLen = 50
	cut := func(s string) string {
		r := []rune(s)
		if len(r) > maxLen {
			return string(r[:maxLen]) + "…"
		}
		return s
	}
	switch msg.Type {
	case model.MsgText:
		return cut(msg.Content)
	case model.MsgImage:
		return "[图片]"
	case model.MsgFile:
		return "[文件]"
	case model.MsgVoice:
		return "[语音]"
	case model.MsgE2Text:
		// 端到端加密文本：服务端不可解密，离线推送预览固定占位（§36 定稿）
		return "[加密消息]"
	case model.MsgVideo:
		return "[视频]"
	case model.MsgRedPacket:
		return "[红包]"
	case model.MsgTransfer:
		return "[转账]"
	case model.MsgCard:
		return "[名片]"
	case model.MsgLocation:
		return "[位置]"
	case model.MsgCall:
		// 通话信令：离线只推 invite（PushMessageOffline 已过滤非 invite），
		// 预览文案带上通话类型，让被叫一眼知道是语音还是视频来电。
		var sig struct {
			CallType string `json:"callType"`
		}
		_ = json.Unmarshal([]byte(msg.Content), &sig)
		if sig.CallType == "video" {
			return "[视频通话]"
		}
		return "[语音通话]"
	default:
		return "[新消息]"
	}
}
