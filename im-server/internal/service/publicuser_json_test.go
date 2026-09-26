package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yourcompany/im-server/internal/model"
)

// fullUser 构造一个"每个隐私字段都填满"的用户，用来侦测脱敏白名单是否漏项。
func fullUser() *model.User {
	sid := "10086"
	now := time.Now()
	return &model.User{
		ID:             12345678901234,
		Account:        "alice",
		PasswordHash:   "$2a$10$password-hash-should-never-leak",
		TokenVersion:   3,
		PayPwdHash:     "$2a$10$paypwd-hash-should-never-leak",
		Nickname:       "Alice",
		Avatar:         "https://cdn.example.com/a.png",
		Signature:      "hello",
		Phone:          "13800000000",
		Email:          "alice@example.com",
		CountryCode:    "+86",
		ShortID:        &sid,
		Balance:        999.99,
		Frozen:         12.5,
		DepartmentID:   77,
		Status:         model.StatusNormal,
		Role:           model.RoleAdmin,
		MyInviteCode:   "INV123456",
		InvitedBy:      555,
		InvitedCode:    "INV999",
		RegisterIP:     "1.2.3.4",
		RegisterDevice: "Android-imei-xxxx",
		IsGuest:        1,
		GuestDeviceID:  "guest-device-abc",
		LastLoginIP:    "5.6.7.8",
		LastLoginAt:    &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// privacyKeys 禁止出现在 PublicUser JSON 里的字段。
// 注意 phone 已移出本名单：手机号现在以「掩码 138****1234 / 全遮 *******」形态
// 进入 PublicUser（见 PublicUserOf / PublicUserOfForSet），明文永不出境。
var privacyKeys = []string{
	"balance", "frozen", "email", "registerIP", "lastLoginIP",
	"myInviteCode", "isGuest", "guestDeviceId", "registerDevice", "lastLoginAt",
	"passwordHash", "payPwdHash", "tokenVersion", "countryCode", "invitedBy",
	"invitedCode", "status", "updatedAt",
}

// 群成员接口对外契约的允许字段（PublicUser 全部字段 + ConvMemberInfo 自有字段）
// 注意 accountRole 仅 ConvMemberInfo 输出（PublicUser 无此字段，下方 PublicUser 用例
// 因实际不出现而不受影响）；存在意义见 conversation.go ConvMemberInfo 注释。
var convMemberAllowedKeys = map[string]bool{
	// PublicUser
	"id": true, "account": true, "nickname": true, "avatar": true,
	"shortId": true, "signature": true, "phone": true, "departmentId": true, "createdAt": true,
	// ConvMemberInfo 自有
	"role": true, "accountRole": true, "remark": true, "vipShortId": true, "speakMutedUntil": true,
}

func mustHaveKeys(t *testing.T, m map[string]json.RawMessage, keys []string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("JSON 缺少必要字段 %q，实际字段: %v", k, keysOf(m))
		}
	}
}

func mustNotHaveKeys(t *testing.T, m map[string]json.RawMessage, keys []string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := m[k]; ok {
			t.Errorf("!! 隐私字段 %q 仍出现在 JSON 里: %s", k, v)
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPublicUserConvMemberInfoJSONContract 群成员列表（/conversation/:id/members 等）
// 对外契约：既要保留客户端依赖的字段，又绝不能带出隐私字段。
func TestPublicUserConvMemberInfoJSONContract(t *testing.T) {
	u := fullUser()
	info := ConvMemberInfo{
		PublicUser:      *PublicUserOf(u),
		Role:            1, // 群主
		Remark:          "备注名",
		VipShortID:      true,
		SpeakMutedUntil: 1700000000,
	}
	b, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal ConvMemberInfo 失败: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal 失败: %v", err)
	}

	mustHaveKeys(t, m, []string{
		"id", "nickname", "avatar", "account", "shortId",
		"role", "remark", "vipShortId", "speakMutedUntil",
	})
	mustNotHaveKeys(t, m, privacyKeys)

	// id 必须是 JSON 字符串（json:"id,string"），客户端按字符串解析雪花 ID
	var idStr string
	if err := json.Unmarshal(m["id"], &idStr); err != nil {
		t.Errorf("id 必须是 JSON 字符串，实际 %s（err=%v）", m["id"], err)
	} else if idStr != "12345678901234" {
		t.Errorf("id 值不保真: %s", idStr)
	}

	// role 必须是「群内角色」，不能被 PublicUser 的任何字段顶掉
	var role int
	if err := json.Unmarshal(m["role"], &role); err != nil {
		t.Errorf("role 不是数字: %s", m["role"])
	} else if role != 1 {
		t.Errorf("role 群内角色被顶掉: got %d want 1", role)
	}

	// 白名单外字段一律不允许出现（防未来新增字段时静默泄露）
	for k := range m {
		if !convMemberAllowedKeys[k] {
			t.Errorf("ConvMemberInfo JSON 出现白名单外字段 %q（值=%s）", k, m[k])
		}
	}
}

// TestPublicUserOfJSONWhitelistClean 是 /user/search、/friend/list、/friend/blacklist、
// handler.attachOnlineOne 共用的脱敏入口，必须只输出白名单字段。
func TestPublicUserOfJSONWhitelistClean(t *testing.T) {
	u := fullUser()
	pu := PublicUserOf(u)
	if pu == nil {
		t.Fatal("PublicUserOf 返回 nil")
	}
	b, err := json.Marshal(pu)
	if err != nil {
		t.Fatalf("marshal PublicUser 失败: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal 失败: %v", err)
	}

	mustHaveKeys(t, m, []string{"id", "account", "nickname", "avatar", "shortId", "signature", "phone", "departmentId", "createdAt"})
	mustNotHaveKeys(t, m, privacyKeys)

	// phone 永远不是明文：PhoneVisible 为零值（历史行为=nobody）时必须全遮
	if string(m["phone"]) != `"*******"` {
		t.Errorf("phone 应全遮为 *******，实际 %s", m["phone"])
	}

	for k := range m {
		if !convMemberAllowedKeys[k] {
			t.Errorf("PublicUser JSON 出现白名单外字段 %q（值=%s）", k, m[k])
		}
	}

	// id 字符串契约
	var idStr string
	if err := json.Unmarshal(m["id"], &idStr); err != nil {
		t.Errorf("PublicUser id 必须是 JSON 字符串，实际 %s", m["id"])
	}
}

// TestMaskPhone 掩码规则：保留前 3 后 4；过短/空串的兜底行为
func TestMaskPhone(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"13812345678", "138****5678"},
		{"+8613812345678", "+86****5678"},
		{"1234567", "*******"},
	}
	for _, c := range cases {
		if got := MaskPhone(c.in); got != c.want {
			t.Errorf("MaskPhone(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPublicUserOfPhoneVisibility 手机号可见范围三档的 PublicUserOf 行为
// （PublicUserOf 不知查看者身份，contacts 档按最严格处理，真正放行在 PublicUserOfForSet）
func TestPublicUserOfPhoneVisibility(t *testing.T) {
	u := fullUser()
	// all → 掩码
	u.PhoneVisible = "all"
	if got := PublicUserOf(u).Phone; got != "138****0000" {
		t.Errorf("phone_visible=all 应给掩码 138****0000，实际 %q", got)
	}
	// contacts → 保守全遮（查看者未知）
	u.PhoneVisible = "contacts"
	if got := PublicUserOf(u).Phone; got != PhoneMasked {
		t.Errorf("phone_visible=contacts 无查看者语境应全遮，实际 %q", got)
	}
	// contacts + 好友语境（PublicUserOfForSet）→ 掩码
	friends := map[int64]struct{}{u.ID: {}}
	if got := PublicUserOfForSet(1, friends, u).Phone; got != "138****0000" {
		t.Errorf("contacts+好友应给掩码，实际 %q", got)
	}
	// contacts + 非好友 → 全遮
	if got := PublicUserOfForSet(1, nil, u).Phone; got != PhoneMasked {
		t.Errorf("contacts+非好友应全遮，实际 %q", got)
	}
	// nobody / 零值 → 全遮
	for _, v := range []string{"nobody", ""} {
		u.PhoneVisible = v
		if got := PublicUserOf(u).Phone; got != PhoneMasked {
			t.Errorf("phone_visible=%q 应全遮，实际 %q", v, got)
		}
	}
}

func TestPublicUserOfNilAndFieldMapping(t *testing.T) {
	if PublicUserOf(nil) != nil {
		t.Error("PublicUserOf(nil) 应返回 nil")
	}
	u := fullUser()
	pu := PublicUserOf(u)
	if pu.ID != u.ID || pu.Account != u.Account || pu.Nickname != u.Nickname ||
		pu.Avatar != u.Avatar || pu.Signature != u.Signature ||
		pu.DepartmentID != u.DepartmentID || pu.CreatedAt != u.CreatedAt {
		t.Error("PublicUserOf 字段映射不一致")
	}
	if pu.ShortID == nil || *pu.ShortID != *u.ShortID {
		t.Error("ShortID 指针未正确传递")
	}
	// 原用户对象不应被修改
	if u.Balance != 999.99 || u.Phone != "13800000000" {
		t.Error("PublicUserOf 不应修改入参")
	}
}

// TestPublicUserBlacklistShape 黑名单列表返回 []PublicUser，逐元素 marshal 后也不得含隐私字段。
func TestPublicUserBlacklistShape(t *testing.T) {
	users := []model.User{*fullUser(), *fullUser()}
	users[1].ID = 999
	out := make([]PublicUser, 0, len(users))
	for i := range users {
		out = append(out, *PublicUserOf(&users[i]))
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal []PublicUser 失败: %v", err)
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal 失败: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("期望 2 个元素，实际 %d", len(arr))
	}
	for i, m := range arr {
		mustNotHaveKeys(t, m, privacyKeys)
		for k := range m {
			if !convMemberAllowedKeys[k] {
				t.Errorf("黑名单第 %d 项出现白名单外字段 %q", i, k)
			}
		}
	}
}
