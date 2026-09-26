package service

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"
)

// GetProfile 我的资料
func GetProfile(ctx context.Context, userID int64) (*model.User, error) {
	var u model.User
	if err := store.DB.First(&u, userID).Error; err != nil {
		return nil, errs.Unauthorized
	}
	// 我的邀请码回退链：
	// 1) user.my_invite_code（后台直填）
	// 2) invite_code.used_by = 本人（注册时消费的码）
	// 3) invite_friend_code.friend_ids 包含本人（后台创建自定义码时把该用户 ID 关联进好友列表）
	//    friend_ids 是 JSON 数组字符串（如 ["123","456"]），用 "UID" 带引号匹配避免子串误伤
	if u.MyInviteCode == "" {
		var ic model.InviteCode
		if err := store.DB.Where("used_by = ?", userID).Order("id desc").First(&ic).Error; err == nil {
			u.MyInviteCode = ic.Code
		}
	}
	if u.MyInviteCode == "" {
		var ifc model.InviteFriendCode
		like := "%\"" + strconv.FormatInt(userID, 10) + "\"%"
		if err := store.DB.Where("enabled = 1 AND friend_ids LIKE ?", like).
			Order("id desc").First(&ifc).Error; err == nil {
			u.MyInviteCode = ifc.Code
		}
	}
	return &u, nil
}

type UpdateProfileReq struct {
	Nickname     string  `json:"nickname"`
	Avatar       string  `json:"avatar"`
	Signature    *string `json:"signature"` // 指针语义：传了就更新（允许清空为空串），不传不动
	DepartmentID int64   `json:"departmentId"`
}

// UpdateProfile 更新资料
func UpdateProfile(ctx context.Context, userID int64, req *UpdateProfileReq) error {
	updates := map[string]interface{}{}
	if req.Nickname != "" {
		updates["nickname"] = req.Nickname
	}
	if req.Avatar != "" {
		updates["avatar"] = req.Avatar
	}
	if req.Signature != nil {
		updates["signature"] = *req.Signature
	}
	if req.DepartmentID > 0 {
		updates["department_id"] = req.DepartmentID
	}
	if len(updates) == 0 {
		return nil
	}
	return store.DB.Model(&model.User{}).Where("id = ?", userID).Updates(updates).Error
}

// SearchUsers 搜索用户（全员可见：昵称/账号/邮箱/靓号/手机号）
// 后台管理员账号（role=admin）不对外暴露——不能被搜到/被加好友。
// 隐私开关（按**被搜索者**的设置过滤，与搜索者无关）：
//   - phone_searchable=0 的用户不会通过手机号被命中（堵「传手机号/号段即可判存在」的预言机）；
//   - short_id_searchable=0 的用户不会通过平台短号被命中（含纯数字精确命中分支）。
func SearchUsers(ctx context.Context, kw string) ([]model.User, error) {
	kw = strings.TrimSpace(kw)
	if kw == "" {
		return nil, nil
	}
	var users []model.User
	// 需求12：纯数字 → 优先按靓号 short_id 精确匹配（通过 ID 添加好友）
	if isNumeric(kw) {
		var byShort model.User
		if err := store.DB.
			Where("status = ? AND role <> ? AND short_id = ? AND short_id_searchable = 1", model.StatusNormal, model.RoleAdmin, kw).
			First(&byShort).Error; err == nil {
			return []model.User{byShort}, nil
		}
	}
	// 注意：short_id 也要 LIKE（比如搜 "1888" 能命中 short_id=18888 的用户）；
	// phone / short_id 两个 LIKE 分支各自由被搜索者的开关放行
	like := "%" + kw + "%"
	err := store.DB.
		Where("status = ? AND role <> ? AND (nickname LIKE ? OR account LIKE ? OR email LIKE ?"+
			" OR (short_id_searchable = 1 AND short_id LIKE ?)"+
			" OR (phone_searchable = 1 AND phone LIKE ?))",
			model.StatusNormal, model.RoleAdmin, like, like, like, like, like).
		Limit(50).Find(&users).Error
	return users, err
}

// isNumeric 是否纯数字（靓号 ID 判断）
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// SearchTargetCard 群/频道搜索结果卡（GET /user/search 纯数字关键词命中会话 ID 时返回）
type SearchTargetCard struct {
	Type        string `json:"type"`      // "group" / "channel"
	ID          int64  `json:"id,string"` // 会话 ID（雪花字符串，即群/频道 ID）
	Name        string `json:"name"`
	Avatar      string `json:"avatar"`
	MemberCount int64  `json:"memberCount"`
	IsPublic    bool   `json:"isPublic"` // 公开=可凭 ID 自助加入/关注；私密群仅展示（加入需被邀请），私密频道不返回
	ShortID     string `json:"shortId"`  // 频道自定义唯一 ID（kw 与 short_id 相等时命中；未设置为空串）
}

// SearchGroupsAndChannels 搜索群/频道，供用户搜索页「凭 ID 加入」使用；前端按 type 渲染后调
// 现有加入端点（群 POST /conversation/:id/join、频道 POST /channel/:id/follow），无需新端点。
// 两条命中路径（可同时返回）：
//  1. 纯数字 kw → 按会话 ID（雪花）精确匹配群（type=2）/频道（type=3）；
//  2. kw 与频道自定义唯一 ID（short_id）完全相等 → 精确命中该频道（第十六批新增）。
//
// 权限口径（不绕过现有加入门控）：
//   - 群：公开/私密都返回，isPublic=false 时前端应提示「私密群需邀请」而非调 join
//     （join 端点仍会按 qrJoinEnabled 强制拦截，双保险）；存在性暴露程度与
//     GET /conversation/:id/preview（任何登录用户可调）一致，无新增泄露；
//   - 频道：仅返回公开（qrJoinEnabled=1）——私密频道对非成员按不存在处理（对齐 ChannelInfo 口径）。
func SearchGroupsAndChannels(ctx context.Context, kw string) []SearchTargetCard {
	var cards []SearchTargetCard
	if isNumeric(kw) {
		if convID, err := strconv.ParseInt(kw, 10, 64); err == nil && convID > 0 {
			var conv model.Conversation
			if err := store.DB.Where("id = ? AND status = ? AND type IN ?",
				convID, model.ConvNormal, []int{model.ConvGroup, model.ConvChannel}).
				First(&conv).Error; err == nil {
				if conv.Type != model.ConvChannel || conv.QrJoinEnabled == 1 {
					cards = append(cards, convCard(&conv))
				}
			}
		}
	}
	// 频道自定义 ID 精确命中（kw 与 short_id 完全相等；私密频道搜不到）
	var sc model.Conversation
	if err := store.DB.Where("short_id = ? AND type = ? AND status = ?",
		kw, model.ConvChannel, model.ConvNormal).First(&sc).Error; err == nil && sc.QrJoinEnabled == 1 {
		dup := false
		for _, c := range cards {
			if c.ID == sc.ID {
				dup = true
				break
			}
		}
		if !dup {
			cards = append(cards, convCard(&sc))
		}
	}
	return cards
}

// convCard 由会话行组装搜索结果卡（含成员数与 shortId）
func convCard(conv *model.Conversation) SearchTargetCard {
	var cnt int64
	store.DB.Model(&model.ConversationMember{}).Where("conversation_id = ?", conv.ID).Count(&cnt)
	name := conv.NameZh
	if name == "" {
		name = conv.NameEn
	}
	t := "group"
	if conv.Type == model.ConvChannel {
		t = "channel"
	}
	return SearchTargetCard{
		Type: t, ID: conv.ID, Name: name, Avatar: conv.Avatar,
		MemberCount: cnt, IsPublic: conv.QrJoinEnabled == 1,
		ShortID: model.StrVal(conv.ShortID),
	}
}

// GetUserDetail 用户详情（内部调用用：返回完整 model.User，含手机/邮箱/余额等隐私字段，
// 严禁直接作为对外接口返回值——对外请使用 GetUserPublic）
func GetUserDetail(ctx context.Context, targetID int64) (*model.User, error) {
	var u model.User
	if err := store.DB.First(&u, targetID).Error; err != nil {
		return nil, &errs.Err{Code: 3001, Msg: "用户不存在"}
	}
	return &u, nil
}

// PublicUser 用户公开资料（GET /user/:id 对外返回的白名单结构）。
// 只保留可公开字段，剥离 email/balance/frozen/registerIp/lastLoginIp/
// myInviteCode/isGuest/guestDeviceId/registerDevice/lastLoginAt 等隐私/敏感字段。
//
// Phone 永远不是明文：按被查看者的 phone_visible 决定给「138****1234」式掩码还是
// 全遮「*******」（见 MaskPhone / PublicUserOfFor）。
type PublicUser struct {
	ID           int64     `json:"id,string"`
	Account      string    `json:"account"`
	Nickname     string    `json:"nickname"`
	Avatar       string    `json:"avatar"`
	ShortID      *string   `json:"shortId"`
	Signature    string    `json:"signature"`
	Phone        string    `json:"phone"`
	Role         int       `json:"role"` // 账号角色：1 普通用户 / 2 管理员 / 3 客服（前端 V 盾只对客服 role=3 显示）
	DepartmentID int64     `json:"departmentId,string"`
	CreatedAt    time.Time `json:"createdAt"`
}

// PhoneMasked 全遮形态：手机号不可见时统一显示这串星号（客户端直接展示）
const PhoneMasked = "*******"

// MaskPhone 把手机号打码为「前 3 后 4」形态（138****1234）；不足 8 位或为空无法安全
// 打码时返回全遮。入参原样返回空串（未绑定手机号就不展示该字段语义）。
func MaskPhone(p string) string {
	if p == "" {
		return ""
	}
	r := []rune(p)
	if len(r) < 8 {
		return PhoneMasked
	}
	return string(r[:3]) + "****" + string(r[len(r)-4:])
}

// isFriend a 与 b 是否互为好友（friend_relation 双向各存一条，任一方向命中即视为好友）。
// 调用方：CreateDirect 的好友门控（service/conversation.go）等单点判定场景；
// 批量场景请用 FriendIDSet 一次查出，避免逐个查库。
func isFriend(ctx context.Context, a, b int64) bool {
	if a <= 0 || b <= 0 || a == b {
		return false
	}
	var cnt int64
	store.DB.Model(&model.FriendRelation{}).
		Where("(user_id = ? AND friend_id = ?) OR (user_id = ? AND friend_id = ?)", a, b, b, a).
		Count(&cnt)
	return cnt > 0
}

// FriendIDSet 一次查出 uid 的全部好友 ID（批量场景用，避免逐个查库）
func FriendIDSet(ctx context.Context, uid int64) map[int64]struct{} {
	set := map[int64]struct{}{}
	if uid <= 0 {
		return set
	}
	var ids []int64
	store.DB.Model(&model.FriendRelation{}).Where("user_id = ?", uid).Pluck("friend_id", &ids)
	store.DB.Model(&model.FriendRelation{}).Where("friend_id = ?", uid).Pluck("user_id", &ids)
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// PublicUserOf 由 model.User 构造对外可见的公开资料（脱敏白名单唯一入口）。
// 任何把用户信息返回给「其他用户」的接口都应经此构造，禁止直接序列化 model.User
// （否则会泄露 balance/frozen/email/registerIP/lastLoginIP/myInviteCode/
// isGuest/guestDeviceId/registerDevice/lastLoginAt 等隐私字段）。
//
// 手机号：不知道查看者身份，按最严格档处理——phone_visible=all 给掩码，
// 其余一律全遮「*******」；需要「仅联系人可见」语义的接口请改用 PublicUserOfFor。
func PublicUserOf(u *model.User) *PublicUser {
	if u == nil {
		return nil
	}
	phone := PhoneMasked
	if u.PhoneVisible == "all" {
		phone = MaskPhone(u.Phone)
	}
	return &PublicUser{
		ID:           u.ID,
		Account:      u.Account,
		Nickname:     u.Nickname,
		Avatar:       u.Avatar,
		ShortID:      u.ShortID,
		Signature:    u.Signature,
		Phone:        phone,
		Role:         u.Role,
		DepartmentID: u.DepartmentID,
		CreatedAt:    u.CreatedAt,
	}
}

// PublicUserOfForSet 带查看者好友集合的公开资料构造（批量场景：好友集合由调用方
// 用 FriendIDSet 一次查出后传入，避免逐个查库）。可见性判定：
//   - phone_visible=all → 掩码 138****1234
//   - phone_visible=contacts → 查看者是本人或好友 → 掩码；否则全遮 *******
//   - 其他（nobody / 历史零值）→ 全遮 *******
func PublicUserOfForSet(viewerID int64, friends map[int64]struct{}, u *model.User) *PublicUser {
	p := PublicUserOf(u)
	if p == nil {
		return nil
	}
	if u.PhoneVisible == "all" || u.PhoneVisible == "contacts" {
		_, isFriendOfViewer := friends[u.ID]
		if viewerID == u.ID || isFriendOfViewer {
			p.Phone = MaskPhone(u.Phone)
		}
	}
	return p
}

// OnlineVisibleTo target 的在线状态对 viewer 是否可见（online_visible 三档：
// all=所有人 / contacts=仅联系人 / nobody=不公开；自己看自己不受限）。
// friends 为 viewer 的好友 ID 集合（FriendIDSet 产出），批量场景复用。
func OnlineVisibleTo(viewerID int64, friends map[int64]struct{}, target *model.User) bool {
	if target == nil {
		return false
	}
	if viewerID == target.ID {
		return true
	}
	switch target.OnlineVisible {
	case "nobody":
		return false
	case "contacts":
		_, ok := friends[target.ID]
		return ok
	}
	return true
}

// IsVipShortID 用户的短 ID 是否为后台靓号（reserved_short_id 池中已分配给该用户的号）
func IsVipShortID(ctx context.Context, userID int64, shortID *string) bool {
	if shortID == nil || *shortID == "" || userID <= 0 {
		return false
	}
	var cnt int64
	store.DB.Model(&model.ReservedShortID{}).
		Where("short_id = ? AND status = ? AND used_by = ?", *shortID, model.ReservedShortIDUsed, userID).
		Count(&cnt)
	return cnt > 0
}

// DeptTree 部门树（双语字段由客户端按语言取 nameZh/nameEn）
func DeptTree(ctx context.Context) ([]*model.Department, error) {
	var depts []*model.Department
	if err := store.DB.Order("sort asc, id asc").Find(&depts).Error; err != nil {
		return nil, err
	}
	return depts, nil
}

// DeptMembers 部门成员
func DeptMembers(ctx context.Context, deptID int64) ([]model.User, error) {
	var users []model.User
	err := store.DB.Where("department_id = ? AND status = ?", deptID, model.StatusNormal).
		Order("id asc").Find(&users).Error
	return users, err
}

// ============ 隐私设置（GET / PUT /api/v1/user/privacy）============

// ---- 高频路径开关读取（带 10s 进程内缓存）----
// typing / 已读上报是每帧/每消息级别的调用，不能逐帧查库；沿用 convInfo 的
// 10s 进程内缓存先例。开关改动最迟 10s 生效，对「隐私设置」这类低频变更完全够用。

type userFlagEntry struct {
	val    int
	expire time.Time
}

var (
	userFlagMu    sync.Mutex
	userFlagCache = map[string]userFlagEntry{}
	userFlagTTL   = 10 * time.Second
)

// UserFlagCached 读取 user 表的 0/1 开关列（typing_enabled / read_receipt_enabled）。
// 查询失败按 1（开）处理：开关链路故障时宁可多展示，也不静默吞掉既有功能。
// column 只允许传本文件内的字面量常量，不对外部输入开放。
func UserFlagCached(ctx context.Context, uid int64, column string) int {
	key := strconv.FormatInt(uid, 10) + "|" + column
	now := time.Now()
	userFlagMu.Lock()
	if e, ok := userFlagCache[key]; ok && now.Before(e.expire) {
		userFlagMu.Unlock()
		return e.val
	}
	userFlagMu.Unlock()
	var val int
	err := store.DB.Table("user").Select(column).Where("id = ?", uid).Scan(&val).Error
	if err != nil || (val != 0 && val != 1) {
		val = 1
	}
	userFlagMu.Lock()
	userFlagCache[key] = userFlagEntry{val: val, expire: now.Add(userFlagTTL)}
	userFlagMu.Unlock()
	return val
}

// 隐私设置

// 隐私字段取值约定（与客户端 l10n 对齐，见 im-app privacy_settings_page.dart）：
//   - 可见范围三档："all"（所有人）/ "contacts"（联系人）/ "nobody"（不公开）
//   - 开关类一律 bool；HTTP 层用 *bool / *string，nil = 不修改（非 nil 才写入，
//     这样「关掉」与「没传」可区分——不能学 UpdateProfile 的 != ""/​>0 判空，那会吞掉 false）

type PrivacySettings struct {
	Gender             string `json:"gender"`             // male / female / secret
	PhoneVisible       string `json:"phoneVisible"`       // all / contacts / nobody
	OnlineVisible      string `json:"onlineVisible"`      // all / contacts / nobody
	PhoneSearchable    bool   `json:"phoneSearchable"`    // 允许手机号搜索
	ShortIDSearchable  bool   `json:"shortIdSearchable"`  // 允许平台短号搜索
	ReadReceiptEnabled bool   `json:"readReceiptEnabled"` // 发送已读回执
	TypingEnabled      bool   `json:"typingEnabled"`      // 显示输入状态
}

// GetPrivacy 读我的隐私设置（未落库的列取 DB 默认值，由 model tag 的 default 保证）
func GetPrivacy(ctx context.Context, userID int64) (*PrivacySettings, error) {
	var u model.User
	if err := store.DB.Select("gender", "phone_visible", "online_visible",
		"phone_searchable", "short_id_searchable", "read_receipt_enabled", "typing_enabled").
		First(&u, userID).Error; err != nil {
		return nil, errs.Unauthorized
	}
	return &PrivacySettings{
		Gender:             u.Gender,
		PhoneVisible:       u.PhoneVisible,
		OnlineVisible:      u.OnlineVisible,
		PhoneSearchable:    u.PhoneSearchable == 1,
		ShortIDSearchable:  u.ShortIDSearchable == 1,
		ReadReceiptEnabled: u.ReadReceiptEnabled == 1,
		TypingEnabled:      u.TypingEnabled == 1,
	}, nil
}

// UpdatePrivacyReq 更新隐私设置：指针字段 nil = 不修改；非 nil 必须是合法取值。
type UpdatePrivacyReq struct {
	Gender             *string `json:"gender"`             // "male" / "female" / "secret" / ""（清空回 secret 语义由客户端定，服务端仅校验枚举）
	PhoneVisible       *string `json:"phoneVisible"`       // "all" / "contacts" / "nobody"
	OnlineVisible      *string `json:"onlineVisible"`      // "all" / "contacts" / "nobody"
	PhoneSearchable    *bool   `json:"phoneSearchable"`
	ShortIDSearchable  *bool   `json:"shortIdSearchable"`
	ReadReceiptEnabled *bool   `json:"readReceiptEnabled"`
	TypingEnabled      *bool   `json:"typingEnabled"`
}

var privacyVisValues = map[string]bool{"all": true, "contacts": true, "nobody": true}
var privacyGenderValues = map[string]bool{"male": true, "female": true, "secret": true}

// UpdatePrivacy 写我的隐私设置。只接受白名单列；布尔 false 必须能落库（这正是
// 全部用指针 + 判 nil 的原因，见 UpdatePrivacyReq 注释）。
func UpdatePrivacy(ctx context.Context, userID int64, req *UpdatePrivacyReq) error {
	updates := map[string]interface{}{}
	if req.Gender != nil {
		if !privacyGenderValues[*req.Gender] {
			return &errs.Err{Code: 1001, Msg: "gender 取值非法"}
		}
		updates["gender"] = *req.Gender
	}
	if req.PhoneVisible != nil {
		if !privacyVisValues[*req.PhoneVisible] {
			return &errs.Err{Code: 1001, Msg: "phoneVisible 取值非法"}
		}
		updates["phone_visible"] = *req.PhoneVisible
	}
	if req.OnlineVisible != nil {
		if !privacyVisValues[*req.OnlineVisible] {
			return &errs.Err{Code: 1001, Msg: "onlineVisible 取值非法"}
		}
		updates["online_visible"] = *req.OnlineVisible
	}
	if req.PhoneSearchable != nil {
		updates["phone_searchable"] = map[bool]int{true: 1, false: 0}[*req.PhoneSearchable]
	}
	if req.ShortIDSearchable != nil {
		updates["short_id_searchable"] = map[bool]int{true: 1, false: 0}[*req.ShortIDSearchable]
	}
	if req.ReadReceiptEnabled != nil {
		updates["read_receipt_enabled"] = map[bool]int{true: 1, false: 0}[*req.ReadReceiptEnabled]
	}
	if req.TypingEnabled != nil {
		updates["typing_enabled"] = map[bool]int{true: 1, false: 0}[*req.TypingEnabled]
	}
	if len(updates) == 0 {
		return nil
	}
	return store.DB.Model(&model.User{}).Where("id = ?", userID).Updates(updates).Error
}
