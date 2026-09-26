// Package whitelist 网页小程序白名单匹配：根据请求 host 判定是否命中白名单，
// 用于控制链接卡片是否在应用内 WebView 打开（inApp）以及是否启用原生桥接。
package whitelist

import (
	"net"
	"strings"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/store"
)

// Entry 白名单命中条目。
// Logo/Cover 为后台自定义的小程序图标与封面大图，非空时由 /link/meta 覆盖 OG 抓取结果。
type Entry struct {
	Domain       string
	DisplayName  string
	NativeBridge bool
	Logo         string
	Cover        string
}

// NormalizeHost 统一 host：ToLower + 去端口；非法返回空串。
// 支持 IPv6 [::1]:80 去端口与去方括号。
func NormalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host
}

// Match 根据 host 匹配白名单。
// 顺序：先精确比对 domain；未命中再比对 *. 前缀通配（仅匹配一个 label，
// 如 *.qq.com 匹配 a.qq.com 但不匹配 a.b.qq.com）；禁止 endsWith（防 example.com.attacker.net 绕过）；
// 大小写不敏感；enabled=0 视为未命中。
// 注意：web_whitelist 表的 domain 写入时已统一 ToLower 去端口，故精确比对直接可用。
func Match(host string) (*Entry, bool) {
	host = NormalizeHost(host)
	if host == "" {
		return nil, false
	}
	// 1) 精确匹配
	var exact model.WebWhitelist
	if err := store.DB.Where("domain = ? AND enabled = 1", host).First(&exact).Error; err == nil {
		return toEntry(&exact), true
	}
	// 2) 通配匹配（仅 *. 前缀，单 label）
	var wilds []model.WebWhitelist
	if err := store.DB.Where("enabled = 1 AND domain LIKE ?", "*.%").Find(&wilds).Error; err == nil {
		for i := range wilds {
			if matchWildcard(host, wilds[i].Domain) {
				return toEntry(&wilds[i]), true
			}
		}
	}
	return nil, false
}

// matchWildcard 判断 host 是否匹配 *.base 形式的单 label 通配。
// 例：pattern=*.qq.com，host=a.qq.com 命中；host=a.b.qq.com / qq.com 不命中。
func matchWildcard(host, pattern string) bool {
	if !strings.HasPrefix(pattern, "*.") {
		return false
	}
	base := pattern[2:] // qq.com
	suffix := "." + base
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	// 仅允许一个 label：host 去掉 ".base" 后不能再含 "."
	rest := host[:len(host)-len(suffix)]
	return !strings.Contains(rest, ".")
}

func toEntry(w *model.WebWhitelist) *Entry {
	return &Entry{
		Domain:       w.Domain,
		DisplayName:  w.DisplayName,
		NativeBridge: w.NativeBridge == 1,
		Logo:         w.Logo,
		Cover:        w.Cover,
	}
}
