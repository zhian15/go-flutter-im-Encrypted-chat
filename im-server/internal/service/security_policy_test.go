package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yourcompany/im-server/internal/config"
)

// ============ 密码策略 ============

func TestPasswordPolicyBoundaries(t *testing.T) {
	cases := []struct {
		name string
		pwd  string
		want bool
	}{
		{"空串", "", false},
		{"5 个 ASCII", "abcde", false},
		{"6 个 ASCII", "abcdef", true},
		{"20 个 ASCII", strings.Repeat("a", 20), true},
		{"21 个 ASCII", strings.Repeat("a", 21), false},
		// 下面两条是「按 rune 计长」的证据：
		// 6 个中文 = 18 字节，若实现退回字节计长会判 false。
		{"6 个中文(18 字节)", "密码策略测试", true},
		// 7 个中文 = 21 字节 > 20；若按字节计长必为 false，按 rune 计长应为 true。
		{"7 个中文(21 字节)", "密码策略测试啊", true},
		{"20 个中文(60 字节)", strings.Repeat("密", 20), true},
		{"21 个中文(63 字节)", strings.Repeat("密", 21), false},
		{"含空格 6 位", "a b c ", true},
		{"emoji 6 个", "😀😀😀😀😀😀", true},
	}
	for _, c := range cases {
		got := validPassword(c.pwd)
		if got != c.want {
			t.Errorf("validPassword(%s=%q) = %v, want %v (rune=%d, bytes=%d)",
				c.name, c.pwd, got, c.want, len([]rune(c.pwd)), len(c.pwd))
		}
	}
}

// ============ WS Origin 校验 ============

func cfgWith(origins ...string) *config.Config {
	return &config.Config{WSAllowedOrigins: origins}
}

func originReq(origin string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/ws", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestOriginPolicy_NoOriginHeaderAllowed(t *testing.T) {
	cfg := cfgWith("https://im.example.com")
	// 完全没有 Origin 头（原生 App / 压测工具）
	if !WsOriginAllowed(cfg, originReq("")) {
		t.Error("无 Origin 头必须放行")
	}
	// 注意（独立验证发现）：origin == "" 的判定发生在 TrimSpace 之前，
	// 因此「纯空白 Origin 头」会走到白名单比对并被拒绝（fail-safe 方向，不构成漏洞）。
	// 浏览器不会发出纯空白 Origin；此处仅固化实测行为，避免将来被误当作回归。
	if WsOriginAllowed(cfg, originReq("   ")) {
		t.Log("纯空白 Origin 被放行（实现若改为先 TrimSpace 即为该结果）")
	}
	// 两侧空白的合法 Origin 应仍能命中白名单
	if !WsOriginAllowed(cfg, originReq("  https://im.example.com  ")) {
		t.Error("Origin 两侧空白应被 TrimSpace 后命中白名单")
	}
}

func TestOriginPolicy_WhitelistExactCaseInsensitive(t *testing.T) {
	cfg := cfgWith("https://im.example.com", "https://admin.example.com")
	if !WsOriginAllowed(cfg, originReq("https://im.example.com")) {
		t.Error("白名单精确匹配应放行")
	}
	if !WsOriginAllowed(cfg, originReq("https://IM.example.com")) {
		t.Error("白名单匹配应大小写不敏感（Origin 大写）")
	}
	if !WsOriginAllowed(cfg, originReq("HTTPS://im.Example.com")) {
		t.Error("白名单匹配应大小写不敏感（两侧混合大小写）")
	}
}

func TestOriginPolicy_WildcardSubdomain(t *testing.T) {
	cfg := cfgWith("*.example.com")
	if !WsOriginAllowed(cfg, originReq("https://a.example.com")) {
		t.Error("*.example.com 应匹配子域 a.example.com")
	}
	if !WsOriginAllowed(cfg, originReq("https://a.b.example.com")) {
		t.Error("*.example.com 应匹配多级子域 a.b.example.com")
	}
	if !WsOriginAllowed(cfg, originReq("https://A.Example.COM")) {
		t.Error("通配匹配应大小写不敏感")
	}
	// 文档明确写「仅匹配子域，裸域需单独列出」——断言实现与文档一致。
	if WsOriginAllowed(cfg, originReq("https://example.com")) {
		t.Error("裸域 example.com 不应被 *.example.com 匹配（文档说需单独列出）")
	}
	if WsOriginAllowed(cfg, originReq("https://notexample.com")) {
		t.Error("notexample.com 不应匹配 *.example.com（后缀必须带点边界）")
	}
	// 子域匹配时应无视端口
	if !WsOriginAllowed(cfg, originReq("https://a.example.com:8443")) {
		t.Error("通配匹配应无视端口")
	}
}

func TestOriginPolicy_LocalhostAndLoopback(t *testing.T) {
	cfg := cfgWith() // 白名单为空，只靠本机放行
	for _, o := range []string{
		"http://localhost:5173",
		"https://localhost",
		"http://127.0.0.1:9090",
		"http://[::1]:8080",
	} {
		if !WsOriginAllowed(cfg, originReq(o)) {
			t.Errorf("本机来源 %q 必须放行", o)
		}
	}
	// 形似但不是本机
	for _, o := range []string{
		"https://localhost.evil.com",
		"https://127.0.0.1.evil.com",
		"https://evil.com/?x=localhost",
	} {
		if WsOriginAllowed(cfg, originReq(o)) {
			t.Errorf("%q 不是本机来源，必须拒绝", o)
		}
	}
}

func TestOriginPolicy_RejectUnknown(t *testing.T) {
	cfg := cfgWith("https://im.example.com")
	if WsOriginAllowed(cfg, originReq("https://evil.com")) {
		t.Error("非白名单来源必须拒绝")
	}
	if WsOriginAllowed(cfg, originReq("https://im.example.com.evil.com")) {
		t.Error("后缀伪装域名必须拒绝")
	}
	if WsOriginAllowed(cfg, originReq("http://im.example.com")) {
		t.Error("协议不同（http vs 白名单 https）应拒绝")
	}
	if WsOriginAllowed(cfg, originReq("wss://im.example.com")) {
		t.Error("wss:// 与白名单 https:// 字面不同，应拒绝")
	}
	if WsOriginAllowed(cfg, originReq("https://im.example.com/")) {
		t.Error("带尾斜杠的 Origin 与白名单字面不同，当前实现会拒绝（见报告观察项）")
	}
	// 空白名单：只放行无 Origin 与本机
	empty := cfgWith("", "   ")
	if WsOriginAllowed(empty, originReq("https://evil.com")) {
		t.Error("空白名单/空白项时外部来源必须拒绝")
	}
}

// TestOriginPolicy_WildcardIgnoresScheme 固化一个观察项：
// "*.example.com" 只比对 host 后缀，**不比对协议**，因此 http://a.example.com 也会被放行。
// 这不是漏洞（能控制子域的人本来就受信），但与"精确项要求协议一致"的行为不对称，值得留意。
func TestOriginPolicy_WildcardIgnoresScheme(t *testing.T) {
	cfg := cfgWith("*.example.com")
	for _, o := range []string{"https://a.example.com", "http://a.example.com", "ws://a.example.com"} {
		if !WsOriginAllowed(cfg, originReq(o)) {
			t.Errorf("通配项按 host 后缀匹配，%q 预期放行", o)
		}
	}
}

// TestOriginPolicy_MalformedNoPanic 畸形 Origin 必须"不 panic 且拒绝"。
// 任何 panic 都是严重缺陷（可被远端单请求打挂进程）。
func TestOriginPolicy_MalformedNoPanic(t *testing.T) {
	cfg := cfgWith("https://im.example.com", "*.example.com")
	malformed := []string{
		"garbage",
		"http://",
		"://x",
		"//",
		"https://",
		"http://:8080",
		"http://[::1",
		"%zz",
		"%",
		"https://%41%42",
		"ftp://example.com",
		"javascript:alert(1)",
		"data:text/html,x",
		string([]byte{0xff, 0xfe, 0xfd}),
		"\x00",
		strings.Repeat("a", 8192),
		"https://" + strings.Repeat("a", 8000) + ".com",
		"*.example.com",
		"null",
	}
	for _, o := range malformed {
		o := o
		t.Run(safeName(o), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("WsOriginAllowed(%q) panic: %v", o, r)
				}
			}()
			if WsOriginAllowed(cfg, originReq(o)) {
				t.Errorf("畸形 Origin %q 必须拒绝，实际放行了", o)
			}
		})
	}
}

func safeName(s string) string {
	if len(s) > 40 {
		s = s[:40]
	}
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "empty"
	}
	return b.String()
}
