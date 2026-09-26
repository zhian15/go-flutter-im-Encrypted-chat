// Package ssrf 提供服务端请求伪造（SSRF）防护：校验待抓取的 URL
// 是否解析到私网/回环/链路本地地址，防止内网探测与云元数据泄露。
package ssrf

import (
	"fmt"
	"net"
	"net/url"
	"time"
)

// ConnectTimeout / TotalTimeout 单次连接 / 总超时，供调用方构造 http.Client 时复用。
const (
	ConnectTimeout = 3 * time.Second
	TotalTimeout   = 8 * time.Second
	MaxBodyBytes   = 2 << 20 // 2MB
)

// privateRanges 需拒绝的私网/特殊地址段。
// 覆盖任务约定：0.0.0.0/8、10/8、172.16/12、192.168/16、127/8、169.254/16（含 169.254.169.254）、
// ::1、fc00::/7、fe80::/10，并补充 100.64/10、192.0.0/24、198.18/15 等保留段以收紧边界。
var privateRanges = []net.IPNet{
	parseCIDR("0.0.0.0/8"),
	parseCIDR("10.0.0.0/8"),
	parseCIDR("100.64.0.0/10"),
	parseCIDR("127.0.0.0/8"),
	parseCIDR("169.254.0.0/16"),
	parseCIDR("172.16.0.0/12"),
	parseCIDR("192.0.0.0/24"),
	parseCIDR("192.168.0.0/16"),
	parseCIDR("198.18.0.0/15"),
	parseCIDR("::1/128"),
	parseCIDR("fc00::/7"),
	parseCIDR("fe80::/10"),
}

func parseCIDR(s string) net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("ssrf: invalid cidr %q: %v", s, err))
	}
	return *n
}

// IsPrivateIP 判断 IP 是否属于私网/回环/链路本地/保留段。
// 支持 IPv4-mapped IPv6 还原。nil 视为不安全。
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, r := range privateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}

// CheckURL 校验 URL 是否可安全进行服务端访问（防 SSRF）。
// 规则：仅允许 http/https；解析 host 的全部 A/AAAA 记录；拒绝任一解析到私网/保留地址；
// 返回 (解析后的 *url.URL, ipOk, err)。调用方应在 err!=nil 或 ipOk==false 时拒绝访问。
func CheckURL(raw string) (parsed *url.URL, ipOk bool, err error) {
	u, e := url.Parse(raw)
	if e != nil {
		return nil, false, fmt.Errorf("url 解析失败: %w", e)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return u, false, fmt.Errorf("仅允许 http/https 协议")
	}
	host := u.Hostname()
	if host == "" {
		return u, false, fmt.Errorf("缺少 host")
	}
	ips, e := net.LookupIP(host)
	if e != nil {
		return u, false, fmt.Errorf("DNS 解析失败: %w", e)
	}
	if len(ips) == 0 {
		return u, false, fmt.Errorf("host 未解析到任何 IP")
	}
	for _, ip := range ips {
		if IsPrivateIP(ip) {
			return u, false, fmt.Errorf("host 解析到内网/保留地址 %s", ip.String())
		}
	}
	return u, true, nil
}
