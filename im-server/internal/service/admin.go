package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/pkg/id"
	"github.com/yourcompany/im-server/internal/store"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// ============ 系统配置（数据库优先，回退环境变量） ============

// SysConfigGet 读取配置（sys_config 表），缺省返回 def
func SysConfigGet(ctx context.Context, key string, def interface{}) interface{} {
	var sc model.SysConfig
	if err := store.DB.Where("config_key = ?", key).First(&sc).Error; err == nil {
		var v interface{}
		if json.Unmarshal([]byte(sc.ConfigValue), &v) == nil {
			if m, ok := v.(map[string]interface{}); ok {
				if val, ok := m["value"]; ok {
					return val
				}
				return v
			}
			return v
		}
	}
	return def
}

// SysConfigSet 写入配置（存 {"value": ...}）
func SysConfigSet(ctx context.Context, key string, value interface{}) error {
	b, _ := json.Marshal(map[string]interface{}{"value": value})
	var sc model.SysConfig
	err := store.DB.Where("config_key = ?", key).First(&sc).Error
	if err != nil {
		// 不存在则插入
		return store.DB.Create(&model.SysConfig{ConfigKey: key, ConfigValue: string(b)}).Error
	}
	return store.DB.Model(&sc).Update("config_value", string(b)).Error
}

// AuthFlags 认证配置（数据库优先，回退 cfg）
type AuthFlags struct {
	AuthMode     string `json:"authMode"`
	InviteCodeOn bool   `json:"inviteCodeOn"`
	RegisterOn   bool   `json:"registerOn"`
	E2EOn        bool   `json:"e2eOn"`
	// E2EEMode 加密方式三选（§36 拍板②）：off 关闭 / server 服务端可解密 / e2ee 端到端。
	// 未配置时按旧 e2e_enabled 布尔推导（true→server，false→off），见 service.E2eeMode。
	E2EEMode string `json:"e2eeMode"`
	// 品牌（登录/注册页 logo + 名称，后台可配）
	AppName   string `json:"appName"`
	AppLogo   string `json:"appLogo"`
	BrandName string `json:"brandName"`
	BrandLogo string `json:"brandLogo"`
	// 公告（移动端消息页跑马灯，后台可配）
	Announcement string `json:"announcement"`
	// App 版本信息（后台可配，客户端关于页/更新检查）
	// 三端独立检测：安卓/iOS/PC 各自比对版本号，H5 不检测（见 update_service / im-pc auth）。
	// android_version / ios_version 未配置时回退旧键 app_version，存量部署平滑过渡。
	AppVersion     string `json:"appVersion"`
	AndroidVersion string `json:"androidVersion"`
	IOSVersion     string `json:"iosVersion"`
	PCVersion      string `json:"pcVersion"`
	UpdateLog      string `json:"updateLog"`
	AndroidURL     string `json:"androidUrl"`
	IOSURL         string `json:"iosUrl"`
	PCURL          string `json:"pcUrl"`
	HotUpdateURL   string `json:"hotUpdateUrl"`
	// 在线状态：当前登录设备（web/ios/android/windows/macos）
	OnlineDevice string `json:"onlineDevice"`
	// 需求9：默认头像（新注册用户使用，后台可配）
	DefaultAvatar string `json:"defaultAvatar"`
	// 小助手头像（通讯录官方入口显示，后台「智能小助手」可配）
	AssistantAvatar string `json:"assistantAvatar"`
	// 功能开关：是否开启零钱（关闭 → 聊天窗口不显示红包/转账入口、用户中心不显示我的钱包）
	WalletOn bool `json:"walletOn"`
	// 功能开关：是否开启邀请码（2026-09-22 需求3 合并：原「开启邀请码」
	// invite_feature_enabled 废弃，本字段改读「邀请码注册」invite_code_enabled，
	// 关闭 → 注册页无邀请码输入框、游客直接进入、用户中心不显示我的邀请码）
	InviteFeatureOn bool `json:"inviteFeatureOn"`
	GuestOn         bool `json:"guestOn"`
	// 功能开关：是否开启图形验证码。注意仅为「开关值」，实际是否校验还取决于 authMode：
	// 只有 authMode 为 sms/email 时图文验证码才会被真正校验（见 needCaptcha）。
	CaptchaOn bool `json:"captchaOn"`
	// 注册方式（2026-09-22 需求6，sys_config register_type）：account（默认，
	// 账号密码）/ phone（手机号，默认区号 +86）/ email（邮箱）。后台「注册认证」配置。
	RegisterType string `json:"registerType"`
	// 开启注册认证（2026-09-22 需求6，sys_config register_verify_enabled，默认 false）：
	// 仅手机/邮箱注册需要短信/邮箱验证码；账号密码注册方式即使开启也不需要。
	RegisterVerifyOn bool `json:"registerVerifyOn"`
	// 功能开关：是否开启小助手（2026-09-22 需求2，sys_config assistant_enabled 默认 true）：
	// 关闭 → 注册/游客登录不自动添加小助手（AssistantAddForUser 短路）、
	// App 通讯录不显示小助手入口
	AssistantOn bool `json:"assistantOn"`
	// 功能开关：是否开启频道（2026-09-22 需求2，sys_config channel_enabled 默认 true）：
	// 关闭 → App 隐藏「新建频道」入口、注册不自动关注频道（DefaultChannelFollowForUser
	// 短路）、创建频道接口拒绝（CreateChannel 返回 4031，兜底旧版本客户端）
	ChannelOn bool `json:"channelOn"`
	// iOS 离线推送服务商（jpush 默认 / getui）：App 按此分流初始化推送 SDK（见 service/getui.go）。
	// 个推三件套随配置下发（getuiflut 插件 iOS startSdk 需要，AppSecret 属插件机制要求）。
	PushProviderIos string `json:"pushProviderIos"`
	GetuiAppId      string `json:"getuiAppId"`
	GetuiAppKey     string `json:"getuiAppKey"`
	GetuiAppSecret  string `json:"getuiAppSecret"`
	// 谷歌地图 API key（位置消息选点页 JS API / 静态地图缩略图共用；
	// 空串 = 位置功能不展示，App 端据此隐藏入口）
	GoogleMapsApiKey string `json:"googleMapsApiKey"`
	// 位置消息地图引擎（2026-09-22 需求1）：后台可选 google（默认）/ amap。
	// 空串视为 google（存量部署向后兼容，App 端只认 'amap'，其余全走谷歌）。
	MapEngine string `json:"mapEngine"`
	// 高德三件套（engine=amap 时生效）：
	//   AmapJsKey  —— 「Web端(JS API)」类型 Key，选点页 WebView 地图用；
	//   AmapJscode —— 该 Key 配套的安全密钥 securityJsCode（2021-12-02 之后
	//                 申请的 Key 必填，缺了服务请求报 INVALID_USER_SCODE）；
	//   AmapWebKey —— 「Web服务」类型 Key，位置气泡静态缩略图（restapi.amap.com）。
	AmapJsKey  string `json:"amapJsKey"`
	AmapJscode string `json:"amapJscode"`
	AmapWebKey string `json:"amapWebKey"`
}

func GetAuthFlags(ctx context.Context, cfg *config.Config) AuthFlags {
	appVer := strVal(SysConfigGet(ctx, "app_version", "1.0.0"))
	androidVer := strVal(SysConfigGet(ctx, "android_version", ""))
	if androidVer == "" {
		androidVer = appVer // 未单独配置安卓版本 → 回退旧键，存量部署行为不变
	}
	iosVer := strVal(SysConfigGet(ctx, "ios_version", ""))
	if iosVer == "" {
		iosVer = appVer
	}
	flags := AuthFlags{
		AuthMode:         strVal(SysConfigGet(ctx, "auth_mode", cfg.AuthMode)),
		InviteCodeOn:     boolVal(SysConfigGet(ctx, "invite_code_enabled", cfg.InviteCodeOn)),
		RegisterOn:       boolVal(SysConfigGet(ctx, "register_enabled", cfg.RegisterOn)),
		E2EOn:            boolVal(SysConfigGet(ctx, "e2e_enabled", cfg.E2EOn)),
		E2EEMode:         E2eeMode(ctx),
		AppName:          strVal(SysConfigGet(ctx, "app_name", "ChatPulse")),
		AppLogo:          strVal(SysConfigGet(ctx, "app_logo", "")),
		BrandName:        strVal(SysConfigGet(ctx, "brand_name", "ChatPulse")),
		BrandLogo:        strVal(SysConfigGet(ctx, "brand_logo", "")),
		Announcement:     strVal(SysConfigGet(ctx, "announcement", "欢迎使用 ChatPulse! 请注意账号安全，不要泄露验证码。")),
		AppVersion:       appVer,
		AndroidVersion:   androidVer,
		IOSVersion:       iosVer,
		PCVersion:        strVal(SysConfigGet(ctx, "pc_version", "")),
		UpdateLog:        strVal(SysConfigGet(ctx, "update_log", "")),
		AndroidURL:       strVal(SysConfigGet(ctx, "android_url", "")),
		IOSURL:           strVal(SysConfigGet(ctx, "ios_url", "")),
		PCURL:            strVal(SysConfigGet(ctx, "pc_url", "")),
		HotUpdateURL:     strVal(SysConfigGet(ctx, "hot_update_url", "")),
		OnlineDevice:     strVal(SysConfigGet(ctx, "online_device", "")),
		DefaultAvatar:    strVal(SysConfigGet(ctx, "default_avatar", "")),
		AssistantAvatar:  GetAssistantConfig(ctx, cfg).Avatar,
		WalletOn:         boolVal(SysConfigGet(ctx, "wallet_enabled", true)),
		InviteFeatureOn:  boolVal(SysConfigGet(ctx, "invite_code_enabled", true)),
		GuestOn:          boolVal(SysConfigGet(ctx, "guest_register_enabled", false)),
		CaptchaOn:        boolVal(SysConfigGet(ctx, "captcha_enabled", false)),
		AssistantOn:      boolVal(SysConfigGet(ctx, "assistant_enabled", true)),
		ChannelOn:        boolVal(SysConfigGet(ctx, "channel_enabled", true)),
		PushProviderIos:  iOSPushProvider(ctx),
		GetuiAppId:       strVal(SysConfigGet(ctx, "getui_app_id", "")),
		GetuiAppKey:      strVal(SysConfigGet(ctx, "getui_app_key", "")),
		GetuiAppSecret:   strVal(SysConfigGet(ctx, "getui_app_secret", "")),
		GoogleMapsApiKey: strVal(SysConfigGet(ctx, "google_maps_api_key", "")),
		MapEngine:        strVal(SysConfigGet(ctx, "map_engine", "")),
		AmapJsKey:        strVal(SysConfigGet(ctx, "amap_js_key", "")),
		AmapJscode:       strVal(SysConfigGet(ctx, "amap_jscode", "")),
		AmapWebKey:       strVal(SysConfigGet(ctx, "amap_web_key", "")),
	}
	// 注册方式与认证开关（2026-09-22 需求6）：GetAuthFlags 返回值类型，函数内补充赋值
	flags.RegisterType, flags.RegisterVerifyOn = RegisterModeGet(ctx, cfg)
	return flags
}

func strVal(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// boolVal 把 sys_config 读到的配置值转布尔。
// 2026-09-17 加固：服务器实测 jpush_app_key/jpush_master_secret 都能读到、
// 唯独 jpush_enabled 判 false——开关值可能被存成字符串 "true"（带引号），
// 旧实现只认 JSON 布尔，字符串一律 false，导致「页面上开着、服务端永远关闭」。
// 现同时兼容 bool / "true","1","on" / 数字非零。
func boolVal(v interface{}) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		s := strings.ToLower(strings.TrimSpace(b))
		return s == "true" || s == "1" || s == "on"
	case float64:
		return b != 0
	}
	return false
}

// intVal 把 sys_config 里读到的配置值转成正整数，失败或为非正数时返回 def。
// 之所以要处理三种类型：SysConfigGet 是 JSON 反序列化后的值，数字会被解成 float64，
// 而后台表单/旧数据可能存成字符串（如 "465"），直接断言 int 会静默拿到 0。
func intVal(v interface{}, def int) int {
	switch n := v.(type) {
	case int:
		if n > 0 {
			return n
		}
	case float64:
		if n > 0 {
			return int(n)
		}
	case string:
		if s := strings.TrimSpace(n); s != "" {
			if p, err := strconv.Atoi(s); err == nil && p > 0 {
				return p
			}
		}
	}
	return def
}

// ============ 用户管理 ============

type AdminUserListResult struct {
	List  []model.User `json:"list"`
	Total int64        `json:"total"`
}

// AdminUserList 用户列表：kw 关键字 / status 状态 / deptID 部门 / role 角色（0=全部）
// / inviteCode 按用户填写的邀请码精确搜索（空=不传）/ inviterID 查某人邀请了哪些人（>0 生效）/ 分页
func AdminUserList(ctx context.Context, kw string, status int, deptID int64, role int, inviteCode string, inviterID int64, page, size int) (*AdminUserListResult, error) {
	q := store.DB.Model(&model.User{})
	if kw != "" {
		like := "%" + kw + "%"
		// 增加 short_id 精确+模糊匹配：纯数字时先按 short_id 精确命中（搜索 18888 这样的靓号）
		if num, err := strconv.ParseInt(kw, 10, 64); err == nil && num > 0 {
			q = q.Where(
				"short_id = ? OR short_id LIKE ? OR nickname LIKE ? OR account LIKE ? OR phone LIKE ? OR email LIKE ?",
				kw, like, like, like, like, like,
			)
		} else {
			q = q.Where(
				"short_id LIKE ? OR nickname LIKE ? OR account LIKE ? OR phone LIKE ? OR email LIKE ?",
				like, like, like, like, like,
			)
		}
	}
	if status > 0 {
		q = q.Where("status = ?", status)
	}
	if deptID > 0 {
		q = q.Where("department_id = ?", deptID)
	}
	// role: 1 普通用户 / 2 管理员 / 3 客服（见 model/user.go 常量），0 或不传=不过滤
	if role > 0 {
		q = q.Where("role = ?", role)
	}
	// 邀请关系：按用户注册/绑定时填写的邀请码原文精确搜索（空串视为不传）
	if ic := strings.TrimSpace(inviteCode); ic != "" {
		q = q.Where("invited_code = ?", ic)
	}
	// 邀请关系：查某个上级邀请了哪些人
	if inviterID > 0 {
		q = q.Where("invited_by = ?", inviterID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 100 {
		size = 20
	}
	var users []model.User
	if err := q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&users).Error; err != nil {
		return nil, err
	}
	return &AdminUserListResult{List: users, Total: total}, nil
}

// AdminUserCreate 管理员创建账号
// AdminUserCreateReq 后台创建账号（2026-09-25 需求）：
//   - 账号 / 手机号 / 邮箱三个字段分开填，**三选一**（至少填一个、只认填了的那个当登录账号；
//     手机号落 user.phone、邮箱落 user.email，便于按手机/邮箱搜索与后续短信/邮箱登录）；
//   - 头像留空 → 取后台「默认头像」配置（sys_config.default_avatar，App 端默认头像）；
//   - 靓号/短 ID 留空 → 走注册同款 genShortID（8 位起随机数字、避开保留号段与靓号池）；
//   - 密码留空 → 默认 123456。
type AdminUserCreateReq struct {
	Account  string // 用户名（3-20 位，仅限英文、数字、下划线）
	Phone    string // 手机号（纯数字 5-20 位）
	Email    string // 邮箱
	Password string // 空 → 123456
	Nickname string // 空 → 按账号推导（邮箱前缀 / 手机后4位 / 用户名本身）
	Avatar   string // 空 → default_avatar
	ShortID  string // 空 → genShortID 自动分配
	DeptID   int64
	Role     int
}

// AdminUserCreate 后台创建账号（三选一 + 默认头像 + 自动靓号），逻辑口径见 AdminUserCreateReq。
func AdminUserCreate(ctx context.Context, req AdminUserCreateReq) (*model.User, error) {
	account := strings.TrimSpace(req.Account)
	phone := strings.TrimSpace(req.Phone)
	email := strings.TrimSpace(req.Email)

	// 三选一：至少填一个；且只允许填一个（填多个无从判断用户意图，直接拒绝）
	filled := 0
	for _, s := range []string{account, phone, email} {
		if s != "" {
			filled++
		}
	}
	if filled == 0 {
		return nil, &errs.Err{Code: 1001, Msg: "账号 / 手机号 / 邮箱至少填写一项"}
	}
	if filled > 1 {
		return nil, &errs.Err{Code: 1001, Msg: "账号 / 手机号 / 邮箱三选一，只能填写其中一项"}
	}
	if account != "" && !isUsername(account) {
		return nil, &errs.Err{Code: 1001, Msg: "账号需为 3-20 位，仅限英文、数字、下划线"}
	}
	if phone != "" && !isPhone(phone) {
		return nil, &errs.Err{Code: 1001, Msg: "手机号格式不正确"}
	}
	if email != "" && !isEmail(email) {
		return nil, &errs.Err{Code: 1001, Msg: "邮箱格式不正确"}
	}
	// 登录账号 = 填的那一项（手机号/邮箱直接当账号，与前端注册口径一致）
	if account == "" {
		account = phone
	}
	if account == "" {
		account = email
	}

	// 唯一性：账号 / 手机号 / 邮箱都查重（非空才查）
	var cnt int64
	store.DB.WithContext(ctx).Model(&model.User{}).Where("account = ?", account).Count(&cnt)
	if cnt > 0 {
		return nil, errs.AccountExists
	}
	if phone != "" {
		store.DB.WithContext(ctx).Model(&model.User{}).Where("phone = ?", phone).Count(&cnt)
		if cnt > 0 {
			return nil, &errs.Err{Code: 1001, Msg: "该手机号已被绑定"}
		}
	}
	if email != "" {
		store.DB.WithContext(ctx).Model(&model.User{}).Where("email = ?", email).Count(&cnt)
		if cnt > 0 {
			return nil, &errs.Err{Code: 1001, Msg: "该邮箱已被绑定"}
		}
	}

	// 密码：空 → 123456（与批量生成、后台重置密码的默认值一致）
	password := req.Password
	if strings.TrimSpace(password) == "" {
		password = "123456"
	}
	if !validPassword(password) {
		return nil, &errs.Err{Code: 1001, Msg: "密码需为 6-20 位"}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	// 靓号 / 短 ID：填了查唯一性，没填走注册同款随机分配
	shortID := strings.TrimSpace(req.ShortID)
	if shortID != "" {
		store.DB.WithContext(ctx).Model(&model.User{}).Where("short_id = ?", shortID).Count(&cnt)
		if cnt > 0 {
			return nil, &errs.Err{Code: 1001, Msg: "该靓号已被使用"}
		}
	} else {
		shortID = genShortID(ctx)
	}

	// 头像：空 → 后台默认头像（App 端默认头像）
	avatar := strings.TrimSpace(req.Avatar)
	if avatar == "" {
		if av, ok := SysConfigGet(ctx, "default_avatar", "").(string); ok {
			avatar = av
		}
	}

	// 昵称：空 → 与注册同口径推导
	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" {
		nickname = defaultNickname(account)
	}

	u := &model.User{
		ID:           id.Next(),
		Account:      account,
		PasswordHash: string(hash),
		Nickname:     nickname,
		Avatar:       avatar,
		Phone:        phone,
		Email:        email,
		ShortID:      model.StrPtr(shortID),
		DepartmentID: req.DeptID,
		CountryCode:  "+86",
		Status:       model.StatusNormal,
		Role:         map[bool]int{true: req.Role, false: model.RoleUser}[req.Role > 0],
	}
	if err := store.DB.Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// AdminUserBatchCreate 批量生成账号（2026-09-25 需求）：按前端一键注册（游客）规则批量建号——
//   - 账号：g + 8 位 hex（9 字符，genGuestAccount 查重）
//   - 昵称：随机中文昵称（randomChineseNickname）
//   - 靓号/短 ID：genShortID 自动分配
//   - 头像：后台默认头像（default_avatar）
//   - 密码：固定 123456
//
// 非游客号（is_guest=0，有固定密码可正常账号密码登录）；注册后自动添加小助手/客服、
// 加入默认群聊、关注默认频道（与游客注册同口径，失败不阻断）。
// 返回生成的账号清单（账号/密码/昵称/靓号）供后台展示与导出。
func AdminUserBatchCreate(ctx context.Context, cfg *config.Config, count int) ([]map[string]string, error) {
	if count < 1 || count > 500 {
		return nil, &errs.Err{Code: 1001, Msg: "生成数量需为 1-500"}
	}
	avatar := ""
	if av, ok := SysConfigGet(ctx, "default_avatar", "").(string); ok {
		avatar = av
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]string, 0, count)
	for i := 0; i < count; i++ {
		u := model.User{
			ID:           id.Next(),
			Account:      genGuestAccount(ctx),
			PasswordHash: string(hash),
			Nickname:     randomChineseNickname(),
			Avatar:       avatar,
			ShortID:      model.StrPtr(genShortID(ctx)),
			CountryCode:  "+86",
			Status:       model.StatusNormal,
			Role:         model.RoleUser,
		}
		if err := store.DB.Create(&u).Error; err != nil {
			// 单个失败（极小概率账号/靓号撞唯一索引）：跳过并继续，保证总量尽量达标
			continue
		}
		// 与游客注册同口径的自动初始化（失败不阻断）
		if err := AssistantAddForUser(ctx, cfg, u.ID); err != nil {
			log.Printf("[batch-user] auto add assistant for %d failed: %v", u.ID, err)
		}
		if err := KefuAddForUser(ctx, u.ID); err != nil {
			log.Printf("[batch-user] auto add kefu for %d failed: %v", u.ID, err)
		}
		if err := DefaultGroupJoinForUser(ctx, u.ID); err != nil {
			log.Printf("[batch-user] auto join default group for %d failed: %v", u.ID, err)
		}
		if err := DefaultChannelFollowForUser(ctx, u.ID); err != nil {
			log.Printf("[batch-user] auto follow default channel for %d failed: %v", u.ID, err)
		}
		out = append(out, map[string]string{
			"account":  u.Account,
			"password": "123456",
			"nickname": u.Nickname,
			"shortId":  model.StrVal(u.ShortID),
		})
	}
	if len(out) == 0 {
		return nil, &errs.Err{Code: 500, Msg: "生成失败，请重试"}
	}
	return out, nil
}

// AdminUserSetStatus 启用/禁用用户。
// 禁用（status=StatusDisabled）时额外做三件事，保证"禁用立即生效"，而不是等 access token 自然过期：
//  1. token_version + 1（与 status 合并为一次 Updates）—— 令所有已签发的 access/refresh token 立即失效；
//  2. 清掉该用户的 refresh Hash（Redis key refresh:{uid}），
//     使其 access token 过期后无法用 refresh 续命；refresh 直接失败 → 客户端 401 清登录态；
//  3. 通过 Redis 事件总线推送 forceLogout 给该用户所有在线设备，
//     网关收到后立刻下发 WS 事件，客户端收到即清登录态并跳登录页（见各端 forceLogout 处理）。
//
// 此外鉴权中间件 Auth 会对每次请求复核 u.Status 与 u.TokenVersion，禁用账号的 access token 在下次任意请求即被 401 拦截。
func AdminUserSetStatus(ctx context.Context, id int64, status int) error {
	if status == model.StatusDisabled {
		// 禁用：status 与 token_version 合并为一次写入（原子自增，避免读-改-写竞态）
		if err := store.DB.Model(&model.User{}).Where("id = ?", id).
			Updates(map[string]interface{}{
				"status":        status,
				"token_version": gorm.Expr("token_version + 1"),
			}).Error; err != nil {
			return err
		}
		// 1) 清 refresh 白名单（整个 refresh Hash），使其续命失败
		if store.RDB != nil {
			_ = LogoutAll(ctx, id)
		}
		// 2) 推送强制下线事件，网关即时下发到在线设备
		_ = PublishEvent(ctx, &Event{
			Type:   "forceLogout",
			ToUIDs: []int64{id},
			Data:   json.RawMessage(`{"reason":"account_disabled"}`),
		})
		return nil
	}
	return store.DB.Model(&model.User{}).Where("id = ?", id).Update("status", status).Error
}

// AdminUserResetPassword 管理员重置用户密码：
// 更新密码 hash 的同时 token_version + 1（原子自增），并清空该用户整个 refresh Hash。
// 效果：原会话（access/refresh）立即全部失效，用户需重新登录。
//
// E2EE（§36 拍板②）：服务端无法代用户 re-wrap 私钥备份 → 重置即**烧备份**
// （删 user_keys 整行，公钥一并作废）。老设备本地私钥仍可解旧消息；
// 新设备登录后生成新密钥对，联系人端提示「对方密钥已变更」，旧历史新设备不可解。
// 后台 UI 应在端到端模式下对重置操作强警告提示。
func AdminUserResetPassword(ctx context.Context, id int64, newPass string) error {
	hash, _ := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err := store.DB.Model(&model.User{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"password_hash": string(hash),
			"token_version": gorm.Expr("token_version + 1"),
		}).Error; err != nil {
		return err
	}
	if store.RDB != nil {
		_ = LogoutAll(ctx, id)
	}
	if err := E2eeBurnBackup(ctx, id); err != nil {
		log.Printf("[admin] burn e2ee backup for user %d failed: %v", id, err)
	}
	return nil
}

// AdminUserUpdate 管理员编辑用户资料（nickname/avatar/role/shortId）。
// short_id 需要唯一，分配前会先检查 reserved_short_id 是否存在（若启用），
// 再做唯一性冲突校验；分配后 reserved_short_id.used_by/used_at 同步更新。
func AdminUserUpdate(ctx context.Context, id int64, nickname, avatar string, role int, shortID *string) error {
	if id <= 0 {
		return errs.ParamError
	}
	updates := map[string]interface{}{}
	// 字段用 !== nil 判空：允许空字符串清空
	if nickname != "" || (nickname == "" && len(nickname) == 0 && false) {
		// 上一行仅兼容旧写法；真正的空串不清空，但允许传空字符串也能安全进入下一层
	}
	if nickname != "" {
		updates["nickname"] = nickname
	}
	if avatar != "" {
		updates["avatar"] = avatar
	}
	if role == model.RoleUser || role == model.RoleAdmin || role == model.RoleKefu {
		// 角色变更必须让已签发的令牌立即失效：middleware.Auth 把 claims.Role 放进上下文，
		// RequireAdmin 只信这个值 —— 不吊销的话，被降权的管理员在 access token 到期前
		// （JWT_ACCESS_TTL_HOURS，默认 24h）仍能以 admin 身份继续调后台接口。
		// 只在角色**确实变化**时才 +1：后台保存资料通常会连带提交 role，
		// 若无条件 +1，管理员改个昵称就会把该用户所有设备踢下线。
		// 查库失败时按"可能变化"处理（fail-safe，宁可多踢一次）。
		changed := true
		var cur model.User
		if err := store.DB.Select("role").Where("id = ?", id).First(&cur).Error; err == nil {
			changed = cur.Role != role
		}
		updates["role"] = role
		if changed {
			updates["token_version"] = gorm.Expr("token_version + 1")
		}
	}

	// short_id：nil 表示不更新；空字符串表示清空；否则赋值并检查唯一性
	if shortID != nil {
		sid := strings.TrimSpace(*shortID)
		if sid == "" {
			updates["short_id"] = nil
		} else {
			// 唯一性检查
			var dup int64
			store.DB.Model(&model.User{}).
				Where("short_id = ? AND id <> ?", sid, id).Count(&dup)
			if dup > 0 {
				return &errs.Err{Code: 1001, Msg: "短ID 已被其他用户占用"}
			}
			updates["short_id"] = sid

			// 关联：如果 reserved_short_id 表里有这条（未使用），登记 used_by/used_at
			var rs model.ReservedShortID
			if err := store.DB.Where("short_id = ?", sid).First(&rs).Error; err == nil {
				store.DB.Model(&rs).Updates(map[string]interface{}{
					"used_by": id,
					"used_at": time.Now(),
					"status":  model.ReservedShortIDUsed,
					"account": "", // 展示时 JOIN user
				})
			}
		}
	}

	if len(updates) == 0 {
		return nil
	}
	return store.DB.Model(&model.User{}).Where("id = ?", id).Updates(updates).Error
}

// AdminUserDetail 用户详情聚合（后台「查看详情」）：
// 基础资料 + 钱包（余额/冻结）+ 累计充值/提现（wallet_transaction 汇总，服务端为准）
// + 注册/登录审计信息（注册 IP/设备、最后登录 IP）+ 统计（好友数/消息数/在线状态）
func AdminUserDetail(ctx context.Context, id int64) (map[string]interface{}, error) {
	if id <= 0 {
		return nil, errs.ParamError
	}
	var u model.User
	if err := store.DB.First(&u, id).Error; err != nil {
		return nil, &errs.Err{Code: 1001, Msg: "用户不存在"}
	}

	// 累计充值 / 累计提现（wallet_transaction：recharge 入账为正，withdraw 支出为负）
	var totalRecharge, totalWithdraw float64
	store.DB.Model(&model.WalletTransaction{}).
		Where("user_id = ? AND type = ?", id, model.WalletTxRecharge).
		Select("COALESCE(SUM(amount),0)").Scan(&totalRecharge)
	store.DB.Model(&model.WalletTransaction{}).
		Where("user_id = ? AND type = ?", id, model.WalletTxWithdraw).
		Select("COALESCE(SUM(amount),0)").Scan(&totalWithdraw)
	if totalWithdraw < 0 {
		totalWithdraw = -totalWithdraw
	}

	// 好友数（双向关系：user_id 或 friend_id 命中即算）
	var friendCount int64
	store.DB.Model(&model.FriendRelation{}).
		Where("user_id = ? OR friend_id = ?", id, id).Count(&friendCount)

	// 累计发送消息数（Mongo）
	msgCount, _ := msgColl().CountDocuments(ctx, bson.M{"sender_id": id})

	// 在线状态（Redis online:{uid}）
	online := false
	if store.RDB != nil {
		if n, err := store.RDB.Exists(ctx, "online:"+strconv.FormatInt(id, 10)).Result(); err == nil {
			online = n > 0
		}
	}

	// 邀请关系：他邀请了多少人（下级数）
	var inviteeCount int64
	store.DB.Model(&model.User{}).Where("invited_by = ?", id).Count(&inviteeCount)

	// 邀请关系：上级信息（u.InvitedBy=0 或上级已删除时为 null）
	var inviter map[string]interface{}
	if u.InvitedBy > 0 {
		var up model.User
		if err := store.DB.Select("id, nickname, account, short_id").First(&up, u.InvitedBy).Error; err == nil {
			inviter = map[string]interface{}{
				"id":       up.ID,
				"nickname": up.Nickname,
				"account":  up.Account,
				"shortId":  model.StrVal(up.ShortID),
			}
		}
	}

	// 最近登录设备（device 表按 last_active_at 最新一行）：后台用户详情展示真实设备型号
	latestDevice := map[string]interface{}(nil)
	var latestDev model.Device
	if err := store.DB.Where("user_id = ?", id).
		Order("last_active_at DESC, created_at DESC").First(&latestDev).Error; err == nil {
		latestDevice = map[string]interface{}{
			"deviceName":   latestDev.DeviceName,
			"deviceType":   latestDev.DeviceType,
			"lastActiveAt": latestDev.LastActiveAt,
		}
	}

	return map[string]interface{}{
		"user":          u,
		"totalRecharge": totalRecharge,
		"totalWithdraw": totalWithdraw,
		"friendCount":   friendCount,
		"msgCount":      msgCount,
		"online":        online,
		"latestDevice":  latestDevice,  // 最近登录设备（型号/类型/最后活跃），无设备行时 null
		"invitedCode":   u.InvitedCode, // 注册/绑定时填写的邀请码（没有则空串）
		"inviter":       inviter,       // 上级信息对象（没有则 null）
		"inviteeCount":  inviteeCount,  // 他邀请了多少人
	}, nil
}

// AdminUserDevices 返回该用户最近登录设备列表（含 IP 与信任状态），供后台详情展示。
// 复用 DeviceSessions，DeviceSession 已带 Trusted 字段（见 device.go）。
func AdminUserDevices(ctx context.Context, uid int64) ([]DeviceSession, error) {
	return DeviceSessions(ctx, uid, "")
}

// AdminClearTrust 清除该用户全部设备的信任标记（后台保底：远程强制重新登录验证）。
// 不会锁死账号——下次任意设备登录因「无可信设备」走豁免分支重新获信任（见设计 §3.1/§14）。
// 同时吊销所有 refresh 槽位 + 推送 device.logout，使当前会话被踢、须重登。
func AdminClearTrust(ctx context.Context, uid int64) (int64, error) {
	res := store.DB.Model(&model.Device{}).Where("user_id = ?", uid).Update("trusted", 0)
	if res.Error != nil {
		return 0, res.Error
	}
	if store.RDB != nil {
		store.RDB.Del(ctx, refreshKey(uid))
		if b, err := json.Marshal(map[string]interface{}{"uid": uid}); err == nil {
			_ = PublishEvent(ctx, &Event{Type: "device.logout", ToUIDs: []int64{uid}, Data: b})
		}
	}
	return res.RowsAffected, nil
}

// AdminDataClear 清空后台数据（危险操作，配合前端二次确认使用）。
// scope：users=用户数据 / chats=聊天数据 / groups=群组数据 / recharge=充值记录 / withdraw=提现记录 / all=以上全部。
// 返回各表删除条数，便于前端展示清理结果；管理员账号（role=2）永远保留，避免把自己锁在门外。
func AdminDataClear(ctx context.Context, scope string) (map[string]interface{}, error) {
	res := map[string]interface{}{}
	run := func(scopes ...string) {
		for _, s := range scopes {
			switch s {
			case "users":
				// 保留管理员；删除普通用户/客服及其关联数据
				var ids []int64
				store.DB.Model(&model.User{}).Where("role <> ?", model.RoleAdmin).Pluck("id", &ids)
				userCnt := int64(0)
				if len(ids) > 0 {
					// 必须用 Delete 的 RowsAffected，不能在 Delete 之后再链 Count：
					// gorm 会把已构建好的 DELETE 语句原样复用，Count 不会再发一条 count(*)，
					// 结果 userCnt 恒为 0（后台"清空用户数据"永远显示删除 0 条）。
					// 本函数其它分支（Device/FriendRelation/…）用的都是 .RowsAffected，此处对齐。
					userCnt = store.DB.Where("id IN ?", ids).Delete(&model.User{}).RowsAffected
					store.DB.Where("user_id IN ?", ids).Delete(&model.Device{})
					// 好友关系 / 好友申请 / 黑名单 / E2E 密钥：任一侧命中即删
					store.DB.Where("user_id IN ? OR friend_id IN ?", ids, ids).Delete(&model.FriendRelation{})
					store.DB.Where("from_user IN ? OR to_user IN ?", ids, ids).Delete(&model.FriendRequest{})
					store.DB.Where("user_id IN ? OR block_user_id IN ?", ids, ids).Delete(&model.Blacklist{})
					store.DB.Where("user_id IN ?", ids).Delete(&model.UserKey{})
					// 靓号池（reserved_short_id）本身保留，仅解除被删用户的占用引用
					store.DB.Model(&model.ReservedShortID{}).
						Where("used_by IN ? AND status = ?", ids, model.ReservedShortIDUsed).
						Updates(map[string]interface{}{"status": model.ReservedShortIDOpen, "used_by": 0, "used_at": nil})
				}
				res["users"] = userCnt
			case "chats":
				del, _ := msgColl().DeleteMany(ctx, bson.M{})
				res["messages"] = del.DeletedCount
				rc := store.DB.Where("1 = 1").Delete(&model.MessageReceipt{}).RowsAffected
				res["messageReceipts"] = rc
				fc := store.DB.Where("1 = 1").Delete(&model.MessageFavorite{}).RowsAffected
				res["messageFavorites"] = fc
				cc := store.DB.Where("1 = 1").Delete(&model.Conversation{}).RowsAffected
				res["conversations"] = cc
			case "groups":
				gc := store.DB.Where("type = ?", model.ConvGroup).Delete(&model.Conversation{}).RowsAffected
				res["groupConversations"] = gc
				mc := store.DB.Where("1 = 1").Delete(&model.ConversationMember{}).RowsAffected
				res["conversationMembers"] = mc
			case "recharge":
				rc := store.DB.Where("1 = 1").Delete(&model.RechargeOrder{}).RowsAffected
				res["rechargeOrders"] = rc
			case "withdraw":
				wc := store.DB.Where("1 = 1").Delete(&model.WithdrawOrder{}).RowsAffected
				res["withdrawOrders"] = wc
			}
		}
	}
	switch scope {
	case "all":
		run("chats", "groups", "users", "recharge", "withdraw")
		// 「所有数据」= 把软件数据清空：仅保留后台配置(sys_config)与管理员账号(role=2)。
		// 在上述范围之外，额外清空：钱包流水/冻结红包、靓号池、提现绑定、朋友圈、登录日志。
		wt := store.DB.Where("1 = 1").Delete(&model.WalletTransaction{}).RowsAffected
		res["walletTransactions"] = wt
		mp := store.DB.Where("1 = 1").Delete(&model.MoneyPacket{}).RowsAffected
		res["moneyPackets"] = mp
		rs := store.DB.Where("1 = 1").Delete(&model.ReservedShortID{}).RowsAffected
		res["reservedShortIds"] = rs
		wa := store.DB.Where("1 = 1").Delete(&model.WithdrawAccount{}).RowsAffected
		res["withdrawAccounts"] = wa
		mo := store.DB.Where("1 = 1").Delete(&model.MomentsPost{}).RowsAffected
		res["momentsPosts"] = mo
		ll := store.DB.Where("1 = 1").Delete(&model.LoginLog{}).RowsAffected
		res["loginLogs"] = ll
	case "users", "chats", "groups", "recharge", "withdraw":
		run(scope)
	default:
		return nil, &errs.Err{Code: 1001, Msg: "未知清空范围: " + scope}
	}
	return res, nil
}

// ============ 部门管理 ============

func AdminDeptList(ctx context.Context) ([]model.Department, error) {
	var depts []model.Department
	err := store.DB.Order("sort asc, id asc").Find(&depts).Error
	return depts, err
}

func AdminDeptCreate(ctx context.Context, nameZh, nameEn string, parentID int64, sort int) (*model.Department, error) {
	d := &model.Department{ID: id.Next(), NameZh: nameZh, NameEn: nameEn, ParentID: parentID, Sort: sort}
	if err := store.DB.Create(d).Error; err != nil {
		return nil, err
	}
	return d, nil
}

func AdminDeptUpdate(ctx context.Context, id int64, nameZh, nameEn string, sort int) error {
	updates := map[string]interface{}{}
	if nameZh != "" {
		updates["name_zh"] = nameZh
	}
	if nameEn != "" {
		updates["name_en"] = nameEn
	}
	updates["sort"] = sort
	return store.DB.Model(&model.Department{}).Where("id = ?", id).Updates(updates).Error
}

func AdminDeptDelete(ctx context.Context, id int64) error {
	// 部门下有用户则禁止删除
	var cnt int64
	store.DB.Model(&model.User{}).Where("department_id = ?", id).Count(&cnt)
	if cnt > 0 {
		return &errs.Err{Code: 1001, Msg: "该部门下还有用户，无法删除"}
	}
	return store.DB.Delete(&model.Department{}, id).Error
}

// ============ 小程序管理（H5 容器） ============

func AdminAppList(ctx context.Context) ([]model.AppEntry, error) {
	var apps []model.AppEntry
	err := store.DB.Order("sort asc, id asc").Find(&apps).Error
	return apps, err
}

func AdminAppCreate(ctx context.Context, nameZh, nameEn, icon, url, category string, sort int, enabled bool) (*model.AppEntry, error) {
	a := &model.AppEntry{
		NameZh: nameZh, NameEn: nameEn, Icon: icon, URL: url,
		Category: category, Sort: sort,
		Enabled: map[bool]int{true: 1, false: 0}[enabled],
	}
	if err := store.DB.Create(a).Error; err != nil {
		return nil, err
	}
	return a, nil
}

func AdminAppUpdate(ctx context.Context, id int64, nameZh, nameEn, icon, url, category string, sort int, enabled bool) error {
	updates := map[string]interface{}{
		"name_zh": nameZh, "name_en": nameEn, "icon": icon, "url": url,
		"category": category, "sort": sort, "enabled": map[bool]int{true: 1, false: 0}[enabled],
	}
	return store.DB.Model(&model.AppEntry{}).Where("id = ?", id).Updates(updates).Error
}

func AdminAppDelete(ctx context.Context, id int64) error {
	return store.DB.Delete(&model.AppEntry{}, id).Error
}

// ============ 群组管理 ============

// AdminGroupOut 群组管理列表项：群信息 + 成员数（后台显示人数）+ 群主资料（头像/昵称/短ID）
type AdminGroupOut struct {
	model.Conversation
	MemberCount   int64  `json:"memberCount"`
	OwnerNickname string `json:"ownerNickname"`
	OwnerAvatar   string `json:"ownerAvatar"`
	OwnerShortID  string `json:"ownerShortId"`
}

// AdminGroupListResult 群组列表（分页）：与后台其它分页接口保持一致，返回 {list,total}。
// 注意：本接口早期直接返回裸数组（硬编码 Limit(200)），改成 {list,total} 是破坏性变更，
// 前端需从 res.data 改为 res.data.list + res.data.total。
type AdminGroupListResult struct {
	List  []AdminGroupOut `json:"list"`
	Total int64           `json:"total"`
}

// AdminGroupList 群组列表：kw 匹配群名（name_zh/name_en）或群主昵称/账号，page/size 分页。
func AdminGroupList(ctx context.Context, kw string, page, size int) (*AdminGroupListResult, error) {
	q := store.DB.Model(&model.Conversation{}).
		Where("type = ? AND status = ?", model.ConvGroup, model.ConvNormal)
	if kw != "" {
		like := "%" + kw + "%"
		// 群主命中：先查 user 表取候选 id（上限 200）再 owner_id IN，比直接 join 代价小
		var ownerIDs []int64
		if err := store.DB.Model(&model.User{}).
			Where("nickname LIKE ? OR account LIKE ?", like, like).
			Limit(200).Pluck("id", &ownerIDs).Error; err != nil {
			return nil, err
		}
		if len(ownerIDs) > 0 {
			q = q.Where("(name_zh LIKE ? OR name_en LIKE ? OR owner_id IN ?)", like, like, ownerIDs)
		} else {
			q = q.Where("(name_zh LIKE ? OR name_en LIKE ?)", like, like)
		}
	}
	var total int64
	// Count 必须早于 Find，否则会被 Find 的 offset/limit 污染
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 100 {
		size = 20
	}
	var groups []model.Conversation
	err := q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&groups).Error
	if err != nil {
		return nil, err
	}
	// 批量取群主用户资料（ownerId 去重）
	ownerIDs := make([]int64, 0, len(groups))
	seen := map[int64]bool{}
	for _, g := range groups {
		if g.OwnerID > 0 && !seen[g.OwnerID] {
			seen[g.OwnerID] = true
			ownerIDs = append(ownerIDs, g.OwnerID)
		}
	}
	ownerMap := map[int64]model.User{}
	if len(ownerIDs) > 0 {
		var owners []model.User
		store.DB.Where("id IN ?", ownerIDs).Find(&owners)
		for _, u := range owners {
			ownerMap[u.ID] = u
		}
	}
	out := make([]AdminGroupOut, 0, len(groups))
	for _, g := range groups {
		var cnt int64
		store.DB.Model(&model.ConversationMember{}).Where("conversation_id = ?", g.ID).Count(&cnt)
		o := AdminGroupOut{Conversation: g, MemberCount: cnt}
		if ou, ok := ownerMap[g.OwnerID]; ok {
			o.OwnerNickname = ou.Nickname
			o.OwnerAvatar = ou.Avatar
			o.OwnerShortID = model.StrVal(ou.ShortID)
		}
		out = append(out, o)
	}
	return &AdminGroupListResult{List: out, Total: total}, nil
}

// ============ 会话/频道管理（通用列表） ============

// AdminConversationOut 会话轻量卡：后台「默认关注频道」下拉等场景使用。
type AdminConversationOut struct {
	ID          int64  `json:"id,string"`
	Name        string `json:"name"`
	Avatar      string `json:"avatar"`
	Type        int    `json:"type"`
	Status      int    `json:"status"`
	MemberCount int64  `json:"memberCount"`
	ShortID     string `json:"shortId"` // 自定义唯一 ID（未设置为空串）
}

type AdminConversationListResult struct {
	List  []AdminConversationOut `json:"list"`
	Total int64                  `json:"total"`
}

// AdminConversationList 通用会话列表：type>0 时按类型过滤（3=频道），kw 匹配名称，
// page/size 分页（size 上限 200，供后台下拉一次拉全量）。与 AdminGroupList 独立，互不影响。
func AdminConversationList(ctx context.Context, typ int, kw string, page, size int) (*AdminConversationListResult, error) {
	q := store.DB.Model(&model.Conversation{}).
		Where("status = ?", model.ConvNormal)
	if typ > 0 {
		q = q.Where("type = ?", typ)
	}
	if kw != "" {
		like := "%" + kw + "%"
		q = q.Where("name_zh LIKE ? OR name_en LIKE ?", like, like)
	}
	var total int64
	// Count 必须早于 Find，否则会被 Find 的 offset/limit 污染
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	var convs []model.Conversation
	if err := q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&convs).Error; err != nil {
		return nil, err
	}
	// 批量取成员数（GROUP BY 一次查询，避免逐行 COUNT）
	ids := make([]int64, 0, len(convs))
	for _, cv := range convs {
		ids = append(ids, cv.ID)
	}
	cntMap := map[int64]int64{}
	if len(ids) > 0 {
		var rows []struct {
			ConversationID int64
			Cnt            int64
		}
		store.DB.Model(&model.ConversationMember{}).
			Select("conversation_id, COUNT(*) AS cnt").
			Where("conversation_id IN ?", ids).
			Group("conversation_id").Scan(&rows)
		for _, r := range rows {
			cntMap[r.ConversationID] = r.Cnt
		}
	}
	out := make([]AdminConversationOut, 0, len(convs))
	for _, cv := range convs {
		name := cv.NameZh
		if name == "" {
			name = cv.NameEn
		}
		out = append(out, AdminConversationOut{
			ID:          cv.ID,
			Name:        name,
			Avatar:      cv.Avatar,
			Type:        cv.Type,
			Status:      cv.Status,
			MemberCount: cntMap[cv.ID],
			ShortID:     model.StrVal(cv.ShortID),
		})
	}
	return &AdminConversationListResult{List: out, Total: total}, nil
}

// ErrGroupMaxMembersTooSmall 群人数上限小于当前成员数
var ErrGroupMaxMembersTooSmall = &errs.Err{Code: 400, Msg: "人数上限不能小于当前成员数"}

// ErrGroupNewOwnerNotMember 指定的新群主不是该群成员
var ErrGroupNewOwnerNotMember = &errs.Err{Code: 400, Msg: "新群主不是该群成员"}

// ErrGroupAlreadyOwner 指定的新群主已经是当前群主
var ErrGroupAlreadyOwner = &errs.Err{Code: 400, Msg: "该用户已是群主"}

// AdminGroupUpdate 编辑群资料：只更新显式传入的字段（指针为 nil 表示不改）。
// name 同时写 name_zh/name_en，announcement 同时写 announcement_zh/announcement_en。
// maxMembers: 0 表示不限；>0 且小于当前成员数则返回 ErrGroupMaxMembersTooSmall。
func AdminGroupUpdate(ctx context.Context, id int64, name, avatar, announcement *string, maxMembers *int) error {
	if id <= 0 {
		return errs.ParamError
	}
	var conv model.Conversation
	// 群不存在 → 404
	if err := store.DB.Where("id = ?", id).First(&conv).Error; err != nil {
		return errs.NotFound
	}
	updates := map[string]any{}
	if name != nil {
		updates["name_zh"] = *name
		updates["name_en"] = *name
	}
	if avatar != nil {
		updates["avatar"] = *avatar
	}
	if announcement != nil {
		updates["announcement_zh"] = *announcement
		updates["announcement_en"] = *announcement
	}
	if maxMembers != nil {
		if *maxMembers < 0 {
			return errs.ParamError
		}
		if *maxMembers > 0 {
			var cnt int64
			if err := store.DB.Model(&model.ConversationMember{}).
				Where("conversation_id = ?", id).Count(&cnt).Error; err != nil {
				return err
			}
			if int64(*maxMembers) < cnt {
				return ErrGroupMaxMembersTooSmall
			}
		}
		updates["max_members"] = *maxMembers
	}
	if len(updates) == 0 {
		return nil
	}
	return store.DB.Model(&model.Conversation{}).Where("id = ?", id).Updates(updates).Error
}

// AdminGroupTransferOwner 转移群主：事务内 原群主降为普通成员 → 新群主提为 owner → 改 conversation.owner_id
func AdminGroupTransferOwner(ctx context.Context, id, newOwnerID int64) error {
	if id <= 0 || newOwnerID <= 0 {
		return errs.ParamError
	}
	var conv model.Conversation
	if err := store.DB.Where("id = ?", id).First(&conv).Error; err != nil {
		return errs.NotFound
	}
	if conv.OwnerID == newOwnerID {
		return ErrGroupAlreadyOwner
	}
	var cnt int64
	if err := store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", id, newOwnerID).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt == 0 {
		return ErrGroupNewOwnerNotMember
	}
	return store.DB.Transaction(func(tx *gorm.DB) error {
		// 原群主降级为普通成员
		if err := tx.Model(&model.ConversationMember{}).
			Where("conversation_id = ? AND user_id = ?", id, conv.OwnerID).
			Update("role", model.MemberNormal).Error; err != nil {
			return err
		}
		// 新群主提为 owner（原本是 admin 也一并提为 owner）
		if err := tx.Model(&model.ConversationMember{}).
			Where("conversation_id = ? AND user_id = ?", id, newOwnerID).
			Update("role", model.MemberOwner).Error; err != nil {
			return err
		}
		return tx.Model(&model.Conversation{}).Where("id = ?", id).
			Update("owner_id", newOwnerID).Error
	})
}

func AdminGroupDisband(ctx context.Context, id int64) error {
	store.DB.Model(&model.Conversation{}).Where("id = ?", id).Update("status", model.ConvDisband)
	return store.DB.Where("conversation_id = ?", id).Delete(&model.ConversationMember{}).Error
}

// ============ 消息查询（后台审计） ============

type AdminMsgQuery struct {
	ConvID int64 `json:"convId,string"`
	UserID int64 `json:"userId,string"`
	// UserKw 发送者关键字：用户 ID / 昵称 / 账号 / 手机号。纯数字时直接当 userID 用。
	UserKw string `json:"userKw"`
	Kw     string `json:"kw"`
	From   int64  `json:"from"` // unix ms
	To     int64  `json:"to"`
	Type   int    `json:"type"` // 消息类型筛选（1文本 2图片 3文件 4语音 5视频 6系统 7通话 8红包 9转账 10名片）
}

// AdminMessageOut 后台消息列表项：消息本体 + 发送者/接收者/会话冗余信息（前端直接渲染头像昵称）
type AdminMessageOut struct {
	model.Message
	// 发送者（senderId 为 -1 即小助手，昵称头像取后台助手配置）
	SenderName    string `json:"senderName"`
	SenderAvatar  string `json:"senderAvatar"`
	SenderShortID string `json:"senderShortId"`
	// 接收者：单聊为对方用户，群聊/频道为会话本身
	ReceiverID      string `json:"receiverId"`
	ReceiverName    string `json:"receiverName"`
	ReceiverAvatar  string `json:"receiverAvatar"`
	ReceiverShortID string `json:"receiverShortId"`
	// 会话冗余
	ConvType   int    `json:"convType"`
	ConvName   string `json:"convName"`
	ConvAvatar string `json:"convAvatar"`
}

// resolveMsgSenderIDs 解析消息查询的发送者筛选条件，返回候选 userID 列表（空切片=不过滤）。
// userKw 为纯数字时直接当 userID 用（与 userId 行为一致）；否则按
// nickname LIKE / account = / phone = 查 user 表，上限 200 个，
// 避免昵称宽匹配导致 Mongo 的 $in 过大。
func resolveMsgSenderIDs(ctx context.Context, q *AdminMsgQuery) []int64 {
	if q.UserID > 0 {
		return []int64{q.UserID}
	}
	kw := strings.TrimSpace(q.UserKw)
	if kw == "" {
		return nil
	}
	if num, err := strconv.ParseInt(kw, 10, 64); err == nil && num > 0 {
		return []int64{num}
	}
	var ids []int64
	store.DB.Model(&model.User{}).
		Where("nickname LIKE ? OR account = ? OR phone = ?", "%"+kw+"%", kw, kw).
		Limit(200).Pluck("id", &ids)
	return ids
}

func AdminMessageQuery(ctx context.Context, q *AdminMsgQuery, page, size int) ([]AdminMessageOut, int64, error) {
	filter := bson.M{}
	if q.ConvID > 0 {
		filter["conversation_id"] = q.ConvID
	}
	// 发送者筛选：userId 优先；否则 userKw（纯数字直接当 userID，其余解析成候选 userID 列表）
	senderIDs := resolveMsgSenderIDs(ctx, q)
	if len(senderIDs) > 0 {
		filter["sender_id"] = bson.M{"$in": senderIDs}
	} else if strings.TrimSpace(q.UserKw) != "" {
		// 关键字一个用户都没匹配上：明确返回空列表，绝不能退化成查全站消息
		return []AdminMessageOut{}, 0, nil
	} else {
		// 只显示用户/小助手发送的消息，过滤 sender_id=0 的系统通知（后台审计无意义）
		filter["sender_id"] = bson.M{"$ne": 0}
	}
	if q.Type > 0 {
		filter["type"] = q.Type
	}
	if q.Kw != "" {
		// 转义正则特殊字符，避免非法 regex 报错
		filter["content"] = bson.M{"$regex": regexp.QuoteMeta(q.Kw), "$options": "i"}
	}
	if q.From > 0 || q.To > 0 {
		t := bson.M{}
		if q.From > 0 {
			t["$gte"] = time.UnixMilli(q.From)
		}
		if q.To > 0 {
			t["$lte"] = time.UnixMilli(q.To)
		}
		filter["created_at"] = t
	}
	if size <= 0 || size > 100 {
		size = 20
	}
	if page <= 0 {
		page = 1
	}
	total, _ := msgColl().CountDocuments(ctx, filter)
	cur, err := msgColl().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "msg_id", Value: -1}}).
			SetSkip(int64((page-1)*size)).SetLimit(int64(size)))
	if err != nil {
		return nil, 0, err
	}
	var msgs []model.Message
	if err := cur.All(ctx, &msgs); err != nil {
		return nil, 0, err
	}

	out := make([]AdminMessageOut, 0, len(msgs))
	if len(msgs) == 0 {
		return out, total, nil
	}

	// ---- 批量取会话 ----
	convIDs := make([]int64, 0, len(msgs))
	seen := map[int64]bool{}
	for _, m := range msgs {
		if !seen[m.ConversationID] {
			seen[m.ConversationID] = true
			convIDs = append(convIDs, m.ConversationID)
		}
	}
	var convs []model.Conversation
	store.DB.Where("id IN ?", convIDs).Find(&convs)
	convMap := map[int64]model.Conversation{}
	for _, cv := range convs {
		convMap[cv.ID] = cv
	}

	// ---- 批量取单聊成员（单聊接收者 = 发送者之外的另一个成员）----
	directIDs := make([]int64, 0)
	for _, cv := range convs {
		if cv.Type == model.ConvDirect {
			directIDs = append(directIDs, cv.ID)
		}
	}
	convOther := map[int64][]int64{} // convID -> 成员 userID 列表（单聊最多 2 个）
	if len(directIDs) > 0 {
		var mems []model.ConversationMember
		store.DB.Where("conversation_id IN ?", directIDs).Find(&mems)
		for _, mm := range mems {
			convOther[mm.ConversationID] = append(convOther[mm.ConversationID], mm.UserID)
		}
	}

	// ---- 批量取用户 ----
	userSet := map[int64]bool{}
	for _, m := range msgs {
		if m.SenderID > 0 {
			userSet[m.SenderID] = true
		}
		for _, uid := range convOther[m.ConversationID] {
			if uid > 0 {
				userSet[uid] = true
			}
		}
	}
	userIDs := make([]int64, 0, len(userSet))
	for uid := range userSet {
		userIDs = append(userIDs, uid)
	}
	userMap := map[int64]model.User{}
	if len(userIDs) > 0 {
		var users []model.User
		store.DB.Where("id IN ?", userIDs).Find(&users)
		for _, u := range users {
			userMap[u.ID] = u
		}
	}

	// ---- 小助手配置（昵称/头像/靓号）----
	ac := GetAssistantConfig(ctx, nil)

	fillUser := func(uid int64) (string, string, string) {
		if uid == -1 {
			return ac.Name, ac.Avatar, "10000"
		}
		u, ok := userMap[uid]
		if !ok {
			return fmt.Sprintf("用户%v", uid), "", ""
		}
		short := ""
		if u.ShortID != nil {
			short = *u.ShortID
		}
		return u.Nickname, u.Avatar, short
	}

	for _, m := range msgs {
		o := AdminMessageOut{Message: m}
		o.SenderName, o.SenderAvatar, o.SenderShortID = fillUser(m.SenderID)

		cv := convMap[m.ConversationID]
		o.ConvType = cv.Type
		// 群聊(2)与频道(3)：接收者都是会话本身（频道复用会话表，若也走单聊分支
		// 会因找不到「另一个成员」落进 fillUser(0) → 显示成「用户0」）
		if cv.Type == model.ConvGroup || cv.Type == model.ConvChannel {
			o.ReceiverID = strconv.FormatInt(m.ConversationID, 10)
			// 名称兜底链：NameZh → NameEn → 「频道#id / 群#id」，绝不再落回用户兜底
			o.ReceiverName = cv.NameZh
			if o.ReceiverName == "" {
				o.ReceiverName = cv.NameEn
			}
			if o.ReceiverName == "" {
				if cv.Type == model.ConvChannel {
					o.ReceiverName = fmt.Sprintf("频道#%d", m.ConversationID)
				} else {
					o.ReceiverName = fmt.Sprintf("群#%d", m.ConversationID)
				}
			}
			o.ReceiverAvatar = cv.Avatar
			o.ConvName = o.ReceiverName
			o.ConvAvatar = cv.Avatar
		} else if cv.ID == 0 {
			// 会话行查不到（已被物理清理等）：明确标注，而不是落进单聊分支显示「用户0」
			o.ReceiverID = strconv.FormatInt(m.ConversationID, 10)
			o.ReceiverName = fmt.Sprintf("会话#%d", m.ConversationID)
			o.ConvName = o.ReceiverName
		} else {
			// 单聊：接收者 = 发送者之外的另一个成员
			other := int64(0)
			for _, uid := range convOther[m.ConversationID] {
				if uid != m.SenderID {
					other = uid
					break
				}
			}
			o.ReceiverID = strconv.FormatInt(other, 10)
			o.ReceiverName, o.ReceiverAvatar, o.ReceiverShortID = fillUser(other)
			peerName, peerAvatar, _ := fillUser(other)
			o.ConvName = peerName
			o.ConvAvatar = peerAvatar
		}
		out = append(out, o)
	}
	return out, total, nil
}

// AdminMessageBlock 屏蔽/恢复屏蔽一条消息（后台审计）：blocked=true 后用户端历史/同步不再下发
func AdminMessageBlock(ctx context.Context, msgID int64, blocked bool) error {
	_, err := msgColl().UpdateOne(ctx, bson.M{"msg_id": msgID},
		bson.M{"$set": bson.M{"blocked": blocked}})
	return err
}

// ============ 数据统计 ============

func AdminStatsOverview(ctx context.Context) (map[string]interface{}, error) {
	var userTotal int64
	store.DB.Model(&model.User{}).Count(&userTotal)
	var online int64
	keys, _ := store.RDB.Keys(ctx, "online:*").Result()
	online = int64(len(keys))
	msgTotal, _ := msgColl().CountDocuments(ctx, bson.M{})
	// 存储估算：消息数 * 1KB + 会话数
	var convTotal int64
	store.DB.Model(&model.Conversation{}).Count(&convTotal)
	storageMB := msgTotal / 1000 // 粗略
	return map[string]interface{}{
		"userTotal": userTotal,
		"online":    online,
		"msgTotal":  msgTotal,
		"convTotal": convTotal,
		"storageMB": storageMB,
	}, nil
}

func AdminStatsMessages(ctx context.Context, days int) (map[string]interface{}, error) {
	if days <= 0 {
		days = 7
	}
	start := time.Now().AddDate(0, 0, -days)
	type dayCount struct {
		Day   string `json:"day" bson:"_id"`
		Count int64  `json:"count" bson:"count"`
	}
	pipe, err := msgColl().Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{"created_at": bson.M{"$gte": start}}}},
		bson.D{{Key: "$group", Value: bson.M{
			"_id":   bson.M{"$dateToString": bson.M{"format": "%Y-%m-%d", "date": "$created_at"}},
			"count": bson.M{"$sum": 1},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
	})
	if err != nil {
		return nil, err
	}
	var out []dayCount
	if err := pipe.All(ctx, &out); err != nil {
		return nil, err
	}
	return map[string]interface{}{"days": days, "series": out}, nil
}

// ============ 日志查询 ============

func AdminLogList(ctx context.Context, page, size int) ([]model.AdminLog, int64, error) {
	if size <= 0 || size > 100 {
		size = 20
	}
	if page <= 0 {
		page = 1
	}
	var total int64
	store.DB.Model(&model.AdminLog{}).Count(&total)
	var logs []model.AdminLog
	err := store.DB.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&logs).Error
	return logs, total, err
}

func AdminLoginLogList(ctx context.Context, page, size int) ([]model.LoginLog, int64, error) {
	if size <= 0 || size > 100 {
		size = 20
	}
	if page <= 0 {
		page = 1
	}
	var total int64
	store.DB.Model(&model.LoginLog{}).Count(&total)
	var logs []model.LoginLog
	err := store.DB.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&logs).Error
	return logs, total, err
}

// ============ 后台操作日志 ============

func AdminLog(ctx context.Context, adminID int64, action, target, ip string, detail interface{}) {
	detailJSON, _ := json.Marshal(detail)
	store.DB.Create(&model.AdminLog{
		AdminID: adminID, Action: action, Target: target, Detail: string(detailJSON), IP: ip, CreatedAt: time.Now(),
	})
}

// ============ 群组：成员 / 消息详情 ============

// groupMemberRoleText 群成员角色文案（前端可直接渲染，省掉一次映射）。
// role 本身仍是数字 1/2/3（model.MemberOwner/MemberAdmin/MemberNormal），roleText 只是冗余展示字段。
func groupMemberRoleText(role int) string {
	switch role {
	case model.MemberOwner:
		return "群主"
	case model.MemberAdmin:
		return "管理员"
	default:
		return "成员"
	}
}

// AdminGroupMembers 某群的成员列表（按 joinedAt 倒序）
func AdminGroupMembers(ctx context.Context, groupID int64, page, size int) ([]map[string]any, int64, error) {
	if groupID <= 0 {
		return nil, 0, errs.ParamError
	}
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = 50
	}
	var total int64
	if err := store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ?", groupID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var mems []model.ConversationMember
	if err := store.DB.Where("conversation_id = ?", groupID).
		Order("role asc, joined_at desc").
		Offset((page - 1) * size).Limit(size).Find(&mems).Error; err != nil {
		return nil, 0, err
	}
	// 关联用户信息
	uids := make([]int64, 0, len(mems))
	for _, m := range mems {
		uids = append(uids, m.UserID)
	}
	userMap := map[int64]model.User{}
	if len(uids) > 0 {
		var us []model.User
		store.DB.Where("id IN ?", uids).Find(&us)
		for i := range us {
			userMap[us[i].ID] = us[i]
		}
	}
	list := make([]map[string]any, 0, len(mems))
	for _, m := range mems {
		u := userMap[m.UserID]
		list = append(list, map[string]any{
			"id":             m.ID,
			"conversationId": m.ConversationID,
			"userId":         m.UserID,
			"role":           m.Role,
			"roleText":       groupMemberRoleText(m.Role),
			"memberNickname": m.Nickname,
			"mute":           m.Mute,
			"joinedAt":       m.JoinedAt,
			"account":        u.Account,
			"nickname":       u.Nickname,
			"avatar":         u.Avatar,
			"shortId":        model.StrVal(u.ShortID),
			"status":         u.Status,
		})
	}
	return list, total, nil
}

// AdminGroupMessages 某群的消息记录（Mongo 消息集合按 conversation_id）
func AdminGroupMessages(ctx context.Context, groupID int64, kw string, page, size int) ([]model.Message, int64, error) {
	if groupID <= 0 {
		return nil, 0, errs.ParamError
	}
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = 50
	}
	filter := bson.M{
		"conversation_id": groupID,
		// 与消息记录保持一致：不显示 sender_id=0 的系统消息，只显示用户/小助手消息
		"sender_id": bson.M{"$ne": 0},
	}
	if kw != "" {
		filter["content"] = bson.M{"$regex": regexp.QuoteMeta(kw), "$options": "i"}
	}
	total, err := msgColl().CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cur, err := msgColl().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "msg_id", Value: -1}}).
			SetSkip(int64((page-1)*size)).SetLimit(int64(size)))
	if err != nil {
		return nil, 0, err
	}
	var msgs []model.Message
	_ = cur.All(ctx, &msgs)
	return msgs, total, nil
}

// ============ 保留靓号 reserved_short_id ============

type ReservedListResult struct {
	List  []map[string]any `json:"list"`
	Total int64            `json:"total"`
}

// AdminReservedShortIDList 靓号列表（按状态/关键字/来源筛选）
func AdminReservedShortIDList(ctx context.Context, kw string, status, source int, page, size int) (*ReservedListResult, error) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = 20
	}
	q := store.DB.Model(&model.ReservedShortID{})
	if kw != "" {
		like := "%" + kw + "%"
		q = q.Where("short_id LIKE ? OR remark LIKE ?", like, like)
	}
	if status > 0 {
		q = q.Where("status = ?", status)
	}
	if source > 0 {
		q = q.Where("source = ?", source)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []model.ReservedShortID
	if err := q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, err
	}
	// 关联 used_by 对应的用户昵称/账号
	uids := make([]int64, 0)
	for _, r := range rows {
		if r.UsedBy > 0 {
			uids = append(uids, r.UsedBy)
		}
	}
	uMap := map[int64]model.User{}
	if len(uids) > 0 {
		var us []model.User
		store.DB.Where("id IN ?", uids).Find(&us)
		for i := range us {
			uMap[us[i].ID] = us[i]
		}
	}
	list := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		row := map[string]any{
			"id":        r.ID,
			"shortId":   r.ShortID,
			"source":    r.Source,
			"type":      r.Type,
			"status":    r.Status,
			"remark":    r.Remark,
			"price":     r.Price,
			"usedBy":    r.UsedBy,
			"usedAt":    r.UsedAt,
			"createdAt": r.CreatedAt,
		}
		if u, ok := uMap[r.UsedBy]; ok {
			row["userNickname"] = u.Nickname
			row["userAccount"] = u.Account
			row["userShortId"] = model.StrVal(u.ShortID)
		}
		list = append(list, row)
	}
	return &ReservedListResult{List: list, Total: total}, nil
}

// AdminReservedShortIDBatch 批量生成靓号（范围/手动列表/规则三种模式之一）
// typeID: 1 普通 / 2 豹子号 / 3 顺子号 / 4 VIP；小于 1 或大于 4 会被归一到 1
func AdminReservedShortIDBatch(ctx context.Context, from, to int64, list []string, prefix string, digits, count int, remark string, price float64, typeID int, source int) (int64, error) {
	if typeID < 1 || typeID > 4 {
		typeID = 1
	}
	now := time.Now()
	cnt := int64(0)
	// 模式 1：范围 [from, to]
	if from > 0 && to >= from {
		err := store.DB.Transaction(func(tx *gorm.DB) error {
			for sid := from; sid <= to; sid++ {
				s := strconv.FormatInt(sid, 10)
				var dup int64
				tx.Model(&model.ReservedShortID{}).Where("short_id = ?", s).Count(&dup)
				if dup > 0 {
					continue
				}
				r := model.ReservedShortID{
					ShortID:   s,
					Source:    source,
					Type:      typeID,
					Status:    model.ReservedShortIDOpen,
					Price:     price,
					Remark:    remark,
					CreatedAt: now,
				}
				if tx.Create(&r).Error == nil {
					cnt++
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		return cnt, nil
	}
	// 模式 2：手动列表
	if len(list) > 0 {
		err := store.DB.Transaction(func(tx *gorm.DB) error {
			for _, raw := range list {
				s := strings.TrimSpace(raw)
				if s == "" {
					continue
				}
				var dup int64
				tx.Model(&model.ReservedShortID{}).Where("short_id = ?", s).Count(&dup)
				if dup > 0 {
					continue
				}
				r := model.ReservedShortID{
					ShortID:   s,
					Source:    source,
					Type:      typeID,
					Status:    model.ReservedShortIDOpen,
					Price:     price,
					Remark:    remark,
					CreatedAt: now,
				}
				if tx.Create(&r).Error == nil {
					cnt++
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		return cnt, nil
	}
	// 模式 3：规则（prefix + digits 位，count 个随机）
	if digits > 0 && digits <= 16 && count > 0 && count <= 100000 {
		if prefix == "" {
			prefix = ""
		}
		randPool := "0123456789"
		err := store.DB.Transaction(func(tx *gorm.DB) error {
			for i := 0; i < count; i++ {
				suf := make([]byte, digits)
				for j := 0; j < digits; j++ {
					suf[j] = randPool[time.Now().UnixNano()%10] // 快速伪随机（靓号不需要加密安全）
					// 混洗一下避免同一毫秒碰撞
					time.Sleep(10 * time.Nanosecond)
				}
				s := prefix + string(suf)
				var dup int64
				tx.Model(&model.ReservedShortID{}).Where("short_id = ?", s).Count(&dup)
				if dup > 0 {
					continue
				}
				r := model.ReservedShortID{
					ShortID:   s,
					Source:    source,
					Type:      typeID,
					Status:    model.ReservedShortIDOpen,
					Price:     price,
					Remark:    remark,
					CreatedAt: now,
				}
				if tx.Create(&r).Error == nil {
					cnt++
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		return cnt, nil
	}
	return 0, errs.ParamError
}

// AdminReservedShortIDRemark 更新备注/价格/类型
func AdminReservedShortIDRemark(ctx context.Context, id int64, remark string, price float64, typeID int) error {
	if id <= 0 {
		return errs.ParamError
	}
	upd := map[string]interface{}{}
	if remark != "" {
		upd["remark"] = remark
	}
	if price > 0 {
		upd["price"] = price
	}
	if typeID >= 1 && typeID <= 4 {
		upd["type"] = typeID
	}
	if len(upd) == 0 {
		return nil
	}
	return store.DB.Model(&model.ReservedShortID{}).Where("id = ?", id).Updates(upd).Error
}

// AdminReservedShortIDFreeze 冻结/解冻（status=2 冻结，其他恢复 1 未分配；已分配 status=3 不允许改）
func AdminReservedShortIDFreeze(ctx context.Context, id int64, frozen bool) error {
	if id <= 0 {
		return errs.ParamError
	}
	var r model.ReservedShortID
	if err := store.DB.First(&r, id).Error; err != nil {
		return errs.ParamError
	}
	if r.Status == model.ReservedShortIDUsed {
		return &errs.Err{Code: 1001, Msg: "已被使用，无法冻结/解冻"}
	}
	status := model.ReservedShortIDOpen
	if frozen {
		status = model.ReservedShortIDFrozen
	}
	return store.DB.Model(&r).Update("status", status).Error
}

// AdminReservedShortIDDelete 删除（仅未分配/冻结；已分配不删）
func AdminReservedShortIDDelete(ctx context.Context, id int64) error {
	if id <= 0 {
		return errs.ParamError
	}
	var r model.ReservedShortID
	if err := store.DB.First(&r, id).Error; err != nil {
		return errs.ParamError
	}
	if r.Status == model.ReservedShortIDUsed {
		return &errs.Err{Code: 1001, Msg: "已被使用，无法删除"}
	}
	return store.DB.Delete(&r).Error
}

// AdminReservedShortIDAssign 把某条预留靓号分配给指定用户（事务 + 行锁）。
// 语义：
//   - reserved.status 必须 = 1（未分配；冻结/已用均不允许）；
//   - 用户原 short_id 若也命中 reserved_short_id 池 → 旧那条自动回收（status=1, used_by=0, used_at=NULL）；
//   - 新靓号若被其他用户占用（极端竞争）→ 回滚并返回错误；
//   - 分配成功后：reserved.status=3 used_by=userId used_at=now；users.short_id = r.ShortID。
//
// 返回：{ nickname, account, shortId, userId } 供前端即时刷新"绑定账号"列
func AdminReservedShortIDAssign(ctx context.Context, id, userID int64) (map[string]any, error) {
	if id <= 0 || userID <= 0 {
		return nil, errs.ParamError
	}
	// 前置：reserved 存在，状态检查/分支处理
	var r model.ReservedShortID
	if err := store.DB.First(&r, id).Error; err != nil {
		return nil, errs.ParamError
	}
	if r.Status == model.ReservedShortIDFrozen {
		return nil, &errs.Err{Code: 1001, Msg: "该靓号已冻结，请先解冻再分配"}
	}
	if r.Status == model.ReservedShortIDUsed && r.UsedBy == userID {
		// 幂等：已经分配给该用户，直接回 OK，不报错
		var u model.User
		_ = store.DB.Select("nickname, account, short_id").First(&u, userID).Error
		return map[string]any{
			"userId":   u.ID,
			"nickname": u.Nickname,
			"account":  u.Account,
			"shortId":  r.ShortID,
		}, nil
	}
	// 走到这里允许：status=1(未分配) 或 status=3(已占用，管理员强行改分配)
	// 前置：用户存在
	var user model.User
	if err := store.DB.First(&user, userID).Error; err != nil {
		return nil, &errs.Err{Code: 1001, Msg: "用户不存在"}
	}
	newSID := strings.TrimSpace(r.ShortID)
	if newSID == "" {
		return nil, &errs.Err{Code: 1001, Msg: "靓号内容为空"}
	}

	err := store.DB.Transaction(func(tx *gorm.DB) error {
		// 固定顺序：1) reserved 行锁  2) user(新) 行锁  3) user(旧占用者) 行锁 if any，避免死锁
		var lockedR model.ReservedShortID
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&lockedR, id).Error; err != nil {
			return err
		}
		var lockedU model.User
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&lockedU, userID).Error; err != nil {
			return err
		}
		if lockedR.Status == model.ReservedShortIDFrozen {
			return &errs.Err{Code: 1001, Msg: "分配失败：该靓号状态已变更（冻结），请刷新后重试"}
		}
		// lockedR 允许 status=1 或 status=3；status=3 必须先处理旧占用者：
		//   1. 查旧占用者 used_by（=lockedR.UsedBy）；
		//   2. 如果旧占用者 user 的 short_id 仍等于 lockedR.ShortID → 置空；
		//   3. 如果旧占用者 == 新用户（lockedU），其实属于幂等情况（上面已 return），不会再到这里。
		if lockedR.Status == model.ReservedShortIDUsed && lockedR.UsedBy > 0 && lockedR.UsedBy != lockedU.ID {
			var oldU model.User
			err := tx.Set("gorm:query_option", "FOR UPDATE").First(&oldU, lockedR.UsedBy).Error
			if err == nil && strings.TrimSpace(model.StrVal(oldU.ShortID)) == newSID {
				if err := tx.Model(&model.User{}).
					Where("id = ?", oldU.ID).
					Update("short_id", nil).Error; err != nil {
					return err
				}
			}
			// oldU 已被删（err == RecordNotFound）则忽略，继续分配
		}

		// Step 1：释放用户原有旧 short_id（如果也在 reserved 池里）
		oldSID := strings.TrimSpace(model.StrVal(lockedU.ShortID))
		if oldSID != "" {
			var oldRS model.ReservedShortID
			if err := tx.Where("short_id = ? AND used_by = ?", oldSID, lockedU.ID).First(&oldRS).Error; err == nil {
				if err := tx.Model(&oldRS).Updates(map[string]any{
					"status":  model.ReservedShortIDOpen,
					"used_by": 0,
					"used_at": nil,
				}).Error; err != nil {
					return err
				}
			}
		}

		// Step 2：新靓号不能被其他用户已占用（极端竞争：AdminUserUpdate 绕过 assign 直接写 short_id 的情况）
		var conflict int64
		if err := tx.Model(&model.User{}).
			Where("short_id = ? AND id <> ?", newSID, lockedU.ID).
			Count(&conflict).Error; err != nil {
			return err
		}
		if conflict > 0 {
			return &errs.Err{Code: 1001, Msg: "该靓号已被其他用户占用，无法分配"}
		}

		// Step 3：写入用户 short_id
		if err := tx.Model(&model.User{}).
			Where("id = ?", lockedU.ID).
			Update("short_id", newSID).Error; err != nil {
			return err
		}

		// Step 4：标记 reserved 已被使用
		now := time.Now()
		if err := tx.Model(&model.ReservedShortID{}).
			Where("id = ?", lockedR.ID).
			Updates(map[string]any{
				"status":  model.ReservedShortIDUsed,
				"used_by": lockedU.ID,
				"used_at": &now,
			}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var latest model.User
	_ = store.DB.Select("id, nickname, account, short_id").First(&latest, userID).Error
	return map[string]any{
		"userId":   latest.ID,
		"nickname": latest.Nickname,
		"account":  latest.Account,
		"shortId":  model.StrVal(latest.ShortID),
	}, nil
}

// AdminReservedShortIDRelieve 解除分配：把已被占用的靓号回收为"未分配"，同时清空对应用户的 short_id。
// 约束：reserved 必须是 status=3 且 used_by>0；只有当 users.short_id 仍等于 reserved.short_id 时才清空（避免误覆盖用户后续手工改好的新值）
func AdminReservedShortIDRelieve(ctx context.Context, id int64) error {
	if id <= 0 {
		return errs.ParamError
	}
	var r model.ReservedShortID
	if err := store.DB.First(&r, id).Error; err != nil {
		return errs.ParamError
	}
	if r.Status != model.ReservedShortIDUsed || r.UsedBy <= 0 {
		return &errs.Err{Code: 1001, Msg: "该靓号当前未被分配，无需解除"}
	}
	return store.DB.Transaction(func(tx *gorm.DB) error {
		// 1) reserved 行锁，2) user 行锁
		var lockedR model.ReservedShortID
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&lockedR, id).Error; err != nil {
			return err
		}
		var lockedU model.User
		err := tx.Set("gorm:query_option", "FOR UPDATE").First(&lockedU, lockedR.UsedBy).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}

		// 回收 reserved
		if err := tx.Model(&model.ReservedShortID{}).
			Where("id = ?", lockedR.ID).
			Updates(map[string]any{
				"status":  model.ReservedShortIDOpen,
				"used_by": 0,
				"used_at": nil,
			}).Error; err != nil {
			return err
		}
		// 仅当用户 short_id 仍等于这条才清空（防止用户在此期间被手工改过）
		if err == nil && strings.TrimSpace(model.StrVal(lockedU.ShortID)) == strings.TrimSpace(lockedR.ShortID) {
			if err := tx.Model(&model.User{}).
				Where("id = ?", lockedU.ID).
				Update("short_id", nil).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ============ 系统健康检测 ============

// procStartedAt API 进程启动时间（包初始化时记录，用于运行时长展示）
var procStartedAt = time.Now()

// hcCheck 单项检测结果：前端系统检测页直接消费
// status: ok=正常 warn=警告 err=异常
type hcCheck struct {
	Status    string            `json:"status"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	LatencyMs int64             `json:"latencyMs"`
}

// AdminHealthCheck 单项检测；key=all 时返回全部。
// 检测项：mysql / redis / mongo / minio / api / wss / jpush / version
// 全部基于真实连接探测（.env 配置已在进程启动时加载进 cfg / store）。
func AdminHealthCheck(ctx context.Context, cfg *config.Config, key string) (map[string]any, error) {
	checks := map[string]func() hcCheck{
		"mysql": func() hcCheck {
			sqlDB, e := store.DB.DB()
			if e != nil {
				return hcCheck{"err", "MySQL 连接池获取失败: " + e.Error(), nil, 0}
			}
			if e := sqlDB.PingContext(ctx); e != nil {
				return hcCheck{"err", "MySQL 连接失败: " + e.Error(), nil, 0}
			}
			var v string
			store.DB.Raw("SELECT VERSION()").Scan(&v)
			st := sqlDB.Stats()
			return hcCheck{"ok", "连接正常", map[string]string{
				"版本": v, "活跃连接": fmt.Sprintf("%d", st.InUse), "空闲连接": fmt.Sprintf("%d", st.Idle),
			}, 0}
		},
		"redis": func() hcCheck {
			if store.RDB == nil {
				return hcCheck{"err", "Redis 未初始化", nil, 0}
			}
			if e := store.RDB.Ping(ctx).Err(); e != nil {
				return hcCheck{"err", "Redis 连接失败: " + e.Error(), map[string]string{"地址": cfg.RedisAddr}, 0}
			}
			return hcCheck{"ok", "连接正常", map[string]string{"地址": cfg.RedisAddr}, 0}
		},
		"mongo": func() hcCheck {
			if store.Mongo == nil {
				return hcCheck{"err", "MongoDB 未初始化", nil, 0}
			}
			if e := store.Mongo.Client().Ping(ctx, nil); e != nil {
				return hcCheck{"err", "MongoDB 连接失败: " + e.Error(), nil, 0}
			}
			return hcCheck{"ok", "连接正常", map[string]string{"数据库": store.Mongo.Name()}, 0}
		},
		"minio": func() hcCheck {
			return checkMinio(ctx, cfg)
		},
		"api": func() hcCheck {
			// 能响应本次请求即代表 API 进程在线
			return hcCheck{"ok", "API 服务在线", map[string]string{
				"NodeID": cfg.NodeID,
				"HTTP端口": cfg.HTTPPort,
				"启动时间":   procStartedAt.Format("2006-01-02 15:04:05"),
				"已运行":    time.Since(procStartedAt).Round(time.Second).String(),
				"Go版本":   runtime.Version(),
				"运行环境":   cfg.AppEnv,
			}, 0}
		},
		"wss": func() hcCheck {
			// gateway 与 api 建议同机部署：探活本机 WS 端口最直接
			addr := net.JoinHostPort("127.0.0.1", cfg.WSPort)
			d := net.Dialer{Timeout: 800 * time.Millisecond}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return hcCheck{"err", "WS 端口未监听（gateway 未启动或端口不一致）: " + err.Error(),
					map[string]string{"本机WS端口": cfg.WSPort}, 0}
			}
			conn.Close()
			online := "未知"
			if store.RDB != nil {
				if keys, e := store.RDB.Keys(ctx, "online:*").Result(); e == nil {
					online = fmt.Sprintf("%d", len(keys))
				}
			}
			return hcCheck{"ok", "Gateway 在线（本机节点）", map[string]string{
				"本机WS端口": cfg.WSPort, "在线连接": online,
			}, 0}
		},
		"jpush": func() hcCheck {
			c := GetJPushConfig(ctx)
			d := map[string]string{
				"启用":     fmt.Sprintf("%v", c.Enabled),
				"AppKey": c.AppKey,
				"APNs生产": fmt.Sprintf("%v", c.ApnsProduction),
			}
			if !c.Enabled {
				return hcCheck{"warn", "极光推送未启用（离线消息将走 App 内通知）", d, 0}
			}
			if c.AppKey == "" || c.MasterSecret == "" {
				return hcCheck{"err", "已启用但 AppKey / MasterSecret 未配置完整", d, 0}
			}
			if len(c.MasterSecret) < 8 {
				d["AppKey"] = d["AppKey"] + "（MasterSecret 长度异常，请检查）"
			} else {
				d["MasterSecret"] = strings.Repeat("*", 8) + "（已配置）"
			}
			return hcCheck{"ok", "推送配置完整（未做真实下发测试）", d, 0}
		},
		"version": func() hcCheck {
			return hcCheck{"ok", "环境配置（.env 已加载）", map[string]string{
				"NodeID":  cfg.NodeID,
				"运行环境":    cfg.AppEnv,
				"HTTP端口":  cfg.HTTPPort,
				"WS端口":    cfg.WSPort,
				"Redis":   cfg.RedisAddr,
				"MongoDB": cfg.MongoDB,
				"MinIO":   cfg.MinIOEndpoint,
				"JWT访问时长": fmt.Sprintf("%dh", cfg.JWTAccessTTLHours),
				"JWT刷新时长": fmt.Sprintf("%dd", cfg.JWTRefreshTTLDays),
				// 本区块展示的是环境变量原值，但 register_enabled / invite_code_enabled
				// 会被后台 sys_config 覆盖（见 auth.go Register），因此额外展示「生效值」，
				// 避免排查时把 env 值当成实际行为——正是这轮踩的坑。
				"开放注册":      fmt.Sprintf("%v", cfg.RegisterOn),
				"开放注册(生效)":  fmt.Sprintf("%v", boolVal(SysConfigGet(ctx, "register_enabled", cfg.RegisterOn))),
				"邀请码注册":     fmt.Sprintf("%v", cfg.InviteCodeOn),
				"邀请码注册(生效)": fmt.Sprintf("%v", boolVal(SysConfigGet(ctx, "invite_code_enabled", cfg.InviteCodeOn))),
			}, 0}
		},
	}
	run := func(k string) map[string]any {
		start := time.Now()
		var res hcCheck
		if fn, ok := checks[k]; ok {
			res = fn()
		} else {
			res = hcCheck{"err", "未知检测项: " + k, nil, 0}
		}
		res.LatencyMs = time.Since(start).Milliseconds()
		b, _ := json.Marshal(res)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "all" || key == "" {
		result := map[string]any{}
		for k := range checks {
			result[k] = run(k)
		}
		return result, nil
	}
	return map[string]any{key: run(key)}, nil
}

// checkMinio 真实连通性检测：GET /minio/health/live（1s 超时）+ 配置完整性。
// 读生效配置（sys_config 优先，env 兜底），与上传服务保持一致。
func checkMinio(ctx context.Context, cfg *config.Config) hcCheck {
	mc := MinioConfigGet(ctx, cfg)
	endpoint := mc.Endpoint
	if endpoint == "" {
		return hcCheck{"warn", "MinIO 未配置", nil, 0}
	}
	detail := map[string]string{
		"Endpoint": endpoint,
		"Bucket":   mc.Bucket,
	}
	if mc.Bucket == "" || mc.AccessKey == "" || mc.SecretKey == "" {
		return hcCheck{"err", "MinIO 配置不完整（缺少 bucket / accessKey / secretKey 之一）", detail, 0}
	}
	base := endpoint
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/minio/health/live", nil)
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return hcCheck{"err", "MinIO 健康接口不可达: " + err.Error(), detail, 0}
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return hcCheck{"err", fmt.Sprintf("MinIO 健康检查返回 %d", resp.StatusCode), detail, 0}
	}
	return hcCheck{"ok", "存储服务可访问", detail, 0}
}
