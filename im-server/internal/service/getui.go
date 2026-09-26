package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/store"
)

// ============ 个推（GeTui）推送 + iOS 服务商分平台路由 ============
//
// 背景（2026-09-21）：极光 iOS 通道应用级故障（受理正常但 ios_apns_sent 恒为 0，
// 卸载重装/换 p8/实名认证均无效），新增个推作为 iOS 备选通道。
//
// 设计：
//   - sys_config 键：push_provider_ios（jpush/getui）+ getui_enabled /
//     getui_app_id / getui_app_key / getui_app_secret，后台「推送配置」可改，
//     读库 60s 缓存（与 GetJPushConfig 同款）。
//   - 分平台路由（dispatchOfflinePush）：
//       provider=getui 且个推已配置 → 设备表里「有 iOS 设备」的接收者走个推，
//       其余（Android/Web/PC）走极光（platform 限 ["android"]，防止 iOS 被双推）；
//       provider=jpush 或个推未配置 → 行为与旧版完全一致（全量走极光）。
//   - 别名约定与极光一致：alias = 用户 ID 字符串。App 端拿到个推 client id 后调
//     POST /user/push-token 上报，服务端存 device.push_token + 调个推绑定别名。
//   - 个推 REST v2：https://restapi.getui.com/v2/{appId}/...，鉴权 token
//     （sign=sha256hex(appkey+timestamp+mastersecret)）缓存至过期前 5 分钟。
//   - 别名绑定契约（实测确认）：POST /user/alias + {"data_list":[{cid,alias}]}；
//     解绑 DELETE /user/alias 同 body。旧式 /cid/alias/{cid} 路径不存在（404）。
//   - 别名推送契约（实测确认）：POST /push/single/alias（单别名，audience.alias
//     传一个元素）。旧式 /push/alias 路径不存在（404）。
//   - iOS 环境（开发/生产）不需要下发参数：个推按 SDK 注册时上报的设备环境自动
//     匹配对应证书，无极光 apns_production 的坑。

const gtBaseURL = "https://restapi.getui.com/v2"

// GetuiConfig 个推配置（sys_config 键：getui_enabled / getui_app_id / getui_app_key /
// getui_app_secret（iOS SDK 用，随 /auth/config 下发）/ getui_master_secret（REST v2 鉴权签名用））
type GetuiConfig struct {
	Enabled      bool   `json:"enabled"`
	AppID        string `json:"appId"`
	AppKey       string `json:"appKey"`
	AppSecret    string `json:"appSecret"`
	MasterSecret string `json:"masterSecret"`
}

var (
	getuiMu     sync.Mutex
	getuiCache  *GetuiConfig
	getuiLoaded time.Time

	gtTokenMu    sync.Mutex
	gtToken      string
	gtTokenAppID string
	gtTokenExp   time.Time
)

// GetGetuiConfig 读取个推配置（数据库优先；60s 缓存）
func GetGetuiConfig(ctx context.Context) *GetuiConfig {
	getuiMu.Lock()
	defer getuiMu.Unlock()
	if getuiCache != nil && time.Since(getuiLoaded) < 60*time.Second {
		return getuiCache
	}
	c := &GetuiConfig{
		Enabled:   boolVal(SysConfigGet(ctx, "getui_enabled", false)),
		AppID:     strVal(SysConfigGet(ctx, "getui_app_id", "")),
		AppKey:    strVal(SysConfigGet(ctx, "getui_app_key", "")),
		AppSecret: strVal(SysConfigGet(ctx, "getui_app_secret", "")),
		// 官方 v2 鉴权公式 sign=sha256(appkey+timestamp+mastersecret)，用的是 MasterSecret
		// 而非 AppSecret（两把钥匙：AppSecret 给客户端 SDK，MasterSecret 给服务端签名）。
		// 未单独配置时回退旧键，兼容只填过一个值的存量部署。
		MasterSecret: strVal(SysConfigGet(ctx, "getui_master_secret",
			strVal(SysConfigGet(ctx, "getui_app_secret", "")))),
	}
	getuiCache = c
	getuiLoaded = time.Now()
	log.Printf("[getui] config loaded: enabled=%v appId=%q keySet=%v secretSet=%v masterSet=%v",
		c.Enabled, c.AppID, c.AppKey != "", c.AppSecret != "", c.MasterSecret != "")
	return c
}

func (c *GetuiConfig) ready() bool {
	return c.Enabled && c.AppID != "" && c.AppKey != "" && c.MasterSecret != ""
}

// iOSPushProvider iOS 端推送服务商："jpush"（默认）或 "getui"
func iOSPushProvider(ctx context.Context) string {
	p := strings.ToLower(strVal(SysConfigGet(ctx, "push_provider_ios", "jpush")))
	if p == "getui" {
		return "getui"
	}
	return "jpush"
}

// gtPost 个推 REST v2 通用 POST。token 为空时不带鉴权头（auth 接口本身）。
func gtPost(ctx context.Context, c *GetuiConfig, path string, body []byte, token string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		gtBaseURL+"/"+c.AppID+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json;charset=utf-8")
	if token != "" {
		req.Header.Set("token", token)
	}
	// 15s：实测个推首推偶发 >10s 才返回，8s 会误判超时
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var r struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("getui bad response: %s", strings.TrimSpace(string(raw)))
	}
	if r.Code != 0 {
		return nil, fmt.Errorf("getui error %d: %s", r.Code, r.Msg)
	}
	return r.Data, nil
}

// gtAuthToken 获取个推鉴权 token（缓存至过期前 5 分钟）。
// sign = sha256hex(appkey + timestamp(毫秒) + appsecret)
func gtAuthToken(ctx context.Context, c *GetuiConfig) (string, error) {
	gtTokenMu.Lock()
	defer gtTokenMu.Unlock()
	if gtToken != "" && gtTokenAppID == c.AppID && time.Now().Before(gtTokenExp) {
		return gtToken, nil
	}
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	// 官方公式：sign = sha256(appkey + timestamp(毫秒) + mastersecret)
	sum := sha256.Sum256([]byte(c.AppKey + ts + c.MasterSecret))
	body, _ := json.Marshal(map[string]string{
		"sign":      hex.EncodeToString(sum[:]),
		"timestamp": ts,
		"appkey":    c.AppKey,
	})
	data, err := gtPost(ctx, c, "/auth", body, "")
	if err != nil {
		// 换了 appId 的旧 token 作废
		gtToken = ""
		return "", err
	}
	var d struct {
		Token      string `json:"token"`
		ExpireTime string `json:"expire_time"` // 毫秒时间戳（字符串）
	}
	if err := json.Unmarshal(data, &d); err != nil || d.Token == "" {
		return "", fmt.Errorf("getui auth bad data: %s", string(data))
	}
	gtToken = d.Token
	gtTokenAppID = c.AppID
	exp := time.Now().Add(2 * time.Hour) // 官方默认 2h；解析失败时保守取 2h-5min
	if ms, err := strconv.ParseInt(d.ExpireTime, 10, 64); err == nil && ms > 0 {
		exp = time.UnixMilli(ms)
	}
	gtTokenExp = exp.Add(-5 * time.Minute)
	return gtToken, nil
}

// GetuiBindAlias 把 client id 绑定到用户别名（alias = 用户 ID 字符串，与极光约定一致）。
// 官方契约（2026-09-21 实测确认，源自官方 Java SDK UserApi.java）：
// POST /user/alias，body {"data_list":[{"cid":..,"alias":..}]}。
// 注意：早期实现的 POST /cid/alias/{cid} 路径不存在（404 not found）。
func GetuiBindAlias(ctx context.Context, c *GetuiConfig, cid string, uid int64) error {
	token, err := gtAuthToken(ctx, c)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]interface{}{
		"data_list": []map[string]string{{"cid": cid, "alias": fmt.Sprintf("%d", uid)}},
	})
	_, err = gtPost(ctx, c, "/user/alias", body, token)
	return err
}

// GetuiUnbindAlias 解绑（退出登录时调用，避免注销后仍收到该账号的个推通知）。
// 官方契约：DELETE /user/alias + 同款 data_list body（batchUnbindAlias）。
func GetuiUnbindAlias(ctx context.Context, c *GetuiConfig, cid string, uid int64) error {
	token, err := gtAuthToken(ctx, c)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]interface{}{
		"data_list": []map[string]string{{"cid": cid, "alias": fmt.Sprintf("%d", uid)}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		gtBaseURL+"/"+c.AppID+"/user/alias", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json;charset=utf-8")
	req.Header.Set("token", token)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var r struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal(raw, &r)
	if r.Code != 0 {
		return fmt.Errorf("getui unbind error %d: %s", r.Code, r.Msg)
	}
	return nil
}

// GetuiPushToAliases 按别名推送（逐别名调 /push/single/alias）。
// 契约（2026-09-21 实测确认）：POST /push/single/alias，audience={"alias":[单个别名]}，
// 返回 data={taskid:{cid:"successed_online|successed_offline|failed_..."}}。
// 注意：旧实现的 POST /push/alias 路径不存在（404 not found）；
// /push/list/alias 是两步式批量（需先建 taskid），收件人少时逐个单推更简单可靠。
// callInvite=true 时 iOS aps 带 category/interruption-level（与极光侧来电样式对齐）。
func GetuiPushToAliases(ctx context.Context, c *GetuiConfig, aliases []string, title, alert string, extras map[string]string, callInvite bool) error {
	token, err := gtAuthToken(ctx, c)
	if err != nil {
		return err
	}
	// 自定义键（extras）：iOS 侧放在 payload（JSON 字符串，个推原样透传给 APNs
	// payload 根层）；Android 侧 notification.click_type=payload 携带同一份 JSON。
	extrasJSON, _ := json.Marshal(extras)

	aps := map[string]interface{}{
		"alert":             map[string]string{"title": title, "body": alert},
		"sound":             "default",
		"content-available": 0,
	}
	if callInvite {
		aps["category"] = "call"
		aps["interruption-level"] = "time-sensitive"
	}
	ios := map[string]interface{}{
		"type":       "notify",
		"aps":        aps,
		"auto_badge": "+1",
		"payload":    string(extrasJSON),
	}
	// push_message.notification：Android 厂商通道兜底。当前 App 仅 iOS 走个推
	//（Android 固定极光），此字段正常不命中，保留以兼容后续扩展。
	notif := map[string]interface{}{
		"title":      title,
		"body":       alert,
		"click_type": "payload",
		"payload":    string(extrasJSON),
	}

	var lastErr error
	ok := 0
	for _, alias := range aliases {
		// request_id 10~32 位；settings.ttl 离线保留 1 天（对齐极光 time_to_live）
		body := map[string]interface{}{
			"request_id":   fmt.Sprintf("%d", time.Now().UnixNano()),
			"settings":     map[string]interface{}{"ttl": 86400000},
			"audience":     map[string]interface{}{"alias": []string{alias}},
			"push_message": map[string]interface{}{"notification": notif},
			"push_channel": map[string]interface{}{"ios": ios},
		}
		b, _ := json.Marshal(body)
		data, err := gtPost(ctx, c, "/push/single/alias", b, token)
		if err != nil {
			log.Printf("[getui] push failed alias=%s: %v", alias, err)
			lastErr = err
			continue
		}
		ok++
		// data 形如 {"taskid":{"cid":"successed_offline"}}：taskid 可去个推后台
		// 「推送监测」查 APNs 通道明细；status 能直接看出在线/离线送达与失败码
		var statuses map[string]map[string]string
		_ = json.Unmarshal(data, &statuses)
		log.Printf("[getui] push accepted alias=%s statuses=%v", alias, statuses)
	}
	if ok == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}

// dispatchOfflinePush 离线推送分平台路由（PushMessageOffline 的唯一出口）。
//   - provider=getui 且个推 ready：设备表里有 iOS 设备的接收者 → 个推；
//     其余接收者 → 极光（platform 限 ["android"]，iOS 不会被极光重复推）。
//   - 其它情况（provider=jpush 或个推未配置）：全量走极光，行为与旧版一致。
//
// 各分腿的结果在内部记日志（成功/失败都要留痕，排障靠 grep [getui]/[jpush]）。
func dispatchOfflinePush(ctx context.Context, aliases []string, title, alert string, extras map[string]string, callInvite bool) {
	gc := GetGetuiConfig(ctx)
	jc := GetJPushConfig(ctx)
	gtReady := gc.ready()
	jpushReady := jc.Enabled && jc.AppKey != "" && jc.MasterSecret != ""

	if iOSPushProvider(ctx) == "getui" && gtReady {
		iosSet, otherSet := splitAliasesByIOS(ctx, aliases)
		log.Printf("[push] route provider=getui msg aliases=%v ios=%v other=%v", aliases, iosSet, otherSet)
		if len(iosSet) > 0 {
			if err := GetuiPushToAliases(ctx, gc, iosSet, title, alert, extras, callInvite); err != nil {
				log.Printf("[getui] push failed (aliases=%v): %v", iosSet, err)
			} else {
				log.Printf("[getui] pushed aliases=%v alert=%q", iosSet, alert)
			}
		}
		if len(otherSet) > 0 {
			if !jpushReady {
				log.Printf("[jpush] skip android leg: not configured (aliases=%v)", otherSet)
			} else if err := jpushToAliases(ctx, jc, otherSet, title, alert, extras, callInvite, true); err != nil {
				log.Printf("[jpush] push failed (android-only, aliases=%v): %v", otherSet, err)
			} else {
				log.Printf("[jpush] pushed android-only aliases=%v alert=%q", otherSet, alert)
			}
		}
		return
	}

	// 极光全量（旧路径）
	if !jpushReady {
		log.Printf("[push] skip: jpush not configured and getui inactive")
		return
	}
	if err := jpushToAliases(ctx, jc, aliases, title, alert, extras, callInvite, false); err != nil {
		log.Printf("[jpush] push failed: %v", err)
	} else {
		log.Printf("[jpush] pushed aliases=%v alert=%q", aliases, alert)
	}
}

// splitAliasesByIOS 查设备表，把接收者分成「有 iOS 设备」与「其余」两批。
// device_type=2 为 iOS；同一用户 iOS + Android 设备并存时两边各收一份
// （个推收 iOS、极光 android-only 收 Android，不重复）。
func splitAliasesByIOS(ctx context.Context, aliases []string) (ios, other []string) {
	uids := make([]int64, 0, len(aliases))
	for _, a := range aliases {
		if id, err := strconv.ParseInt(a, 10, 64); err == nil {
			uids = append(uids, id)
		}
	}
	if len(uids) == 0 {
		return nil, aliases
	}
	type devRow struct {
		UserID     int64
		DeviceType int
	}
	var rows []devRow
	if err := store.DB.Model(&model.Device{}).
		Select("user_id, device_type").
		Where("user_id IN ?", uids).
		Find(&rows).Error; err != nil {
		log.Printf("[push] splitByIOS query failed: %v (fallback all->jpush)", err)
		return nil, aliases
	}
	hasIOS := make(map[int64]bool, len(rows))
	for _, r := range rows {
		if r.DeviceType == 2 {
			hasIOS[r.UserID] = true
		}
	}
	for _, a := range aliases {
		if id, err := strconv.ParseInt(a, 10, 64); err == nil && hasIOS[id] {
			ios = append(ios, a)
		} else {
			other = append(other, a)
		}
	}
	return ios, other
}

// SavePushToken 上报推送 token（个推 client id）：存 device.push_token + 绑定别名。
// unbind=true 时反向解绑（退出登录），并清空 push_token。
func SavePushToken(ctx context.Context, uid int64, deviceID, provider, token string, unbind bool) error {
	if uid == 0 {
		return fmt.Errorf("未登录")
	}
	if deviceID != "" {
		updates := map[string]interface{}{}
		if unbind {
			updates["push_token"] = ""
		} else if token != "" {
			updates["push_token"] = token
		}
		if len(updates) > 0 {
			if err := store.DB.Model(&model.Device{}).
				Where("user_id = ? AND device_id = ?", uid, deviceID).
				UpdateColumns(updates).Error; err != nil {
				log.Printf("[push] save push_token failed: uid=%d device=%s err=%v", uid, deviceID, err)
			}
		}
	}
	gc := GetGetuiConfig(ctx)
	if !gc.ready() {
		if unbind {
			return nil // 个推未启用：解绑视为成功（本地已清 token）
		}
		return fmt.Errorf("个推未启用或配置不完整，token 已存库但未绑定别名")
	}
	cid := token
	if deviceID != "" && cid == "" {
		// 解绑场景：token 可能没传，从库里找该设备已存的 cid
		var dev model.Device
		if err := store.DB.Where("user_id = ? AND device_id = ?", uid, deviceID).
			First(&dev).Error; err == nil {
			cid = dev.PushToken
		}
	}
	if cid == "" {
		return fmt.Errorf("client id 为空")
	}
	if unbind {
		if err := GetuiUnbindAlias(ctx, gc, cid, uid); err != nil {
			return err
		}
		log.Printf("[getui] alias unbound uid=%d cid=%s", uid, cid)
		return nil
	}
	if err := GetuiBindAlias(ctx, gc, cid, uid); err != nil {
		return err
	}
	// 成功也记日志：App 端诊断有时序覆盖问题，服务端日志是绑定是否成功的权威证据
	log.Printf("[getui] alias bound uid=%d cid=%s device=%s", uid, cid, deviceID)
	return nil
}
