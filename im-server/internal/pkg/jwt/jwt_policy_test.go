package jwt

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const policyTestSecret = "unit-test-secret-do-not-use-in-prod"

// decodePayload 解出 JWT 中间那段 base64url payload（不校验签名，只看原始声明）
func decodePayload(t *testing.T, token string) map[string]json.RawMessage {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token 结构不是三段: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("payload base64 解码失败: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload 不是 JSON: %v (%s)", err, raw)
	}
	return m
}

func TestJwtPolicy_RoundTrip(t *testing.T) {
	tok, err := Generate(policyTestSecret, 778899, 2, 1, 7)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	c, err := Parse(policyTestSecret, tok)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if c.UserID != 778899 {
		t.Errorf("UserID 不保真: got %d want 778899", c.UserID)
	}
	if c.Role != 2 {
		t.Errorf("Role 不保真: got %d want 2", c.Role)
	}
	if c.Ver != 7 {
		t.Errorf("Ver 不保真: got %d want 7", c.Ver)
	}
}

func TestJwtPolicy_VerNonZeroPresentInPayload(t *testing.T) {
	tok, err := Generate(policyTestSecret, 1, 1, 1, 7)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	m := decodePayload(t, tok)
	raw, ok := m["v"]
	if !ok {
		t.Fatalf("ver=7 时 payload 应含 v 字段，实际: %v", m)
	}
	if string(raw) != "7" {
		t.Errorf("v 值不是 7: %s", raw)
	}
}

// TestJwtPolicy_VerZeroOmittedAndParsesAsZero 是"上线后存量 token 不集体失效"的关键回归：
// 1) DB 里存量用户 token_version 默认 0；
// 2) 存量 token 里没有 v 声明 → Parse 出来 Ver 必须为 0 → 与 DB 0 相等 → 不 401；
// 3) 同时因为 omitempty，新签发的 ver=0 token 也不应写 v，保持 payload 与旧 token 完全一致。
func TestJwtPolicy_VerZeroOmittedAndParsesAsZero(t *testing.T) {
	tok, err := Generate(policyTestSecret, 42, 1, 1, 0)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	m := decodePayload(t, tok)
	if v, ok := m["v"]; ok {
		t.Errorf("ver=0 时 payload 不应含 v 字段（omitempty 失效），实际 v=%s，完整 payload=%v", v, m)
	}
	c, err := Parse(policyTestSecret, tok)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if c.Ver != 0 {
		t.Errorf("ver=0 签发的 token Parse 出 Ver=%d，应为 0（否则存量用户上线即全员 401）", c.Ver)
	}
}

func TestJwtPolicy_GenerateForDeviceKeepsDeviceID(t *testing.T) {
	tok, err := GenerateForDevice(policyTestSecret, 1001, 1, 720, 3, "device-abc")
	if err != nil {
		t.Fatalf("GenerateForDevice 失败: %v", err)
	}
	c, err := Parse(policyTestSecret, tok)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if c.UserID != 1001 || c.Role != 1 || c.Ver != 3 {
		t.Errorf("声明不保真: uid=%d role=%d ver=%d", c.UserID, c.Role, c.Ver)
	}
	if c.DeviceID != "device-abc" {
		t.Errorf("DeviceID 不保真: got %q want %q", c.DeviceID, "device-abc")
	}

	// access token（Generate）不应带 did
	access, err := Generate(policyTestSecret, 1001, 1, 1, 3)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	am := decodePayload(t, access)
	if raw, ok := am["did"]; ok {
		t.Errorf("access token payload 不应含 did，实际 did=%s", raw)
	}
	ac, err := Parse(policyTestSecret, access)
	if err != nil {
		t.Fatalf("Parse access 失败: %v", err)
	}
	if ac.DeviceID != "" {
		t.Errorf("access token DeviceID 应为空: %q", ac.DeviceID)
	}
}

func TestJwtPolicy_WrongSecretRejected(t *testing.T) {
	tok, err := Generate(policyTestSecret, 5, 1, 1, 0)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if _, err := Parse("another-secret", tok); err == nil {
		t.Error("错误密钥解析必须失败，实际成功了")
	}
}

func TestJwtPolicy_ExpiredRejected(t *testing.T) {
	// ttlHours = -1 → 已过期
	tok, err := Generate(policyTestSecret, 5, 1, -1, 0)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if _, err := Parse(policyTestSecret, tok); err == nil {
		t.Error("过期 token 解析必须失败，实际成功了")
	}
}

func TestJwtPolicy_TamperedPayloadRejected(t *testing.T) {
	tok, err := Generate(policyTestSecret, 5, 1, 1, 0)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	parts := strings.Split(tok, ".")
	// 篡改 payload（把 uid 改成 6）但保留原签名
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	tampered := strings.Replace(string(raw), `"uid":5`, `"uid":6`, 1)
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(tampered))
	if parts[1] == strings.Split(tok, ".")[1] {
		t.Skip("篡改未生效（uid 编码形式不同），跳过")
	}
	if _, err := Parse(policyTestSecret, strings.Join(parts, ".")); err == nil {
		t.Error("篡改 payload 后签名校验必须失败，实际成功了")
	}
}

func TestJwtPolicy_MalformedTokenRejected(t *testing.T) {
	for _, s := range []string{"", "abc", "a.b.c", "....", strings.Repeat("x", 5000)} {
		if _, err := Parse(policyTestSecret, s); err == nil {
			t.Errorf("畸形 token %q 必须解析失败", s)
		}
	}
}

func TestJwtPolicy_NoneAlgRejected(t *testing.T) {
	// alg=none 的经典绕过：手工构造 header.payload. 空签名
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"uid":1,"role":2}`))
	noneTok := header + "." + payload + "."
	if _, err := Parse(policyTestSecret, noneTok); err == nil {
		t.Error("alg=none 的 token 必须被拒绝，实际成功了")
	}
}
