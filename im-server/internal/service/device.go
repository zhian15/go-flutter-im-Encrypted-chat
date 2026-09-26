package service

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"

	"github.com/redis/go-redis/v9"
)

// ============ 设备会话（活跃会话 / 注销设备） ============
//
// 数据源分工（两套记录语义不同，缺一不可）：
//   - MySQL `device` 表（migrations/002_auth.sql）→ 列表内容：device_id / 类型 / 最后活跃 / IP；
//     last_active_at、last_ip 在 **WS 建连与心跳续期时更新**（见 ws.go handleWS），
//     登录/刷新 token 时也会写（auth.go issueTokens）。
//   - Redis Hash `refresh:{uid}`（字段=设备槽位）→ 该设备登录态是否仍有效（有槽位=已登录）。
//     列表只返回槽位仍存在的设备 = 「正在登录的设备」。
//   - Redis Hash `onlinedev:{uid}`（字段=deviceId，值=平台名，TTL 90s 心跳续期）→ WS 在线状态。
//
// 鉴权残留风险（本阶段已知并接受）：Logout 只吊销 refresh 槽位 + 断开 WS，
// 不使 access token 失效（JWT 无状态，middleware.Auth 只校验 user.status + token_version），
// 被注销设备在 access token 过期前仍可调用 HTTP API。设备级 token 失效（JWT device_id
// claim + 中间件复核槽位）留二期。

// onlineDevKey WS 在线设备表：Hash `onlinedev:{uid}`，字段=deviceId，值=平台名
func onlineDevKey(uid int64) string { return "onlinedev:" + strconv.FormatInt(uid, 10) }

// DeviceSession 活跃会话列表项
type DeviceSession struct {
	DeviceID     string     `json:"deviceId"`     // 客户端持久化设备号
	DeviceType   int        `json:"deviceType"`   // 1 Android 2 iOS 3 Web 4 Windows 5 macOS
	DeviceName   string     `json:"deviceName"`   // 客户端上报的设备型号（Xiaomi 2201123G / 手机网页 / Windows PC）
	Platform     string     `json:"platform"`     // android/ios/web/windows/macos
	LastActiveAt *time.Time `json:"lastActiveAt"` // 最后活跃（WS 建连/心跳/登录刷新时更新）
	LastIP       string     `json:"lastIp"`       // 最后活跃出口 IP（WS 建连时捕获）
	Online       bool       `json:"online"`       // WS 是否在线（onlinedev 90s 内有心跳）
	Current      bool       `json:"current"`      // 是否当前发起请求的设备（X-Device-ID 匹配）——旧字段，兼容已上线客户端
	IsCurrent    bool       `json:"isCurrent"`    // 同 Current（新命名，见 doc/API.md 设备会话节）
	CreatedAt    time.Time  `json:"createdAt"`    // 首次登录时间
	Trusted      bool       `json:"trusted"`      // 设备信任标记（登录设备批准）：true=已信任免二次验证
}

// DeviceSessions 活跃会话列表：只返回 refresh 槽位仍存在的设备（= 正在登录的设备）。
// **返回全部已登录设备，包含发起请求的当前设备**（isCurrent 标识本机，前端据此挂
// 「本机」徽标且不显示注销按钮）——不做排除，排除会让本机在列表里消失。
// currentDeviceID 来自请求头 X-Device-ID；为空时按最近活跃行兜底标记（见 applyCurrent）。
func DeviceSessions(ctx context.Context, userID int64, currentDeviceID string) ([]DeviceSession, error) {
	var devs []model.Device
	if err := store.DB.Where("user_id = ?", userID).
		Order("last_active_at DESC, created_at DESC").Find(&devs).Error; err != nil {
		return nil, err
	}
	// refresh 槽位集合（Hash refresh:{uid} 的字段 = 设备号；空设备号槽位为 "default"）
	slotSet := make(map[string]struct{})
	if store.RDB != nil {
		slots, err := store.RDB.HKeys(ctx, refreshKey(userID)).Result()
		if err == nil {
			for _, s := range slots {
				slotSet[s] = struct{}{}
			}
		}
	}
	// WS 在线设备（deviceId → platform）
	onlineSet := make(map[string]struct{})
	if store.RDB != nil {
		m, err := store.RDB.HGetAll(ctx, onlineDevKey(userID)).Result()
		if err == nil {
			for id := range m {
				onlineSet[id] = struct{}{}
			}
		}
	}
	// 平台级在线集合（Set online:{uid}，成员=平台名字符串，每条 WS 连接无条件写入，
	// 见 ws.go markOnline）。设备级 onlinedev:{uid} 只有带 deviceId 的连接才写：
	// 老客户端 / 未带 deviceId 的连接（此前 PC 端 WS 即如此）两边粒度对不上，
	// 设备行会被误判离线。这里作为兜底：设备号不在 onlinedev 时，按其平台名
	// 是否在集合中判定在线（同平台任一连接存活即视为在线）。
	platformOnline := make(map[string]struct{})
	if store.RDB != nil {
		if members, err := store.RDB.SMembers(ctx, onlineUIDKey(userID)).Result(); err == nil {
			for _, p := range members {
				platformOnline[p] = struct{}{}
			}
		}
	}
	out := make([]DeviceSession, 0, len(devs))
	for _, d := range devs {
		if _, ok := slotSet[refreshSlot(d.DeviceID)]; !ok {
			continue // 登录态已被吊销（改密/注销/被踢）→ 不在「活跃会话」中显示
		}
		platform := deviceName(d.DeviceType)
		out = append(out, DeviceSession{
			DeviceID:     d.DeviceID,
			DeviceType:   d.DeviceType,
			DeviceName:   d.DeviceName,
			Platform:     platform,
			LastActiveAt: &d.LastActiveAt,
			LastIP:       d.LastIP,
			Online:       resolveDeviceOnline(d.DeviceID, platform, onlineSet, platformOnline),
			Trusted:      d.Trusted,
			CreatedAt:    d.CreatedAt,
		})
	}
	applyCurrent(out, currentDeviceID)
	return out, nil
}

// resolveDeviceOnline 单台设备的在线判定（两级联动，纯函数便于无 DB 单测）：
//  1. 设备级 onlinedev:{uid} 有该 deviceId（新客户端 WS 带 deviceId）→ 在线；
//  2. 回退平台级 online:{uid}：设备号不在 onlinedev 时，按其平台名（不区分大小写，
//     markOnline 写入的是小写，见 ws.go deviceNameOf）是否在集合中判定——同平台任一
//     连接存活即视为在线（老客户端 / 未带 deviceId 的连接只有平台级记录）；
//  3. 都没有 → 离线。
func resolveDeviceOnline(deviceID, platform string, deviceOnline, platformOnline map[string]struct{}) bool {
	if _, ok := deviceOnline[deviceID]; ok {
		return true
	}
	_, ok := platformOnline[strings.ToLower(platform)]
	return ok
}

// applyCurrent 标记「本机」行（原位修改 list）：
//   - 请求头 X-Device-ID 非空：按 device_id 精确匹配（可能 0 命中——请求方设备
//     无槽位/无行时本就不在列表里，此时不应错标其它行，保持全空是正确语义）；
//   - 请求头为空：无法识别请求方设备（access token 的 JWT 无 device claim，
//     见 internal/pkg/jwt/jwt.go），兜底把**最近活跃的第一行**标为本机
//     （列表已按 lastActiveAt 倒序）——启发式，避免 App 设备页顶部「当前设备」
//     卡片因 isCurrent 全空而恒显示离线。
func applyCurrent(list []DeviceSession, currentDeviceID string) {
	if currentDeviceID == "" {
		if len(list) > 0 {
			list[0].Current, list[0].IsCurrent = true, true
		}
		return
	}
	for i := range list {
		if list[i].DeviceID == currentDeviceID {
			list[i].Current, list[i].IsCurrent = true, true
		}
	}
}

// RecentDevice 取用户最近活跃的可信设备（用于登录设备批准的审批方）。
//   - excludeDeviceID：排除当前发起登录的新设备（新设备本身不该批准自己）；
//   - onlyTrusted=true：仅从 trusted=1 设备中选，被手动注销（trusted=0）的设备
//     不会成为审批方（见设计方案 §3.1 规则 2：被注销设备不能批准他人）；
//   - 返回 nil 表示无可用审批方（首登/唯一设备/全部已被注销）。
func RecentDevice(ctx context.Context, userID int64, excludeDeviceID string, onlyTrusted bool) *DeviceSession {
	var devs []model.Device
	q := store.DB.Where("user_id = ?", userID)
	if onlyTrusted {
		// Web(3) 仅扫码登录、属临时会话，不当审批方（见设计 §6.3 / startVerify 注释②）。
		q = q.Where("trusted = ?", 1).Where("device_type <> ?", 3)
	}
	if excludeDeviceID != "" {
		q = q.Where("device_id <> ?", excludeDeviceID)
	}
	if err := q.Order("last_active_at DESC, created_at DESC").Limit(1).Find(&devs).Error; err != nil || len(devs) == 0 {
		return nil
	}
	d := devs[0]
	return &DeviceSession{
		DeviceID:   d.DeviceID,
		DeviceType: d.DeviceType,
		DeviceName: d.DeviceName,
		Platform:   deviceName(d.DeviceType),
		LastIP:     d.LastIP,
		Trusted:    d.Trusted,
		CreatedAt:  d.CreatedAt,
	}
}

// LogoutDevice 注销单台设备：
//  1. 吊销该设备 refresh 槽位（复用 auth.Logout → HDel refresh:{uid} 槽位）；
//  2. 发布 device.logout 事件 → gateway 先给该设备推 forceLogout 帧，再主动断开其连接
//     （ConnManager.CloseByDevice；单 gateway 节点约束下本地关闭即全量，见 ws.go shardOf 注释）；
//  3. 摘除 WS 在线标记 + 设备行置离线。
//
// 注意：不吊销 access token（见文件头注释的残留风险）。
func LogoutDevice(ctx context.Context, userID int64, deviceID string) error {
	if deviceID == "" {
		return errs.ParamError
	}
	// 该设备必须真的有登录态（槽位存在），否则视为未登录过 → 404 语义用 4001
	if _, ok := refreshSlotGet(ctx, userID, refreshSlot(deviceID)); !ok {
		return errs.ConvNotFound
	}
	// 1) 吊销 refresh 槽位
	if err := Logout(ctx, userID, deviceID); err != nil {
		return err
	}
	// 2) 通知 gateway 断连（api 进程没有 connMgr，连接在 gateway 进程）
	b, _ := json.Marshal(map[string]interface{}{"deviceId": deviceID})
	if err := PublishEvent(ctx, &Event{
		Type:   "device.logout",
		ToUIDs: []int64{userID},
		Data:   b,
	}); err != nil {
		// 发布失败不回滚吊销：槽位已删，设备最多撑到 access token 过期 / 下次刷新失败
		log.Printf("[device] publish device.logout failed uid=%d device=%s err=%v", userID, deviceID, err)
	}
	// 3) 在线标记 + 设备行
	if store.RDB != nil {
		store.RDB.HDel(ctx, onlineDevKey(userID), deviceID)
	}
	// 手动注销 → 清除信任标记（规则 1：下次登录需重新批准）；
	// 注意 trusted 列由 migrations/028 补，迁移前该列不存在，Update 报错仅记录不阻断。
	if err := store.DB.Model(&model.Device{}).
		Where("user_id = ? AND device_id = ?", userID, deviceID).
		Updates(map[string]interface{}{"status": 0, "trusted": 0}).Error; err != nil {
		log.Printf("[device] mark device offline/trusted failed uid=%d device=%s err=%v", userID, deviceID, err)
	}
	return nil
}

// DeviceHasSlot 设备级令牌失效（二期落地，2026-09-24）：判断某设备的 refresh 槽位是否仍存在。
// 已登录设备槽位存在；被注销 / 单设备登出 / 改密码全量登出后槽位被删 → 返回 false，
// 据此 middleware.Auth 与 WS 握手续拒该设备的请求，被注销设备无法凭旧 access token 续命。
//   - deviceID 为空（老 token 无 did 字段）→ 按"存在"处理，放行兼容存量会话自然过期；
//   - Redis 不可用 → fail-open 返回 true，避免一次抖动把全体在线用户踢下线。
func DeviceHasSlot(ctx context.Context, uid int64, deviceID string) bool {
	if deviceID == "" {
		return true
	}
	if store.RDB == nil {
		return true
	}
	_, ok := refreshSlotGet(ctx, uid, refreshSlot(deviceID))
	return ok
}

// ============ 在线统计（群顶栏「N 成员，M 在线」） ============

// onlineUIDKey 旧平台名在线集合：Set `online:{uid}`（ws.go markOnline 每条 WS 连接
// 无条件写入，无论客户端是否带 deviceId；90s 心跳 TTL）。
// 在线判定用它而非 onlinedev:{uid}：后者只有带 deviceId 的新连接才写，
// 老客户端连接会漏统计。
func onlineUIDKey(uid int64) string { return "online:" + strconv.FormatInt(uid, 10) }

// OnlineCount 统计 uid 列表中的在线人数（Redis pipeline 批量 EXISTS，一次 RTT）。
func OnlineCount(ctx context.Context, uids []int64) int {
	if len(uids) == 0 || store.RDB == nil {
		return 0
	}
	pipe := store.RDB.Pipeline()
	cmds := make([]*redis.IntCmd, 0, len(uids))
	for _, uid := range uids {
		cmds = append(cmds, pipe.Exists(ctx, onlineUIDKey(uid)))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[online] count failed n=%d err=%v", len(uids), err)
		return 0
	}
	n := 0
	for _, c := range cmds {
		if v, err := c.Result(); err == nil && v > 0 {
			n++
		}
	}
	return n
}

// ConvOnlineCount 会话在线成员数（成员列表走 convMemberIDs 缓存；用于群顶栏）。
func ConvOnlineCount(ctx context.Context, convID int64) int {
	return OnlineCount(ctx, convMemberIDs(ctx, convID))
}
