package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"
)

// ============ 登录设备批准（账户安全验证） ============
// 详见 doc/ACCOUNT_VERIFY_DESIGN.md。
// 密码校验通过后、发 token 前创建一次性验证会话（Redis Hash verify:{ticket}，TTL 5min），
// 按端类型 + 配置 + 是否有最近可信设备决定 method：code（小助手发码）/ approve（最近设备批准）。

const verifyTTL = 5 * time.Minute

// 验证码有效期（与验证会话 TTL 对齐，独立计时便于后续单独调整）。
const verifyCodeTTL = 5 * time.Minute

// 一台设备一天最多「重发」验证码 5 次（首次发送在 CreateVerifySession，不计入重发）。
const maxVerifyCodeResendPerDay = 5

func verifyKey(ticket string) string { return "verify:" + ticket }

// gen6Code 生成 6 位数字验证码
func gen6Code() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%06d", time.Now().Nanosecond()%1000000) // 退化
	}
	for i := range b {
		b[i] = byte('0' + b[i]%10)
	}
	return string(b)
}

func verifyLoadUser(ctx context.Context, uid int64) (*model.User, error) {
	var u model.User
	if err := store.DB.First(&u, uid).Error; err != nil {
		return nil, errs.NotFound
	}
	return &u, nil
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// CreateVerifySession 创建登录验证会话。
// method=code：生成 6 位码并经小助手发给用户；method=approve：向审批设备推 login.approve 事件。
func CreateVerifySession(ctx context.Context, uid int64, req *LoginReq, ip, method, approveDevice string) (string, string, error) {
	ticket := randToken(32)
	code := ""
	codeExpireAt := int64(0)
	if method == "code" {
		code = gen6Code()
		codeExpireAt = time.Now().Add(verifyCodeTTL).Unix()
	}
	fields := map[string]interface{}{
		"uid":            uid,
		"method":         method,
		"device_id":      req.DeviceID,
		"device_type":    req.DeviceType,
		"device_name":    req.DeviceName,
		"ip":             ip,
		"status":         "pending",
		"approve_device": approveDevice,
		"code":           code,
		"code_expire_at": codeExpireAt,
		"fail":           0,
		"created_at":     time.Now().Unix(),
	}
	if store.RDB != nil {
		if err := store.RDB.HSet(ctx, verifyKey(ticket), fields).Err(); err != nil {
			return "", "", err
		}
		store.RDB.Expire(ctx, verifyKey(ticket), verifyTTL)
	}
	if method == "code" {
		_ = AssistantNotify(ctx, uid, fmt.Sprintf(
			"【登录验证码】您的账号正在新设备（%s，IP %s）登录，验证码：%s（5分钟内有效）。若非本人操作请忽略并修改密码。",
			deviceName(req.DeviceType), ip, code))
	} else if method == "approve" && approveDevice != "" {
		b, _ := json.Marshal(map[string]interface{}{
			"verifyTicket":  ticket,
			"deviceName":    req.DeviceName,
			"deviceType":    req.DeviceType,
			"ip":            ip,
			"time":          time.Now().Format("2006-01-02 15:04:05"),
			"approveDevice": approveDevice, // 审批设备号：事件广播给 uid 全部设备，客户端据此自过滤（非审批设备不弹窗）
		})
		_ = PublishEvent(ctx, &Event{Type: "login.approve", ToUIDs: []int64{uid}, Data: b})
	}
	return ticket, code, nil
}

// VerifyByCode 验证码方式校验：正确则发 token 并通知，错误限 5 次。
func VerifyByCode(ctx context.Context, cfg *config.Config, ticket, inputCode string) (*LoginResult, error) {
	if store.RDB == nil {
		return nil, errs.Internal
	}
	key := verifyKey(ticket)
	m, err := store.RDB.HGetAll(ctx, key).Result()
	if err != nil || len(m) == 0 || m["status"] != "pending" {
		return nil, errs.VerifyExpired
	}
	// 验证码时效校验：5 分钟内有效，过期则要求重新获取（与小助手发码提示一致）。
	if exp := int64(atoiSafe(m["code_expire_at"])); exp > 0 && time.Now().Unix() > exp {
		return nil, errs.VerifyCodeExpired
	}
	if m["code"] != inputCode {
		fail := atoiSafe(m["fail"]) + 1
		if fail >= 5 {
			store.RDB.HSet(ctx, key, "status", "rejected", "fail", fail)
			return nil, errs.VerifyTooMany
		}
		store.RDB.HSet(ctx, key, "fail", fail)
		return nil, errs.VerifyCodeWrong
	}
	uid := int64(atoiSafe(m["uid"]))
	u, err := verifyLoadUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	access, refresh, err := issueTokens(ctx, cfg, u, m["device_id"], atoiSafe(m["device_type"]), m["device_name"])
	if err != nil {
		return nil, err
	}
	store.RDB.HSet(ctx, key, "status", "approved")
	notifyLogin(ctx, cfg, uid, m["device_name"], m["ip"], "")
	return &LoginResult{User: u, AccessToken: access, RefreshToken: refresh}, nil
}

// ApproveLogin 设备批准：审批方允许则发 token 存入会话（供新设备轮询），拒绝则标记 rejected。
func ApproveLogin(ctx context.Context, cfg *config.Config, ticket, decision, approverDevice string) error {
	if store.RDB == nil {
		return errs.Internal
	}
	key := verifyKey(ticket)
	m, err := store.RDB.HGetAll(ctx, key).Result()
	if err != nil || len(m) == 0 || m["status"] != "pending" {
		return errs.VerifyExpired
	}
	// 审批方必须是被推送批准的设备（命中 approve_device）
	if m["approve_device"] != approverDevice {
		return errs.VerifyNotApprover
	}
	if decision == "reject" {
		store.RDB.HSet(ctx, key, "status", "rejected")
		return nil
	}
	uid := int64(atoiSafe(m["uid"]))
	u, err := verifyLoadUser(ctx, uid)
	if err != nil {
		return err
	}
	access, refresh, err := issueTokens(ctx, cfg, u, m["device_id"], atoiSafe(m["device_type"]), m["device_name"])
	if err != nil {
		return err
	}
	store.RDB.HSet(ctx, key, "status", "approved", "access", access, "refresh", refresh)
	notifyLogin(ctx, cfg, uid, m["device_name"], m["ip"], approverDevice)
	return nil
}

// VerifyPoll 新设备轮询验证结果（新设备尚未登录、无 token，靠轮询拿 token）。
func VerifyPoll(ctx context.Context, ticket string) (*LoginResult, error) {
	if store.RDB == nil {
		return nil, errs.Internal
	}
	key := verifyKey(ticket)
	m, err := store.RDB.HGetAll(ctx, key).Result()
	if err != nil || len(m) == 0 {
		return nil, errs.VerifyExpired
	}
	switch m["status"] {
	case "pending":
		return &LoginResult{VerifyRequired: true}, nil
	case "approved":
		uid := int64(atoiSafe(m["uid"]))
		u, err := verifyLoadUser(ctx, uid)
		if err != nil {
			return nil, err
		}
		return &LoginResult{User: u, AccessToken: m["access"], RefreshToken: m["refresh"]}, nil
	case "rejected":
		return nil, errs.VerifyRejected
	default:
		return nil, errs.VerifyExpired
	}
}

// VerifySwitch 切换验证方式（新设备侧）：同一 verify 会话内在 code/approve 间切换。
// code：需小助手开启，重新生成验证码并经小助手发送给用户；approve：需存在最近可信设备，重新推送 login.approve 事件。
// 用于客户端「等待其他设备批准 / 改用验证码」互切（小助手开启时才有验证码可切）。
func VerifySwitch(ctx context.Context, cfg *config.Config, ticket, method string) error {
	if store.RDB == nil {
		return errs.Internal
	}
	if method != "code" && method != "approve" {
		return errs.ParamError
	}
	key := verifyKey(ticket)
	m, err := store.RDB.HGetAll(ctx, key).Result()
	if err != nil || len(m) == 0 || m["status"] != "pending" {
		return errs.VerifyExpired
	}
	uid := int64(atoiSafe(m["uid"]))
	ip := m["ip"]
	devType := atoiSafe(m["device_type"])
	devName := m["device_name"]
	devID := m["device_id"]
	if method == "code" {
		if !boolVal(SysConfigGet(ctx, "assistant_enabled", true)) {
			return errs.VerifyAssistantOff
		}
		// 一台设备一天最多重发 5 次（首次发送在 CreateVerifySession，不计入）。
		if devID != "" {
			rk := "verify:resend:" + devID + ":" + time.Now().Format("20060102")
			n, _ := store.RDB.Incr(ctx, rk).Result()
			if n == 1 {
				store.RDB.Expire(ctx, rk, 24*time.Hour)
			}
			if n > int64(maxVerifyCodeResendPerDay) {
				store.RDB.Decr(ctx, rk)
				return errs.VerifyResendLimit
			}
		}
		code := gen6Code()
		store.RDB.HSet(ctx, key, "method", "code", "code", code,
			"code_expire_at", time.Now().Add(verifyCodeTTL).Unix(),
			"fail", 0, "status", "pending")
		_ = AssistantNotify(ctx, uid, fmt.Sprintf(
			"【登录验证码】您的账号正在新设备（%s，IP %s）登录，验证码：%s（5分钟内有效）。若非本人操作请忽略并修改密码。",
			deviceName(devType), ip, code))
		return nil
	}
	// method == approve
	R := RecentDevice(ctx, uid, devID, true)
	if R == nil {
		return errs.VerifyNoApprover
	}
	b, _ := json.Marshal(map[string]interface{}{
		"verifyTicket":  ticket,
		"deviceName":    devName,
		"deviceType":    devType,
		"ip":            ip,
		"time":          time.Now().Format("2006-01-02 15:04:05"),
		"approveDevice": R.DeviceID, // 与 CreateVerifySession 同：客户端自过滤
	})
	store.RDB.HSet(ctx, key, "method", "approve", "code", "", "approve_device", R.DeviceID, "status", "pending")
	_ = PublishEvent(ctx, &Event{Type: "login.approve", ToUIDs: []int64{uid}, Data: b})
	return nil
}

// notifyLogin 登录安全通知（小助手开启时）：任何设备登录成功都发一条提醒（见设计 §3.2）。
// 同设备 5min 去重，避免 token 刷新 / 重连导致的通知轰炸。
func notifyLogin(ctx context.Context, cfg *config.Config, uid int64, devName, ip, approvedBy string) {
	if !boolVal(SysConfigGet(ctx, "assistant_enabled", true)) {
		return
	}
	key := "loginnotify:" + strconv.FormatInt(uid, 10) + ":" + devName
	if store.RDB != nil {
		if ok, _ := store.RDB.SetNX(ctx, key, 1, 5*time.Minute).Result(); !ok {
			return // 5min 内同设备已通知，去重
		}
	}
	ts := time.Now().Format("01-02 15:04")
	txt := fmt.Sprintf("【登录提醒】您的账号于 %s 在 %s（IP %s）登录成功。", ts, devName, ip)
	if approvedBy != "" {
		txt += fmt.Sprintf("（已由设备 %s 批准）", approvedBy)
	}
	_ = AssistantNotify(ctx, uid, txt+" 若非本人操作请立即修改密码。")
}
