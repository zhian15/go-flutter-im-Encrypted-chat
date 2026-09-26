package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/captcha"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/pkg/id"
	jwtx "github.com/yourcompany/im-server/internal/pkg/jwt"
	"github.com/yourcompany/im-server/internal/store"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	authModeNone  = "none"
	authModeSMS   = "sms"
	authModeEmail = "email"
)

// 注册方式（2026-09-22 需求6，sys_config register_type）：决定注册表单填什么
const (
	regTypeAccount = "account" // 账号密码（用户名/邮箱/手机号均可，历史行为）
	regTypePhone   = "phone"   // 手机号注册（默认区号 +86）
	regTypeEmail   = "email"   // 邮箱注册（必须填邮箱）
)

// RegisterModeGet 注册方式与认证开关（2026-09-22 需求6）。
//   - sys_config register_type：account（默认）/ phone / email；
//   - sys_config register_verify_enabled：默认 false。开启后**仅手机/邮箱注册**
//     需要短信/邮箱验证码，账号密码注册即使开启也不需要（需求原文口径）。
//
// 存量兼容：register_type 未配置时从 auth_mode 推导——sms → phone+强制认证、
// email → email+强制认证、none → account。auth_mode=sms/email 的存量部署
// 升级后验证码要求与账号收紧行为完全不变。
func RegisterModeGet(ctx context.Context, cfg *config.Config) (regType string, verifyOn bool) {
	verifyOn = boolVal(SysConfigGet(ctx, "register_verify_enabled", false))
	regType = strVal(SysConfigGet(ctx, "register_type", ""))
	if regType == "" {
		// 新键未配置 → 老键 auth_mode 推导（存量部署行为不变）
		switch strVal(SysConfigGet(ctx, "auth_mode", cfg.AuthMode)) {
		case authModeSMS:
			regType, verifyOn = regTypePhone, true
		case authModeEmail:
			regType, verifyOn = regTypeEmail, true
		default:
			regType = regTypeAccount
		}
	}
	return
}

// ============ 管理员初始化 ============

// EnsureAdmin 启动时根据环境变量创建第一个管理员（幂等）
func EnsureAdmin(cfg *config.Config) error {
	if cfg.AdminInitUser == "" {
		return nil
	}
	var u model.User
	if err := store.DB.Where("account = ?", cfg.AdminInitUser).First(&u).Error; err == nil {
		return nil // 已存在
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminInitPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	admin := model.User{
		ID:           id.Next(),
		Account:      cfg.AdminInitUser,
		PasswordHash: string(hash),
		Nickname:     "系统管理员",
		CountryCode:  "+86",
		Status:       model.StatusNormal,
		Role:         model.RoleAdmin,
	}
	if err := store.DB.Create(&admin).Error; err != nil {
		return err
	}
	log.Printf("admin account initialized: %s", cfg.AdminInitUser)
	return nil
}

// ============ 注册 ============

type RegisterReq struct {
	Account      string `json:"account" binding:"required"`  // 手机号或邮箱
	Password     string `json:"password" binding:"required"` // 6-20 位
	Nickname     string `json:"nickname"`
	CountryCode  string `json:"countryCode"`
	DepartmentID int64  `json:"departmentId"`
	Code         string `json:"code"`       // 短信/邮箱验证码
	InviteCode   string `json:"inviteCode"` // 邀请码（开关开启时必填）
	CaptchaID    string `json:"captchaId"`  // 图形验证码 ID（防刷）
	CaptchaCode  string `json:"captchaCode"`
	Channel      string `json:"channel"` // sms / email / ""（为空时按 AUTH_MODE 决定）
	DeviceID     string `json:"deviceId"`
	DeviceType   int    `json:"deviceType"`
	DeviceName   string `json:"deviceName"` // 客户端上报的设备型号（手机厂商型号 / 手机网页 / Windows PC）
	// snake_case 别名：PC 端（im-pc）登录/注册发的是 device_id / device_type，
	// 只认 camelCase 会让 PC 登录拿空设备号 → device 表没有本机行 → 设备列表缺本机。
	DeviceIDAlias   string `json:"device_id"`
	DeviceTypeAlias int    `json:"device_type"`
}

// NormalizeDevice 别名归一：camelCase 缺失时回退 snake_case；设备类型缺省按 Web（与 WS
// 握手 deviceType 为空默认 3 的行为一致，见 ws.go handleWS）。导出供 handler 在参数校验前调用。
func NormalizeDevice(deviceID string, deviceType int, aliasID string, aliasType int) (string, int) {
	if deviceID == "" {
		deviceID = aliasID
	}
	if deviceType == 0 {
		deviceType = aliasType
	}
	if deviceID != "" && deviceType == 0 {
		deviceType = 3 // Web
	}
	return deviceID, deviceType
}

// Register 注册（按认证模式校验验证码 / 邀请码 / 注册开关）
// clientIP：注册请求来源 IP（handler 传 c.ClientIP()），落 user.register_ip 供后台审计
func Register(ctx context.Context, cfg *config.Config, req *RegisterReq, clientIP string) (*model.User, string, string, error) {
	req.DeviceID, req.DeviceType = NormalizeDevice(req.DeviceID, req.DeviceType, req.DeviceIDAlias, req.DeviceTypeAlias)
	// 1. 注册开关（后台 sys_config.register_enabled 优先，环境变量 REGISTER_ON 仅回落）
	if !boolVal(SysConfigGet(ctx, "register_enabled", cfg.RegisterOn)) {
		return nil, "", "", errs.RegisterOff
	}
	// 2. 图形验证码（后台配置 captcha_enabled=true 才校验；默认关闭——前端已不再收集）
	if needCaptcha(ctx, cfg) {
		if err := verifyCaptcha(ctx, req.CaptchaID, req.CaptchaCode); err != nil {
			return nil, "", "", err
		}
	}
	// 3. 注册方式与账号格式校验（2026-09-22 需求6）：
	//    register_type=phone → 必须手机号；email → 必须邮箱；account（默认）→
	//    邮箱 / 手机号 / 用户名三选一（历史行为）。
	regType, verifyOn := RegisterModeGet(ctx, cfg)
	account := strings.TrimSpace(req.Account)
	switch regType {
	case regTypePhone:
		if !isPhone(account) {
			return nil, "", "", &errs.Err{Code: 1001, Msg: "请输入正确的手机号"}
		}
	case regTypeEmail:
		if !isEmail(account) {
			return nil, "", "", &errs.Err{Code: 1001, Msg: "请输入正确的邮箱地址"}
		}
	default: // account
		if !isEmail(account) && !isPhone(account) && !isUsername(account) {
			return nil, "", "", &errs.Err{Code: 1001, Msg: "账号需为邮箱、手机号或用户名（3-20位，仅限英文、数字、下划线）"}
		}
	}
	// 4. 密码强度（6-20 位，不要求字母+数字组合）
	if !validPassword(req.Password) {
		return nil, "", "", &errs.Err{Code: 1001, Msg: "密码需为 6-20 位"}
	}
	// 5. 邀请码（后台配置 invite_code_enabled 开启时校验）
	inviteOn := cfg.InviteCodeOn
	if v := SysConfigGet(ctx, "invite_code_enabled", cfg.InviteCodeOn); v != nil {
		inviteOn = boolVal(v)
	}
	if inviteOn {
		if err := consumeInviteCode(ctx, req.InviteCode, account); err != nil {
			// 一次性邀请码无效 → 回退校验自定义好友邀请码（后台创建、多用不限次）
			if !InviteFriendCodeValid(ctx, req.InviteCode) {
				return nil, "", "", err
			}
		}
	}
	// 6. 验证码校验（2026-09-22 需求6 改造）：
	//    注册方式为手机 → 短信验证码；邮箱 → 邮箱验证码；**账号密码注册方式
	//    即使「开启注册认证」也不需要验证码**（需求原文口径）。
	//    「开启注册认证」= register_verify_enabled；register_type 未配置的存量
	//    部署由 RegisterModeGet 从 auth_mode 推导（sms/email 存量行为不变）。
	//    channel 由服务端按注册方式决定，客户端伪造无效。
	switch {
	case regType == regTypePhone && verifyOn:
		// 手机号格式已在第 3 步校验
		if err := verifyCode(ctx, "sms", account, req.Code); err != nil {
			return nil, "", "", err
		}
	case regType == regTypeEmail && verifyOn:
		if err := verifyCode(ctx, "email", account, req.Code); err != nil {
			return nil, "", "", err
		}
	default:
		// 账号密码注册方式 / 未开启注册认证：无需验证码
	}

	// 7. 唯一性
	var cnt int64
	store.DB.Model(&model.User{}).Where("account = ?", account).Count(&cnt)
	if cnt > 0 {
		return nil, "", "", errs.AccountExists
	}

	// 8. 创建用户
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", "", err
	}
	nickname := req.Nickname
	if nickname == "" {
		nickname = defaultNickname(account)
	}
	cc := req.CountryCode
	if cc == "" {
		cc = "+86"
	}
	u := model.User{
		ID:           id.Next(),
		Account:      account,
		PasswordHash: string(hash),
		Nickname:     nickname,
		CountryCode:  cc,
		DepartmentID: req.DepartmentID,
		Status:       model.StatusNormal,
		Role:         model.RoleUser,
		ShortID:      model.StrPtr(genShortID(ctx)),
		RegisterIP:   clientIP,
	}
	// 注册设备：设备类型名 + 设备号（后台用户详情展示）
	regDev := deviceName(req.DeviceType)
	if req.DeviceID != "" {
		regDev += " / " + req.DeviceID
	}
	u.RegisterDevice = regDev
	// 需求9：默认头像（后台可配置 default_avatar，新注册用户使用）
	if av, ok := SysConfigGet(ctx, "default_avatar", "").(string); ok && av != "" {
		u.Avatar = av
	}
	// 账号落位：邮箱 → email；手机号 → phone；**用户名 → 两个都不写**
	// （account 字段本身就保存了它）。
	// 旧代码是 if/else「非邮箱一律塞 phone」，用户名注册时会把用户名写成手机号，
	// 污染 phone 字段 —— 手机号搜索用户、后台用户详情、短信认证都会跟着出错。
	// 2026-09-17 二次修复 + 2026-09-22 需求6 调整：只有**走验证码注册**
	// （注册方式 phone/email 且「开启注册认证」——账号已被真实验证）才落
	// phone/email；账号密码注册一律不写，不管账号长什么样。
	switch {
	case regType == regTypeEmail && verifyOn && isEmail(account):
		u.Email = account
	case regType == regTypePhone && verifyOn && isPhone(account):
		u.Phone = account
	}
	// 注册时填写的邀请码 → 记录邀请关系：
	// invited_code 存原文（后台按码可搜到此人），invited_by 按码反查上级用户 ID（查不到为 0）。
	// u.ID 已由 id.Next() 生成，传给 excludeUID 防止填写自己的邀请码导致自邀请。
	if ic := strings.TrimSpace(req.InviteCode); ic != "" {
		u.InvitedCode = ic
		u.InvitedBy = resolveInviterByCode(ctx, ic, u.ID)
	}
	if err := store.DB.Create(&u).Error; err != nil {
		return nil, "", "", err
	}

	// 9. 注册成功自动添加小助手（后台开启时）
	if err := AssistantAddForUser(ctx, cfg, u.ID); err != nil {
		log.Printf("[assistant] auto add assistant for user %d failed: %v", u.ID, err)
	}

	// 9.1 注册成功按配置自动添加客服好友（失败不阻断注册）
	if err := KefuAddForUser(ctx, u.ID); err != nil {
		log.Printf("[kefu] auto add kefu for user %d failed: %v", u.ID, err)
	}

	// 9.1.1 注册成功按配置自动加入默认群聊（后台 default_group_config，失败不阻断注册）
	if err := DefaultGroupJoinForUser(ctx, u.ID); err != nil {
		log.Printf("[default-group] auto join for user %d failed: %v", u.ID, err)
	}
	// 9.1.2 注册成功按配置自动关注默认频道（后台 default_channel_config，失败不阻断注册）
	if err := DefaultChannelFollowForUser(ctx, u.ID); err != nil {
		log.Printf("[default-channel] auto follow for user %d failed: %v", u.ID, err)
	}

	// 9.2 注册时填了自定义邀请码 → 自动添加该邀请码关联的好友（失败不阻断注册）
	if err := InviteFriendBindForRegister(ctx, req.InviteCode, u.ID); err != nil {
		log.Printf("[invite] auto add invite friends for user %d failed: %v", u.ID, err)
	}

	// 10. 注册成功即登录，签发 token
	access, refresh, err := issueTokens(ctx, cfg, &u, req.DeviceID, req.DeviceType, req.DeviceName)
	return &u, access, refresh, err
}

// ============ 登录 ============

type LoginReq struct {
	Account    string `json:"account" binding:"required"`
	Password   string `json:"password" binding:"required"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"` // 客户端上报的设备型号
	// snake_case 别名（im-pc 发 device_id / device_type，见 RegisterReq 注释）
	DeviceIDAlias   string `json:"device_id"`
	DeviceType      int    `json:"deviceType"`
	DeviceTypeAlias int    `json:"device_type"`
}

// LoginResult 登录结果：VerifyRequired=true 表示需要二次验证（设备批准），
// 前端据此展示输码 / 等待批准界面；false 表示已直接发 token。
type LoginResult struct {
	User           *model.User
	AccessToken    string
	RefreshToken   string
	VerifyRequired bool
	VerifyTicket   string
	Method         string // code | approve
	ApproveDevice  string // approve 方式下被推送批准的设备号
	AllowApprove   bool   // 小助手开启且有 R 时，前端可切换「设备批准」通道（二选一）
	ExpiresIn      int
}

func Login(ctx context.Context, cfg *config.Config, req *LoginReq, ip, clientVersion string) (*LoginResult, error) {
	req.DeviceID, req.DeviceType = NormalizeDevice(req.DeviceID, req.DeviceType, req.DeviceIDAlias, req.DeviceTypeAlias)
	account := strings.TrimSpace(req.Account)
	// IP 维度封禁前置检查：被临时封锁的 IP 直接拒，省去后续校验
	if isIPBanned(ctx, ip) {
		return nil, errs.IPBanned
	}
	// 限流：每账号 5 次/分钟
	if err := rateLimit(ctx, "login:"+account, 5, time.Minute); err != nil {
		return nil, err
	}

	var u model.User
	if err := store.DB.Where("account = ?", account).First(&u).Error; err != nil {
		writeLoginLog(account, ip, "", 0)
		return nil, errs.LoginFailed
	}
	if u.Status != model.StatusNormal {
		writeLoginLog(account, ip, "", 0)
		// 账号被封禁：明确提示"已被封禁"，不要复用通用的"无权限"
		return nil, errs.Banned
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		writeLoginLog(account, ip, "", 0)
		incrLoginFail(ctx, ip) // 密码错误计数 +1，达到上限即拉黑 IP 24h
		return nil, errs.LoginFailed
	}

	// 更新最后登录时间 + 最后登录 IP（后台用户详情/列表展示）
	now := time.Now()
	store.DB.Model(&u).Updates(map[string]interface{}{
		"last_login_at": now,
		"last_login_ip": ip,
	})
	u.LastLoginAt = &now
	u.LastLoginIP = ip

	// 是否走二次验证：
	//  - 开关开启 + 客户端携带非空 X-Client-Version 头（Electron 桌面端等）→ startVerify；
	//  - 开关开启 + 网页 PC(Web, type=3) 密码登录 → 无论是否带版本头都必须 startVerify。
	//    产品策略（2026-09-24 用户拍板）：网页密码登录每次都需设备批准/验证码，
	//    仅两种豁免——①二维码扫码登录（走独立 qr 流程不经此处，天然免批准）；
	//    ②账户无任何可信设备时（startVerify 内「无 R」分支兜底，防锁死）。
	//    不存在「网页免验证」第三豁免，故此处对 Web 强制进 startVerify。
	//    老桌面/App 客户端（无版本头且非 Web）仍按「首登即信任」保底直接发 token（设计 §6.1）。
	// 扫码登录不经 Login，故上面的「②」不影响扫码。
	if boolVal(SysConfigGet(ctx, "account_verify_enabled", false)) && (strings.TrimSpace(clientVersion) != "" || req.DeviceType == 3) {
		return startVerify(ctx, cfg, &u, req, ip)
	}

	// 原逻辑：直接发 token（开关关 / 老客户端保底）
	access, refresh, err := issueTokens(ctx, cfg, &u, req.DeviceID, req.DeviceType, req.DeviceName)
	if err != nil {
		return nil, err
	}
	writeLoginLog(account, ip, deviceName(req.DeviceType), 1)
	clearLoginFail(ctx, ip) // 登录成功清除失败计数
	notifyLogin(ctx, cfg, u.ID, deviceName(req.DeviceType), ip, "")
	return &LoginResult{User: &u, AccessToken: access, RefreshToken: refresh}, nil
}

// startVerify 进入二次验证流程（密码已校验通过）。决策见设计 §3 method 决策树：
//   - ① 当前设备已受信（trusted=1，Web 除外）→ 直接放行（桌面受信免码 / App 受信免批准）
//   - ② 非 Web（手机/PC）+ 小助手开 → code（小助手发码，手机/PC 一致；密码泄露+拿到设备仍需码）
//   - ③ 网页 Web(3) 密码登录 → 必须设备批准（approve）；扫码登录走 qr 流程不经此处，天然免批准。
//     Web 本身不当审批方（RecentDevice 已排除 type 3），故批准推给手机等可信设备。
//   - ④ 存在最近可信设备 R（排除 Web/当前设备）→ 设备批准（approve）
//   - ⑤ 无任何其他可信设备 → 豁免直接登录（无锁死；首登/唯一设备场景）
//
// 2026-09-24 修正：此前 Web(3) 被直接豁免（不进二次验证），与"网页 PC 密码登录必须设备批准"
// 的产品策略冲突。改为 Web 密码登录走 approve（存在可信设备时），仅扫码登录免批准。
func startVerify(ctx context.Context, cfg *config.Config, u *model.User, req *LoginReq, ip string) (*LoginResult, error) {
	devType := req.DeviceType
	assistantOn := boolVal(SysConfigGet(ctx, "assistant_enabled", true))

	// ① 当前设备已受信 → 直接放行（覆盖桌面受信免码 / App 受信免批准）。
	//    Web(3) 除外：网页端设备号存于浏览器 localStorage、随清缓存即变，且按策略
	//    「网页 PC 密码登录每次都需设备批准」，故即便曾被标记受信也不走此短路。
	//    用户重申（2026-09-24）：自己电脑设备是信任设备 → 重登豁免，仅当下。
	if devType != 3 && req.DeviceID != "" && currentTrusted(ctx, u.ID, req.DeviceID) {
		return verifyExempt(ctx, cfg, u, req, ip, devType)
	}

	R := RecentDevice(ctx, u.ID, req.DeviceID, true) // 已排除当前设备 + 排除 Web(type 3 审批方)

	// 网页 Web(3) 密码登录：必须设备批准（扫码登录走 qr 流程不经此处，天然免批准）。
	// 存在其他可信设备 R 时推 approve；无 R 则豁免（无锁死，防首登卡死）。
	if devType == 3 {
		if R != nil {
			ticket, _, err := CreateVerifySession(ctx, u.ID, req, ip, "approve", R.DeviceID)
			if err != nil {
				return nil, err
			}
			return &LoginResult{VerifyRequired: true, VerifyTicket: ticket, Method: "approve", ApproveDevice: R.DeviceID, ExpiresIn: 300}, nil
		}
		return verifyExempt(ctx, cfg, u, req, ip, devType)
	}

	// 移动端 / PC：存在其他可信设备 R → 必须验证（批准或验证码）；无 R → 豁免（首登/唯一设备）。
	// 产品策略（2026-09-24 用户重申）：手机/PC 一致——只要账号有任何信任设备（哪怕只有手机是信任），
	// PC/手机登录都必须批准或验证码；仅当账号【无任何信任设备】时 PC 首登才豁免（防锁死）。
	// 修正：此前「② PC+小助手开→code」排在 R 判定之前，导致无 R 首登也走验证码、违背首登豁免；
	// 现已收进 R!=nil 分支内（2026-09-24 修正）。
	if R != nil {
		// 小助手开启 → 验证码（手机/PC 一致），同时把 R 写入会话并标记 AllowApprove，
		// 前端可一键「改用设备批准」切换到 approve 通道（二选一，2026-09-24 用户拍板）。
		// 小助手关闭 → 仅设备批准（弹窗）。
		if assistantOn {
			ticket, _, err := CreateVerifySession(ctx, u.ID, req, ip, "code", R.DeviceID)
			if err != nil {
				return nil, err
			}
			return &LoginResult{VerifyRequired: true, VerifyTicket: ticket, Method: "code", ApproveDevice: R.DeviceID, AllowApprove: true, ExpiresIn: 300}, nil
		}
		ticket, _, err := CreateVerifySession(ctx, u.ID, req, ip, "approve", R.DeviceID)
		if err != nil {
			return nil, err
		}
		return &LoginResult{VerifyRequired: true, VerifyTicket: ticket, Method: "approve", ApproveDevice: R.DeviceID, ExpiresIn: 300}, nil
	}
	// ⑤ 无任何其他可信设备 → 豁免直接登录（首登 / 唯一设备，防锁死）
	return verifyExempt(ctx, cfg, u, req, ip, devType)
}

// verifyExempt 豁免直接发 token（首登/受信重登/Web），并标记受信 + 登录通知（小助手开时）。
func verifyExempt(ctx context.Context, cfg *config.Config, u *model.User, req *LoginReq, ip string, devType int) (*LoginResult, error) {
	access, refresh, err := issueTokens(ctx, cfg, u, req.DeviceID, req.DeviceType, req.DeviceName)
	if err != nil {
		return nil, err
	}
	writeLoginLog(u.Account, ip, deviceName(devType), 1)
	clearLoginFail(ctx, ip)
	notifyLogin(ctx, cfg, u.ID, deviceName(devType), ip, "")
	return &LoginResult{User: u, AccessToken: access, RefreshToken: refresh}, nil
}

// currentTrusted 当前设备是否已受信（device 表存在且 trusted=1）。用于登录二次验证短路：
// 受信设备（曾成功登录且未被手动注销）重登直接放行，无需再次 code/approve（设计 §3.1 规则 3）。
func currentTrusted(ctx context.Context, uid int64, deviceID string) bool {
	var n int64
	if err := store.DB.Model(&model.Device{}).
		Where("user_id = ? AND device_id = ? AND trusted = ?", uid, deviceID, 1).
		Count(&n).Error; err != nil {
		return false
	}
	return n > 0
}

// ============ 账号可用性查询 ============

// AccountAvailable 查询账号是否可用（公开接口 /auth/check-account 使用）。
// 仅在账号格式合法时返回结果；格式非法直接返回错误由 handler 透出。
func AccountAvailable(ctx context.Context, account string) (bool, error) {
	account = strings.TrimSpace(account)
	if !isEmail(account) && !isPhone(account) && !isUsername(account) {
		return false, &errs.Err{Code: 1001, Msg: "账号需为邮箱、手机号或用户名（3-20位，仅限英文、数字、下划线）"}
	}
	var cnt int64
	store.DB.Model(&model.User{}).Where("account = ?", account).Count(&cnt)
	return cnt == 0, nil
}

// ============ 游客注册/登录 ============

// GuestRegisterReq 游客注册/登录请求（按设备号幂等）
type GuestRegisterReq struct {
	DeviceID   string `json:"deviceId" binding:"required"`
	DeviceType int    `json:"deviceType"` // 1=Android 2=iOS 3=Web 4=Windows 5=macOS
	DeviceName string `json:"deviceName"` // 客户端上报的设备型号
	// snake_case 别名（im-pc 发 device_id / device_type）
	DeviceIDAlias   string `json:"device_id"`
	DeviceTypeAlias int    `json:"device_type"`
	// 邀请码（2026-09-17 需求）：后台开启 invite_code_enabled 时新游客**必填**，
	// 服务端强制校验，不允许跳过；未开启时不校验（兼容旧客户端不传）
	InviteCode string `json:"inviteCode"`
}

// GuestRegister 游客注册或登录：
//   - 后台未开启 guest_register_enabled → 返回 errs.GuestOff
//   - 已存在该设备号的游客(is_guest=1 且 guest_device_id=该设备) → 直接复用并签发 token
//   - 否则新建游客账号：随机短账号(≤10 位) + 服务端随机密码 + 随机中文昵称
//   - 邀请码（2026-09-17 需求）：后台开启 invite_code_enabled 时，新游客必须携带
//     有效邀请码（请求体 inviteCode），服务端强制校验、不允许跳过；未开启不校验。
//     老游客幂等复用不受影响；「登录后补绑」端点 /user/invite/bind 保留兼容。
func GuestRegister(ctx context.Context, cfg *config.Config, req *GuestRegisterReq, clientIP string) (*model.User, string, string, bool, error) {
	if !boolVal(SysConfigGet(ctx, "guest_register_enabled", false)) {
		return nil, "", "", false, errs.GuestOff
	}

	// 1. 幂等复用：同一设备号只对应一个游客账号
	var exist model.User
	if err := store.DB.Where("guest_device_id = ? AND is_guest = 1", req.DeviceID).First(&exist).Error; err == nil {
		now := time.Now()
		store.DB.Model(&exist).Updates(map[string]interface{}{
			"last_login_at": now,
			"last_login_ip": clientIP,
		})
		exist.LastLoginAt = &now
		exist.LastLoginIP = clientIP

		// 幂等补齐：首次绑定若失败过（客服当时不在 / DB 抖动），此后每次登录都命中本分支短路，
		// 会永远补不回来。这里补一次（内部会判断是否已存在关系，已存在则跳过，见函数注释）。
		guestEnsureAutoFriends(ctx, cfg, exist.ID)

		access, refresh, err := issueTokens(ctx, cfg, &exist, req.DeviceID, req.DeviceType, req.DeviceName)
		return &exist, access, refresh, false, err
	}

	// 2. 新建游客账号
	account := genGuestAccount(ctx)
	hash, err := bcrypt.GenerateFromPassword([]byte(randToken(16)), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", "", false, err
	}
	// 2.0 邀请码（2026-09-17 需求）：后台开启 invite_code_enabled 时，新游客**必须**
	// 携带有效邀请码，不允许跳过——校验口径与注册接口一致：一次性码 consume 失败
	// 回退自定义好友邀请码（后台创建、多用不限次）；两者都无效/为空 → 2003。
	// 老游客幂等复用（上方分支）不 consume、不受影响。
	inviteOn := cfg.InviteCodeOn
	if v := SysConfigGet(ctx, "invite_code_enabled", cfg.InviteCodeOn); v != nil {
		inviteOn = boolVal(v)
	}
	if inviteOn {
		ic := strings.TrimSpace(req.InviteCode)
		if err := consumeInviteCode(ctx, ic, account); err != nil {
			if !InviteFriendCodeValid(ctx, ic) {
				return nil, "", "", false, err
			}
		}
	}
	regDev := deviceName(req.DeviceType)
	if req.DeviceID != "" {
		regDev += " / " + req.DeviceID
	}
	u := model.User{
		ID:             id.Next(),
		Account:        account,
		PasswordHash:   string(hash),
		Nickname:       randomChineseNickname(),
		CountryCode:    "+86",
		Status:         model.StatusNormal,
		Role:           model.RoleUser,
		IsGuest:        1,
		GuestDeviceID:  req.DeviceID,
		ShortID:        model.StrPtr(genShortID(ctx)),
		RegisterIP:     clientIP,
		RegisterDevice: regDev,
	}
	// 邀请关系持久化（与注册接口同口径）：invited_code 存原文（后台按码可搜到
	// 此人），invited_by 按码反查上级用户 ID（查不到为 0）
	if ic := strings.TrimSpace(req.InviteCode); ic != "" {
		u.InvitedCode = ic
		u.InvitedBy = resolveInviterByCode(ctx, ic, u.ID)
	}
	if av, ok := SysConfigGet(ctx, "default_avatar", "").(string); ok && av != "" {
		u.Avatar = av
	}
	if err := store.DB.Create(&u).Error; err != nil {
		return nil, "", "", false, err
	}

	// 3. 自动添加小助手 + 客服好友
	if err := AssistantAddForUser(ctx, cfg, u.ID); err != nil {
		log.Printf("[assistant] auto add assistant for guest %d failed: %v", u.ID, err)
	}
	if err := KefuAddForUser(ctx, u.ID); err != nil {
		log.Printf("[kefu] auto add kefu for guest %d failed: %v", u.ID, err)
	}
	// 游客注册同样按配置自动加入默认群聊
	if err := DefaultGroupJoinForUser(ctx, u.ID); err != nil {
		log.Printf("[default-group] auto join for guest %d failed: %v", u.ID, err)
	}
	// 游客注册同样按配置自动关注默认频道（失败不阻断）
	if err := DefaultChannelFollowForUser(ctx, u.ID); err != nil {
		log.Printf("[default-channel] auto follow for guest %d failed: %v", u.ID, err)
	}
	// 3.1 填了自定义邀请码 → 自动添加该邀请码关联的好友（与注册接口同口径，失败不阻断）
	if err := InviteFriendBindForRegister(ctx, req.InviteCode, u.ID); err != nil {
		log.Printf("[invite] auto add invite friends for guest %d failed: %v", u.ID, err)
	}

	// 4. 签发 token
	access, refresh, err := issueTokens(ctx, cfg, &u, req.DeviceID, req.DeviceType, req.DeviceName)
	return &u, access, refresh, true, err
}

// guestEnsureAutoFriends 幂等补齐「复用已有游客账号」场景下缺失的小助手 / 客服关系。
//
// 背景：GuestRegister 命中「同一设备号已存在游客」时会直接 return，只有「新建游客」才会走
// 自动添加流程（本文件步骤 3）。因此首次绑定若因任何原因失败（客服当时不在、DB 抖动等），
// 该账号此后永远补不回来——因为后续每次登录都会走这条短路分支。
//
// 为什么不能无条件调用 AssistantAddForUser / KefuAddForUser：
// 两者的「建关系」部分确实是幂等的（kefuBind 用 FirstOrCreate、CreateDirect 先查后建），
// 但它们成功建完关系后都会各发一条消息（小助手欢迎语、客服招呼），而 SendMessage 在
// ClientMsgID 为空时会由服务端生成新的 UUID（message.go:61-64），幂等去重判断不会命中，
// 即每次调用都会落一条新消息。本分支是登录热路径（游客每次打开 App 都会走到），
// 无条件调用会导致游客每次重新登录都收到重复的欢迎语与客服招呼。
//
// 因此先判断关系/会话是否已存在，仅缺失时才执行完整流程：
//   - 缺失 → 执行一次（发一条消息）；下次登录已存在则跳过，效果与「新建游客」路径一致；
//   - 已存在 → 直接跳过，不产生任何重复消息。
func guestEnsureAutoFriends(ctx context.Context, cfg *config.Config, userID int64) {
	if userID <= 0 {
		return
	}
	// 小助手（assistant 虚拟 uid = -1）：会话不存在才建会话 + 发欢迎语
	if !hasDirectConvWith(ctx, userID, -1) {
		if err := AssistantAddForUser(ctx, cfg, userID); err != nil {
			log.Printf("[assistant] auto add assistant for existing guest %d failed: %v", userID, err)
		}
	}
	// 客服：尚未与任何在职客服建立好友关系才执行绑定 + 打招呼
	if !hasKefuFriend(ctx, userID) {
		if err := KefuAddForUser(ctx, userID); err != nil {
			log.Printf("[kefu] auto add kefu for existing guest %d failed: %v", userID, err)
		}
	}
	// 默认群聊：进群是静默幂等插入（无消息副作用），直接调用由函数内部按成员存在性去重。
	// 每次登录多花 1 次配置读取 + 每群 1 次成员计数查询，登录热路径可接受。
	if err := DefaultGroupJoinForUser(ctx, userID); err != nil {
		log.Printf("[default-group] auto join for existing guest %d failed: %v", userID, err)
	}
	// 默认频道：关注同为静默幂等插入（同上可接受）
	if err := DefaultChannelFollowForUser(ctx, userID); err != nil {
		log.Printf("[default-channel] auto follow for existing guest %d failed: %v", userID, err)
	}
}

// hasKefuFriend 判断 userID 是否已与任一在职客服（role=RoleKefu 且 status=StatusNormal）建立好友关系。
// kefuBind 是双向写入，因此只需查 user_id = userID 这一侧即可。
func hasKefuFriend(ctx context.Context, userID int64) bool {
	var cnt int64
	store.DB.WithContext(ctx).Model(&model.FriendRelation{}).
		Where("user_id = ? AND friend_id IN (?)", userID,
			store.DB.WithContext(ctx).Model(&model.User{}).Select("id").
				Where("role = ? AND status = ?", model.RoleKefu, model.StatusNormal)).
		Count(&cnt)
	return cnt > 0
}

// hasDirectConvWith 判断 userID 与 otherID 之间是否已存在单聊会话（只查询，不创建）。
// 查询条件与 CreateDirect 内部的查重逻辑保持一致（conversation.go:30-34）。
func hasDirectConvWith(ctx context.Context, userID, otherID int64) bool {
	var cid int64
	store.DB.WithContext(ctx).Raw(`
		SELECT c.id FROM conversation c
		JOIN conversation_member m1 ON m1.conversation_id = c.id AND m1.user_id = ?
		JOIN conversation_member m2 ON m2.conversation_id = c.id AND m2.user_id = ?
		WHERE c.type = ? AND c.status = ? LIMIT 1`,
		userID, otherID, model.ConvDirect, model.ConvNormal).Scan(&cid)
	return cid > 0
}

// genGuestAccount 生成游客短账号：前缀 g + 8 位 hex = 9 字符（≤10 位，且不与邮箱/手机号冲突）
func genGuestAccount(ctx context.Context) string {
	for i := 0; i < 5; i++ {
		acc := "g" + randToken(4) // 1 + 8 = 9 字符
		var cnt int64
		store.DB.Model(&model.User{}).Where("account = ?", acc).Count(&cnt)
		if cnt == 0 {
			return acc
		}
	}
	return "g" + randToken(4)
}

// BindInviteCode 登录后补填邀请码（游客/普通用户通用）。
// 完全复用注册流程的邀请码处理：先尝试验证一次性邀请码(consume)，
// 失败再回退自定义好友邀请码；最终调用 InviteFriendBindForRegister 自动加好友。
func BindInviteCode(ctx context.Context, code string, userID int64) error {
	code = strings.TrimSpace(code)
	if code == "" || userID <= 0 {
		return errs.ParamError
	}
	var u model.User
	if err := store.DB.First(&u, userID).Error; err != nil {
		return errs.Unauthorized
	}
	// 与 Register 一致的回退逻辑
	if err := consumeInviteCode(ctx, code, u.Account); err != nil {
		if !InviteFriendCodeValid(ctx, code) {
			return err
		}
	}
	// 记录邀请关系：仅首次绑定写入（保留首次，不覆盖）；已有邀请码时只记日志，不报错，
	// 保证游客/普通用户重复绑定时接口行为不变（仍返回成功）。
	if strings.TrimSpace(u.InvitedCode) == "" {
		upd := map[string]interface{}{
			"invited_code": code,
			"invited_by":   resolveInviterByCode(ctx, code, userID),
		}
		if err := store.DB.Model(&model.User{}).Where("id = ?", userID).Updates(upd).Error; err != nil {
			log.Printf("[invite] persist invited relation for user %d failed: %v", userID, err)
		}
	} else {
		log.Printf("[invite] user %d already has invited_code %q, skip %q", userID, u.InvitedCode, code)
	}
	if err := InviteFriendBindForRegister(ctx, code, userID); err != nil {
		log.Printf("[invite] bind invite friends for user %d failed: %v", userID, err)
	}
	return nil
}

// Refresh 刷新 access token。
// 多设备独立槽位：refresh:{uid} 是 Hash，字段为设备号（slot），
// 因此 PC 登录不会再顶掉 App，且可精确吊销单台设备（见 Logout）。
func Refresh(ctx context.Context, cfg *config.Config, refreshToken string) (string, error) {
	claims, err := jwtx.Parse(cfg.JWTSecret, refreshToken)
	if err != nil {
		return "", errs.Unauthorized
	}
	// 校验 Redis 白名单：只比对「该设备槽位」里存的 refresh token
	slot := refreshSlot(claims.DeviceID)
	stored, ok := refreshSlotGet(ctx, claims.UserID, slot)
	if !ok || stored != refreshToken {
		return "", errs.Unauthorized
	}
	var u model.User
	if err := store.DB.Select("id", "role", "status", "token_version").
		Where("id = ?", claims.UserID).First(&u).Error; err != nil {
		return "", errs.Unauthorized
	}
	if u.Status != model.StatusNormal {
		return "", errs.Unauthorized
	}
	// 令牌版本比对：改密码 / 被禁用 / 管理员重置密码后 +1，旧 refresh 立即失效
	if u.TokenVersion != claims.Ver {
		return "", errs.Unauthorized
	}
	return jwtx.Generate(cfg.JWTSecret, u.ID, u.Role, cfg.JWTAccessTTLHours, u.TokenVersion, claims.DeviceID)
}

// Logout 登出：只吊销当前设备的 refresh 槽位（精确吊销单设备，不影响其它端在线）。
func Logout(ctx context.Context, userID int64, deviceID string) error {
	return refreshSlotDel(ctx, userID, refreshSlot(deviceID))
}

// LogoutAll 吊销该用户所有设备的 refresh 槽位（改密码 / 注销 / 管理员禁用/重置密码时使用）。
func LogoutAll(ctx context.Context, userID int64) error {
	return refreshAllDel(ctx, userID)
}

// ChangePassword 修改登录密码（需校验旧密码）。
// 密码落库后额外做两件事，让「旧密码已泄露 → 用户改密码」能立刻把攻击者会话踢掉：
//  1. token_version + 1（原子自增）—— 令所有已签发的 access/refresh token 立即失效；
//  2. LogoutAll —— 删掉该用户整个 refresh Hash。
//
// 注意：改完密码本人的当前设备也会掉线，这是有意为之（安全优先），不要为保住当前设备加特例。
//
// E2EE（§36 定稿）：encPriv = 客户端用**新密码**派生 KEK 重新包裹的私钥备份密文。
//   - 已有备份却未带 encPriv → 拒绝（4401），防「密码已改、备份没换」——
//     否则下次换设备用新密码解不开旧备份，历史消息永久丢失；
//   - encPriv 与密码 hash 在**同一事务**落库，失败整体回滚，不存在中间态；
//   - 公钥不变（身份密钥对不轮换），历史消息继续可解。
func ChangePassword(ctx context.Context, userID int64, oldPwd, newPwd, encPriv string) error {
	if !validPassword(newPwd) {
		return &errs.Err{Code: 1001, Msg: "新密码需为 6-20 位"}
	}
	var u model.User
	if err := store.DB.First(&u, userID).Error; err != nil {
		return errs.Unauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(oldPwd)) != nil {
		return &errs.Err{Code: 1001, Msg: "原密码错误"}
	}
	// E2EE 备份一致性校验（事务外先查，事务内带条件更新）
	var uk model.UserKey
	hasKey := store.DB.Where("user_id = ?", userID).First(&uk).Error == nil
	if hasKey && uk.EncryptedPrivateKey != "" && encPriv == "" {
		return errs.E2eeBackupRequired
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPwd), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	err = store.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", userID).
			Updates(map[string]interface{}{
				"password_hash": string(hash),
				"token_version": gorm.Expr("token_version + 1"),
			}).Error; err != nil {
			return err
		}
		// re-wrap 备份：仅在客户端确实带新密文时更新（行存在但备份为空 → 顺带补上）
		if hasKey && encPriv != "" {
			return tx.Model(&model.UserKey{}).Where("user_id = ?", userID).
				Updates(map[string]interface{}{
					"encrypted_private_key": encPriv,
					"key_version":           gorm.Expr("key_version + 1"),
				}).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 删掉整个 refresh Hash：所有设备需重新登录
	if err := LogoutAll(ctx, userID); err != nil {
		log.Printf("[auth] logout all after password change for user %d failed: %v", userID, err)
	}
	return nil
}

// DeleteAccount 注销账户：软删除（状态置为禁用，无法再登录），
// 并把状态更新与 token_version + 1 合并为一次写入 —— 令已签发的 access/refresh token 立即失效；
// 同时清除整个 refresh Hash。
func DeleteAccount(ctx context.Context, userID int64) error {
	if err := store.DB.Model(&model.User{}).Where("id = ?", userID).
		Updates(map[string]interface{}{
			"status":        model.StatusDisabled,
			"token_version": gorm.Expr("token_version + 1"),
		}).Error; err != nil {
		return err
	}
	if err := LogoutAll(ctx, userID); err != nil {
		log.Printf("[auth] logout all after delete account for user %d failed: %v", userID, err)
	}
	return nil
}

// ============ 验证码 ============

type SendCodeReq struct {
	Account     string `json:"account" binding:"required"` // 手机号（短信模式）或邮箱（邮箱模式）
	CountryCode string `json:"countryCode"`                // 国际区号，默认 +86
	// 是否必填交给业务层 needCaptcha 判断：关掉图形验证码开关后前端不传这两个字段，
	// 若仍标 required 会让 Gin 直接返回 1001 参数错误，导致开关形同虚设。
	CaptchaID   string `json:"captchaId"`
	CaptchaCode string `json:"captchaCode"`
	Channel     string `json:"channel"` // sms / email / ""（为空时按 AUTH_MODE 决定）
}

// SendCode 发送注册/找回验证码（按认证模式或客户端显式指定的渠道）
// 返回实际使用的发送渠道（sms / email），供前端提示"已发送至短信/邮箱"
func SendCode(ctx context.Context, cfg *config.Config, req *SendCodeReq) (string, error) {
	// 图形验证码是「发送手机/邮箱验证码」的前置门槛：仅 captcha_enabled 开启
	// 且认证模式为 sms/email 时才校验（auth_mode=none 时既不发码也不校验图形码）
	if needCaptcha(ctx, cfg) {
		if err := verifyCaptcha(ctx, req.CaptchaID, req.CaptchaCode); err != nil {
			return "", err
		}
	}
	// 限流：每账号 1 次/60s
	if err := rateLimit(ctx, "sendcode:"+req.Account, 1, 60*time.Second); err != nil {
		return "", err
	}

	code, err := genCode(6)
	if err != nil {
		return "", err
	}

	// 注册方式与认证开关（2026-09-22 需求6）：发码渠道由注册方式决定——
	// phone → 短信、email → 邮箱，且必须「开启注册认证」；账号密码注册方式
	// / 未开启认证 → 拒绝发码（1001）。register_type 未配置的存量部署由
	// RegisterModeGet 从 auth_mode 推导（sms/email 部署发码行为不变）。
	// 客户端传入的 channel 仅作参考，实际渠道以注册方式为准（防伪造）。
	regType, verifyOn := RegisterModeGet(ctx, cfg)
	var channel string
	switch {
	case regType == regTypePhone && verifyOn:
		channel = authModeSMS
	case regType == regTypeEmail && verifyOn:
		channel = authModeEmail
	}

	// 短信/邮件配置：优先读取后台 sys_config（管理后台「系统设置-短信/邮件」），回退到环境变量。
	// 这样在后台配置阿里云短信/SMTP 后即可生效，无需改环境变量重启。
	smsAK := strVal(SysConfigGet(ctx, "sms_access_key", cfg.AliyunSMSAccessKey))
	smsSK := strVal(SysConfigGet(ctx, "sms_secret", cfg.AliyunSMSSecretKey))
	smsSign := strVal(SysConfigGet(ctx, "sms_sign_name", cfg.AliyunSMSSignName))
	smsTpl := strVal(SysConfigGet(ctx, "sms_template_code", cfg.AliyunSMSTemplateCode))
	smtpHost := strVal(SysConfigGet(ctx, "smtp_host", cfg.SMTPHost))
	smtpUser := strVal(SysConfigGet(ctx, "smtp_user", cfg.SMTPUser))
	smtpPass := strVal(SysConfigGet(ctx, "smtp_password", cfg.SMTPPassword))
	smtpFrom := strVal(SysConfigGet(ctx, "smtp_from", cfg.SMTPFrom))
	// 端口是数字：SysConfigGet 解出来可能是 float64 / 字符串，统一用 intVal 收敛
	smtpPort := intVal(SysConfigGet(ctx, "smtp_port", cfg.SMTPPort), cfg.SMTPPort)

	switch channel {
	case authModeSMS:
		if !isPhone(req.Account) {
			return "", &errs.Err{Code: 1001, Msg: "短信验证码需使用手机号"}
		}
		if smsAK == "" || smsTpl == "" {
			return "", &errs.Err{Code: 2002, Msg: "短信服务未配置（请在后台系统设置中配置阿里云短信，或设置环境变量 ALIYUN_SMS_*）"}
		}
		if err := sendSMSCode(smsAK, smsSK, smsSign, smsTpl, req.CountryCode, req.Account, code); err != nil {
			log.Printf("send sms failed: %v", err)
			return "", &errs.Err{Code: 2002, Msg: "短信发送失败：" + err.Error()}
		}
		if err := storeCode(ctx, "sms", req.Account, code); err != nil {
			return "", err
		}
	case authModeEmail:
		if !isEmail(req.Account) {
			return "", &errs.Err{Code: 1001, Msg: "邮箱验证码需使用邮箱"}
		}
		if smtpHost == "" || smtpUser == "" {
			return "", &errs.Err{Code: 2002, Msg: "邮件服务未配置（请在后台系统设置中配置 SMTP，或设置环境变量 SMTP_*）"}
		}
		if err := sendEmailCode(smtpHost, smtpPort, smtpUser, smtpPass, smtpFrom, req.Account, code); err != nil {
			log.Printf("send email failed: %v", err)
			return "", &errs.Err{Code: 2002, Msg: "邮件发送失败：" + err.Error()}
		}
		if err := storeCode(ctx, "email", req.Account, code); err != nil {
			return "", err
		}
	default:
		return "", &errs.Err{Code: 1001, Msg: "当前未开启验证码服务"}
	}
	return channel, nil
}

// ============ 绑定手机号 ============

// SendBindPhoneCode 绑定手机号：校验图形验证码后，向该手机号发送短信验证码（走已配置的短信服务）
func SendBindPhoneCode(ctx context.Context, cfg *config.Config, uid int64, phone, countryCode, captchaID, captchaCode string) error {
	// 与 SendCode 保持一致：图形验证码仅在 needCaptcha 为真时才校验
	if needCaptcha(ctx, cfg) {
		if err := verifyCaptcha(ctx, captchaID, captchaCode); err != nil {
			return err
		}
	}
	if !isPhone(phone) {
		return &errs.Err{Code: 1001, Msg: "请输入正确的手机号"}
	}
	// 限流：每手机号 1 次/60s
	if err := rateLimit(ctx, "bindphone:"+phone, 1, 60*time.Second); err != nil {
		return err
	}
	code, err := genCode(6)
	if err != nil {
		return err
	}
	// 短信配置：优先后台 sys_config（sms_access_key 等），回退到环境变量
	smsAK := strVal(SysConfigGet(ctx, "sms_access_key", cfg.AliyunSMSAccessKey))
	smsSK := strVal(SysConfigGet(ctx, "sms_secret", cfg.AliyunSMSSecretKey))
	smsSign := strVal(SysConfigGet(ctx, "sms_sign_name", cfg.AliyunSMSSignName))
	smsTpl := strVal(SysConfigGet(ctx, "sms_template_code", cfg.AliyunSMSTemplateCode))
	if smsAK == "" || smsTpl == "" {
		return &errs.Err{Code: 2002, Msg: "短信服务未配置（请在后台系统设置中配置阿里云短信）"}
	}
	if err := sendSMSCode(smsAK, smsSK, smsSign, smsTpl, countryCode, phone, code); err != nil {
		log.Printf("send bind-phone sms failed: %v", err)
		return &errs.Err{Code: 2002, Msg: "短信发送失败：" + err.Error()}
	}
	return storeCode(ctx, "bindphone", phone, code)
}

// BindPhone 绑定手机号：校验短信验证码后写入用户手机号（校验唯一性，避免被他人占用）
func BindPhone(ctx context.Context, uid int64, phone, countryCode, code string) error {
	if err := verifyCode(ctx, "bindphone", phone, code); err != nil {
		return err
	}
	var cnt int64
	store.DB.Model(&model.User{}).Where("phone = ? AND id <> ?", phone, uid).Count(&cnt)
	if cnt > 0 {
		return &errs.Err{Code: 1001, Msg: "该手机号已被其他账号绑定"}
	}
	cc := countryCode
	if cc == "" {
		cc = "+86"
	}
	if err := store.DB.Model(&model.User{}).Where("id = ?", uid).
		Updates(map[string]interface{}{"phone": phone, "country_code": cc}).Error; err != nil {
		return err
	}
	return nil
}

// Captcha 生成图形验证码，返回 captchaID 与 base64 图片
func Captcha(ctx context.Context) (string, string, error) {
	code, b64, err := captcha.Generate()
	if err != nil {
		return "", "", err
	}
	cid := "c" + hex.EncodeToString([]byte(fmt.Sprintf("%d-%d", time.Now().UnixNano(), codeLenRand())))
	if err := store.RDB.Set(ctx, "code:captcha:"+cid, code, 5*time.Minute).Err(); err != nil {
		return "", "", err
	}
	return cid, b64, nil
}

// ============ 内部工具 ============

func issueTokens(ctx context.Context, cfg *config.Config, u *model.User, deviceID string, deviceType int, deviceName ...string) (string, string, error) {
	access, err := jwtx.Generate(cfg.JWTSecret, u.ID, u.Role, cfg.JWTAccessTTLHours, u.TokenVersion, deviceID)
	if err != nil {
		return "", "", err
	}
	slot := refreshSlot(deviceID)
	refresh, err := jwtx.GenerateForDevice(cfg.JWTSecret, u.ID, u.Role, cfg.JWTRefreshTTLDays*24, u.TokenVersion, deviceID)
	if err != nil {
		return "", "", err
	}
	// refresh 白名单：Hash 存多设备独立槽位（HSET refresh:{uid} {slot} {token} + EXPIRE）
	if err := refreshSlotSet(ctx, u.ID, slot, refresh, time.Duration(cfg.JWTRefreshTTLDays)*24*time.Hour); err != nil {
		return "", "", err
	}
	// 设备登记（可变参数 deviceName 兼容旧调用：扫码登录等未带型号的入口不写型号列；
	// name 为空时不清空已存的型号 —— 老客户端重复登录不能把型号抹掉）
	if deviceID != "" {
		attrs := model.Device{
			DeviceType:   deviceType,
			PushToken:    "",
			Status:       1,
			LastActiveAt: time.Now(),
		}
		if len(deviceName) > 0 && deviceName[0] != "" {
			attrs.DeviceName = deviceName[0]
		}
		store.DB.Where("user_id = ? AND device_id = ?", u.ID, deviceID).
			Assign(attrs).Omit("Trusted").FirstOrCreate(&model.Device{UserID: u.ID, DeviceID: deviceID})
		// 统一出口：所有成功发 token 的设备标记 trusted=1（首次审批/豁免/重登均获信任，见 §3.1）。
		// trusted 列由 migrations/028 补，迁移前该列不存在，Update 报错仅记录不阻断。
		if err := store.DB.Model(&model.Device{}).
			Where("user_id = ? AND device_id = ?", u.ID, deviceID).
			Update("trusted", 1).Error; err != nil {
			log.Printf("[auth] mark trusted failed uid=%d device=%s err=%v", u.ID, deviceID, err)
		}
	}
	return access, refresh, nil
}

// ============ refresh 多设备槽位（Redis Hash） ============
//
// 结构：Hash `refresh:{uid}`，字段 = 设备号（slot），值 = 该设备的 refresh token。
// slot 为空设备号时用字面量 "default"。相比旧的 String 单槽，多设备互不顶掉，
// 且可精确吊销单台设备（HDel 单个字段）。
//
// 旧数据兼容（滚动上线期间，老键仍是 String 类型 refresh:{uid} = token）：
//   - 读取：HGet 拿到 WRONGTYPE，或字段不存在且 slot=="default" 时，退回旧 String 键比对；
//   - 删除：HDel 拿到 WRONGTYPE 时改为 Del 整个键；
//   - 写入：HSet 拿到 WRONGTYPE 时先 Del 旧键再重试 HSet —— 老键在用户下次登录时
//     自动升级为 Hash。三个动作必须都兼容，漏掉写入侧会直接打挂存量用户的登录。
// refreshAllDel 直接 Del（Hash/String 都适用）。

const refreshSlotDefault = "default"

// refreshSlot 归一化设备号为槽位名：空 → "default"
func refreshSlot(deviceID string) string {
	if deviceID == "" {
		return refreshSlotDefault
	}
	return deviceID
}

func refreshKey(uid int64) string { return fmt.Sprintf("refresh:%d", uid) }

func refreshSlotSet(ctx context.Context, uid int64, slot, token string, ttl time.Duration) error {
	key := refreshKey(uid)
	if err := store.RDB.HSet(ctx, key, slot, token).Err(); err != nil {
		// 旧版本是 String 单槽：HSet 打在 String 键上会 WRONGTYPE，先删掉旧键再重试。
		// 这一条不能省 —— 登录路径 issueTokens → refreshSlotSet，直接 return err 会让
		// 存量用户（键还是 String）登录失败，而 access token 还能用，故障表现极具迷惑性。
		if strings.Contains(err.Error(), "WRONGTYPE") {
			if err = store.RDB.Del(ctx, key).Err(); err != nil {
				return err
			}
			if err = store.RDB.HSet(ctx, key, slot, token).Err(); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	// 每次写入都续期整个 Hash（Hash 有 TTL，字段级不单独过期）
	store.RDB.Expire(ctx, key, ttl)
	return nil
}

func refreshSlotGet(ctx context.Context, uid int64, slot string) (string, bool) {
	key := refreshKey(uid)
	v, err := store.RDB.HGet(ctx, key, slot).Result()
	if err == nil {
		return v, true
	}
	// 字段不存在：只有 default 槽位才回退查旧 String 键（旧数据只有一个 token）
	if errors.Is(err, redis.Nil) {
		if slot != refreshSlotDefault {
			return "", false
		}
		if old, e := store.RDB.Get(ctx, key).Result(); e == nil {
			return old, true
		}
		return "", false
	}
	// 键还是旧 String 类型（WRONGTYPE）→ 退回 Get 比对
	if strings.Contains(err.Error(), "WRONGTYPE") {
		if old, e := store.RDB.Get(ctx, key).Result(); e == nil {
			return old, true
		}
	}
	return "", false
}

func refreshSlotDel(ctx context.Context, uid int64, slot string) error {
	key := refreshKey(uid)
	if err := store.RDB.HDel(ctx, key, slot).Err(); err != nil {
		if strings.Contains(err.Error(), "WRONGTYPE") {
			return store.RDB.Del(ctx, key).Err()
		}
		return err
	}
	return nil
}

// refreshAllDel 删除整个 refresh 键（Hash/String 都适用）
func refreshAllDel(ctx context.Context, uid int64) error {
	return store.RDB.Del(ctx, refreshKey(uid)).Err()
}

// needCaptcha 是否需要校验图文验证码。
// 语义：图文验证码是「发送手机/邮箱验证码」的前置门槛，只有注册方式为手机/邮箱
// 且「开启注册认证」（2026-09-22 需求6）时才存在这个门槛；账号密码注册方式
// 不发码也不校验图形码。register_type 未配置的存量部署由 RegisterModeGet
// 从 auth_mode 推导（sms/email 部署行为不变）。
func needCaptcha(ctx context.Context, cfg *config.Config) bool {
	if !boolVal(SysConfigGet(ctx, "captcha_enabled", false)) {
		return false
	}
	regType, verifyOn := RegisterModeGet(ctx, cfg)
	return verifyOn && (regType == regTypePhone || regType == regTypeEmail)
}

func verifyCaptcha(ctx context.Context, cid, code string) error {
	if cid == "" || code == "" {
		return &errs.Err{Code: 1001, Msg: "请先获取图形验证码"}
	}
	key := "code:captcha:" + cid
	stored, err := store.RDB.Get(ctx, key).Result()
	if err != nil || stored != strings.ToLower(code) {
		// 试错计数：窗口 10 分钟（覆盖验证码 5 分钟有效期），超过 5 次删掉验证码强制重新获取。
		// 错误信息不区分「验证码不存在」与「验证码错误」，避免枚举。
		if overCodeTryLimit(ctx, "code:captry:"+cid) {
			store.RDB.Del(ctx, key)
			return &errs.Err{Code: 2002, Msg: "验证码错误次数过多，请重新获取"}
		}
		return &errs.Err{Code: 2002, Msg: "图形验证码错误或过期"}
	}
	store.RDB.Del(ctx, key)
	// 校验成功即清理计数器（保持验证码「一次性使用」语义）
	store.RDB.Del(ctx, "code:captry:"+cid)
	return nil
}

func storeCode(ctx context.Context, typ, account, code string) error {
	key := fmt.Sprintf("code:%s:%s", typ, account)
	return store.RDB.Set(ctx, key, code, 5*time.Minute).Err()
}

func verifyCode(ctx context.Context, typ, account, code string) error {
	key := fmt.Sprintf("code:%s:%s", typ, account)
	stored, err := store.RDB.Get(ctx, key).Result()
	if err != nil || stored != code {
		// 试错计数：窗口 10 分钟（覆盖验证码 5 分钟有效期），超过 5 次删掉验证码强制重新获取。
		// 错误信息不区分「验证码不存在」与「验证码错误」，避免枚举。
		if overCodeTryLimit(ctx, fmt.Sprintf("code:try:%s:%s", typ, account)) {
			store.RDB.Del(ctx, key)
			return &errs.Err{Code: 2002, Msg: "验证码错误次数过多，请重新获取"}
		}
		return errs.CodeInvalid
	}
	store.RDB.Del(ctx, key)
	// 校验成功即清理计数器（保持验证码「一次性使用」语义）
	store.RDB.Del(ctx, fmt.Sprintf("code:try:%s:%s", typ, account))
	return nil
}

// overCodeTryLimit 验证码试错计数：先自增，返回是否已超过上限（5 次）。
// 窗口 10 分钟，首次计数时设置过期（写法与 rateLimit 一致）；
// 注意不要复用 rateLimit（它返回 errs.RateLimited=7001），这里只做布尔判断。
func overCodeTryLimit(ctx context.Context, key string) bool {
	n, err := store.RDB.Incr(ctx, key).Result()
	if err != nil {
		// Redis 异常时不阻断校验流程，按未超限处理
		return false
	}
	if n == 1 {
		store.RDB.Expire(ctx, key, 10*time.Minute)
	}
	return n > 5
}

func consumeInviteCode(ctx context.Context, code, account string) error {
	if code == "" {
		return errs.InviteInvalid
	}
	var ic model.InviteCode
	if err := store.DB.Where("code = ? AND enabled = 1", code).First(&ic).Error; err != nil {
		return errs.InviteInvalid
	}
	if ic.UsedBy != nil {
		return errs.InviteInvalid
	}
	if ic.ExpiresAt != nil && time.Now().After(*ic.ExpiresAt) {
		return errs.InviteInvalid
	}
	now := time.Now()
	return store.DB.Model(&ic).Updates(map[string]interface{}{
		"used_by": account, "used_at": now,
	}).Error
}

func rateLimit(ctx context.Context, key string, limit int, window time.Duration) error {
	k := "rl:" + key
	n, err := store.RDB.Incr(ctx, k).Result()
	if err != nil {
		return err
	}
	if n == 1 {
		store.RDB.Expire(ctx, k, window)
	}
	if n > int64(limit) {
		return errs.RateLimited
	}
	return nil
}

// ============ 密码错误 5 次拉黑 IP 24h ============
// 防同一 IP 换不同账号爆破登录。与「按账号 5 次/分钟」速率限制（rateLimit）互补：
// 后者防单账号爆破，前者防 IP 维度换号爆破。计数/封禁均存 Redis（TTL 天然适配 24h），
// 沿用既有 Incr+Expire 范式；Redis 异常时按"放行"处理，不误伤（封禁检查）、不阻断（计数）。
const (
	loginFailLimit = 5
	loginBanWindow = 24 * time.Hour
)

func loginFailKey(ip string) string { return "login:fail:ip:" + ip }
func loginBanKey(ip string) string  { return "login:ban:ip:" + ip }

// isIPBanned 该 IP 是否处于临时封锁（Redis 挂时放行，不误伤全体）
func isIPBanned(ctx context.Context, ip string) bool {
	n, err := store.RDB.Exists(ctx, loginBanKey(ip)).Result()
	if err != nil {
		return false
	}
	return n > 0
}

// incrLoginFail 密码错误计数 +1；达到上限即写 24h 封锁标记（含本这次失败）
func incrLoginFail(ctx context.Context, ip string) {
	k := loginFailKey(ip)
	n, err := store.RDB.Incr(ctx, k).Result()
	if err != nil {
		return
	}
	if n == 1 {
		store.RDB.Expire(ctx, k, loginBanWindow)
	}
	if n >= int64(loginFailLimit) {
		store.RDB.Set(ctx, loginBanKey(ip), time.Now().Add(loginBanWindow).Unix(), loginBanWindow)
	}
}

// clearLoginFail 登录成功后清除失败计数（封禁标记靠 TTL 自然过期，无需手动清）
func clearLoginFail(ctx context.Context, ip string) {
	store.RDB.Del(ctx, loginFailKey(ip))
}

// CheckAccountRateLimited 账号可用性查询的限流：按 IP 每分钟 30 次。
// handler 无法调用包私有 rateLimit，故在此导出包装。
func CheckAccountRateLimited(ctx context.Context, ip string) error {
	return rateLimit(ctx, "checkaccount:"+ip, 30, time.Minute)
}

func writeLoginLog(account, ip, device string, result int) {
	var u model.User
	uid := int64(0)
	if err := store.DB.Where("account = ?", account).First(&u).Error; err == nil {
		uid = u.ID
	}
	store.DB.Create(&model.LoginLog{UserID: uid, IP: ip, Device: device, Result: result})
}

func genCode(n int) (string, error) {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		b, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		sb.WriteByte(byte('0' + b.Int64()))
	}
	return sb.String(), nil
}

func codeLenRand() int64 {
	b, _ := rand.Int(rand.Reader, big.NewInt(100000))
	return b.Int64()
}

func defaultNickname(account string) string {
	if isEmail(account) {
		return strings.Split(account, "@")[0]
	}
	// 手机号必须排在 isUsername 之前：纯数字同时满足 isUsername，
	// 否则手机号注册的默认昵称会变成完整手机号（等于把手机号当昵称外露）。
	if isPhone(account) {
		if len(account) >= 4 {
			return "用户" + account[len(account)-4:]
		}
		return "用户"
	}
	// 用户名直接作为默认昵称（注册页第 1 步收集的就是用户名）
	if isUsername(account) {
		return account
	}
	if len(account) >= 4 {
		return "用户" + account[len(account)-4:]
	}
	return "用户"
}

// genShortID 生成用户靓号 ID（需求12：可通过 ID 添加好友）
// 2026-09-17 需求3：改为 **8 位以上随机数字**、不按顺序分配——在 8 位空间
// （10000000-99999999）内加密随机起抽，抽满（极小概率）升 9 位、再 10 位；
// 唯一性双保险：分配前查 user.short_id 计数 + 数据库 short_id 唯一索引兜底。
// 跳过后台保留号段（reserved_short_ids 配置）与靓号池（reserved_short_id 表，
// 池内号码是后台预留分配的，随机不能占用）。老用户已分配的 5 位短号保持不变。
func genShortID(ctx context.Context) string {
	reserved := map[string]bool{}
	if v, ok := SysConfigGet(ctx, "reserved_short_ids", "").(string); ok && v != "" {
		for _, s := range strings.Split(v, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				reserved[s] = true
			}
		}
	}
	for digits := 8; digits <= 10; digits++ {
		lo := int64(1)
		for i := 1; i < digits; i++ {
			lo *= 10
		}
		hi := lo * 10 // 上界（不含）
		for i := 0; i < 200; i++ {
			n, err := rand.Int(rand.Reader, big.NewInt(hi-lo))
			if err != nil {
				// 加密随机源故障：退化为时间戳派生，保证不阻塞注册
				n = big.NewInt(time.Now().UnixNano() % (hi - lo))
			}
			short := fmt.Sprintf("%d", lo+n.Int64())
			if reserved[short] {
				continue
			}
			var cnt int64
			store.DB.Model(&model.User{}).Where("short_id = ?", short).Count(&cnt)
			if cnt > 0 {
				continue
			}
			var rc int64
			store.DB.Model(&model.ReservedShortID{}).Where("short_id = ?", short).Count(&rc)
			if rc > 0 {
				continue
			}
			return short
		}
	}
	// 兜底：时间戳派生 9 位（正常到不了这里）
	return fmt.Sprintf("%d", time.Now().UnixNano()%1000000000+100000000)
}

// validPassword 密码策略：长度 6-20 位（按 rune 数计），不要求字母+数字组合。
// 按 rune 计是为了让中文等多字节字符按「字符个数」而非「字节数」计量，与前端 maxlength 语义一致。
func validPassword(p string) bool {
	n := len([]rune(p))
	return n >= 6 && n <= 20
}

func isEmail(s string) bool {
	return strings.Contains(s, "@") && strings.Contains(s, ".")
}

func isPhone(s string) bool {
	if len(s) < 5 || len(s) > 20 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isUsername 用户名校验：3-20 位（按 rune 计），仅限英文、数字、下划线。
// 对应注册页第 1 步的「用户名（3-20位，仅限英文、数字、下划线）」。
func isUsername(s string) bool {
	n := len([]rune(s))
	if n < 3 || n > 20 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_') {
			return false
		}
	}
	return true
}

func deviceName(t int) string {
	switch t {
	case 1:
		return "Android"
	case 2:
		return "iOS"
	case 3:
		return "Web"
	case 4:
		return "Windows"
	case 5:
		return "macOS"
	}
	return "Unknown"
}

// ============ 游客随机昵称 / 随机串 ============

var (
	surnameList   = []string{"李", "王", "张", "刘", "陈", "杨", "赵", "黄", "周", "吴", "徐", "孙", "马", "朱", "胡", "林", "郭", "何", "高", "罗", "郑", "梁", "谢", "宋", "唐", "许", "韩", "冯", "邓", "曹"}
	givenNameList = []string{"晓明", "小红", "子轩", "一诺", "梓涵", "浩然", "欣怡", "宇航", "思源", "雨桐", "俊杰", "佳怡", "梓萱", "晨曦", "若曦", "天磊", "梦琪", "志强", "雅静", "文博", "可馨", "嘉豪", "诗涵", "博文", "婉清", "立诚", "乐瑶", "修远", "清扬", "知微"}
)

// pick 从列表中按 crypto/rand 取一个元素（无外部依赖）
func pick(list []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(list))))
	if err != nil {
		return list[0]
	}
	return list[n.Int64()]
}

// randomChineseNickname 生成随机中文昵称（姓 + 名）
func randomChineseNickname() string {
	return pick(surnameList) + pick(givenNameList)
}

// randToken 生成 n 字节的十六进制随机串（2n 字符）
func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

var _ = errors.New
