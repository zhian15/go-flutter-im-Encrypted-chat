package handler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yourcompany/im-server/internal/cache"
	"github.com/yourcompany/im-server/internal/ssrf"
	"github.com/yourcompany/im-server/internal/whitelist"
)

// LinkMetaHandler GET /api/v1/link/meta?url= （功能 B-1/B-2/B-3）
// 流程：SSRF 校验 → 白名单匹配 → 抓取 OG 元数据（Redis 缓存 + 限流中间件）。
// 出参：{url,domain,title,icon,favicon,image,description,inApp(bool),displayName,matched(bool)}。
//   - icon：站点 favicon（link rel=icon 的 href），缺失时回退为 og:image
//   - image：封面大图（og:image）
//   - 命中白名单且该条配置了 cover/logo 时，image 用 cover 覆盖、icon 用 logo 覆盖
//
// 规则：
//   - SSRF 拒绝 → 403
//   - 限流（中间件）→ 429
//   - 抓取失败/超时/非 html → 降级为 inApp=false 的灰边卡片数据，仍 200 + 数据，不阻塞发送。
func LinkMetaHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.Query("url")
		if raw == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "缺少 url 参数"})
			return
		}
		// 1) SSRF 校验（拒绝内网/保留地址）
		parsed, ipOk, err := ssrf.CheckURL(raw)
		if err != nil || !ipOk {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "该链接地址不允许访问"})
			return
		}
		host := parsed.Hostname()

		// 2) 白名单匹配
		entry, matched := whitelist.Match(host)
		inApp := matched
		displayName := ""
		nativeBridge := false
		cover := ""
		logo := ""
		if matched && entry != nil {
			displayName = entry.DisplayName
			nativeBridge = entry.NativeBridge
			cover = entry.Cover
			logo = entry.Logo
		}

		// 3) 缓存命中
		if meta, ok := cache.GetLinkMeta(c.Request.Context(), raw); ok {
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok",
				"data": linkMetaData(raw, host, meta, inApp, displayName, nativeBridge, matched, cover, logo)})
			return
		}

		// 4) 抓取 OG 元数据（带超时 + 每跳 SSRF 重校验）
		title, icon, image, description, fetchOK := fetchOpenGraph(c.Request.Context(), raw)
		if fetchOK {
			meta := cache.LinkMeta{Title: title, Icon: icon, Image: image, Description: description}
			cache.SetLinkMeta(c.Request.Context(), raw, meta)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok",
				"data": linkMetaData(raw, host, meta, inApp, displayName, nativeBridge, matched, cover, logo)})
			return
		}

		// 5) 降级：灰边卡片（inApp=false），仍 200 + 数据，不阻塞发送
		//    命中白名单且配了封面/图标时，即使抓取失败也要展示后台配置的图
		cache.SetLinkMetaNegative(c.Request.Context(), raw)
		icon, image = applyWhitelistMedia("", "", logo, cover)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
			"url":          raw,
			"domain":       host,
			"title":        "",
			"icon":         icon,
			"image":        image,
			"description":  "",
			"inApp":        false,
			"displayName":  displayName,
			"nativeBridge": nativeBridge,
			"matched":      matched,
		}})
	}
}

func linkMetaData(raw, host string, meta cache.LinkMeta, inApp bool, displayName string, nativeBridge, matched bool, cover, logo string) gin.H {
	icon, image := applyWhitelistMedia(meta.Icon, meta.Image, logo, cover)
	return gin.H{
		"url":          raw,
		"domain":       host,
		"title":        meta.Title,
		"icon":         icon,
		"image":        image,
		"description":  meta.Description,
		"inApp":        inApp,
		"displayName":  displayName,
		"nativeBridge": nativeBridge,
		"matched":      matched,
	}
}

// applyWhitelistMedia 用白名单配置的封面/图标覆盖 OG 抓取结果。
//   - cover 非空 → 覆盖 image（原为 og:image）
//   - logo  非空 → 覆盖 icon（原为站点 favicon）
//   - 未配置（空串）→ 维持原值，保留「favicon 缺失回退 og:image」的既有逻辑。
func applyWhitelistMedia(icon, image, logo, cover string) (outIcon, outImage string) {
	outIcon, outImage = icon, image
	if cover != "" {
		outImage = cover
	}
	if logo != "" {
		outIcon = logo
	}
	return outIcon, outImage
}

// fetchOpenGraph SSRF 安全的元数据抓取：手动逐跳重定向（≤3 跳），每跳重校验；
// connect 超时 3s、total 8s、body ≤2MB、仅接受 text/html。
func fetchOpenGraph(ctx context.Context, raw string) (title, icon, image, description string, ok bool) {
	current := raw
	client := &http.Client{
		Timeout: ssrf.TotalTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 手动处理重定向
		},
		Transport: &http.Transport{
			DialContext: safeDialContext,
		},
	}
	for hop := 0; hop <= 3; hop++ {
		parsed, ipOk, err := ssrf.CheckURL(current)
		if err != nil || !ipOk {
			return "", "", "", "", false
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return "", "", "", "", false
		}
		req.Header.Set("User-Agent", "ChatPulseBot/1.0 (+link-preview)")
		req.Header.Set("Accept", "text/html")

		resp, err := client.Do(req)
		if err != nil {
			return "", "", "", "", false
		}
		// 重定向：手动逐跳，下一跳需经 SSRF 重校验
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if loc == "" {
				return "", "", "", "", false
			}
			next, e := parsed.Parse(loc)
			if e != nil {
				return "", "", "", "", false
			}
			current = next.String()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return "", "", "", "", false
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			resp.Body.Close()
			return "", "", "", "", false // 非 html → 降级
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, ssrf.MaxBodyBytes))
		resp.Body.Close()

		t, ic, im, d := parseOG(body)
		// 相对路径解析为绝对（icon 与 image 都可能是相对 URL）
		if ic != "" && parsed != nil {
			if u, e := parsed.Parse(ic); e == nil {
				ic = u.String()
			}
		}
		if im != "" && parsed != nil {
			if u, e := parsed.Parse(im); e == nil {
				im = u.String()
			}
		}
		return t, ic, im, d, true
	}
	return "", "", "", "", false
}

// safeDialContext 解析并校验每个 IP 均非私网/保留，再连接到首个合法 IP（防 TOCTOU）。
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
		port = "80"
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ssrf.IsPrivateIP(ip) {
			return nil, fmt.Errorf("ssrf: 解析到内网/保留地址 %s", ip.String())
		}
	}
	dialer := &net.Dialer{Timeout: ssrf.ConnectTimeout}
	// 连接到首个非私网 IP（host:port 形式，避免二次解析绕过校验）
	return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

// ===== OG 解析（轻量正则，避免引入 HTML 解析依赖） =====

var (
	reMetaPropContent = regexp.MustCompile(`(?is)<meta\b[^>]*property\s*=\s*["']([^"']*)["'][^>]*content\s*=\s*["']([^"']*)["']`)
	reMetaContentProp = regexp.MustCompile(`(?is)<meta\b[^>]*content\s*=\s*["']([^"']*)["'][^>]*property\s*=\s*["']([^"']*)["']`)
	reNameContent     = regexp.MustCompile(`(?is)<meta\b[^>]*name\s*=\s*["']([^"']*)["'][^>]*content\s*=\s*["']([^"']*)["']`)
	reContentName     = regexp.MustCompile(`(?is)<meta\b[^>]*content\s*=\s*["']([^"']*)["'][^>]*name\s*=\s*["']([^"']*)["']`)
	reTitle           = regexp.MustCompile(`(?is)<title[^>]*>([^<]*)</title>`)
	reIconHrefRel     = regexp.MustCompile(`(?is)<link\b[^>]*rel\s*=\s*["'][^"']*icon[^"']*["'][^>]*href\s*=\s*["']([^"']*)["']`)
	reIconRelHref     = regexp.MustCompile(`(?is)<link\b[^>]*href\s*=\s*["']([^"']*)["'][^>]*rel\s*=\s*["'][^"']*icon[^"']*["']`)
)

// parseOG 解析 OG 元数据。
//   - icon  = 站点 favicon（link rel=icon 的 href），favicon 缺失时回退为 og:image（小图标兜底）
//   - image = 封面大图（og:image），独立提取，不受 icon 影响
func parseOG(body []byte) (title, icon, image, description string) {
	html := string(body)
	// title
	if m := reMetaPropContent.FindStringSubmatch(html); m != nil && m[1] == "og:title" {
		title = htmlUnescape(m[2])
	}
	if title == "" {
		if m := reMetaContentProp.FindStringSubmatch(html); m != nil && m[2] == "og:title" {
			title = htmlUnescape(m[1])
		}
	}
	if title == "" {
		if m := reTitle.FindStringSubmatch(html); m != nil {
			title = htmlUnescape(m[1])
		}
	}
	// description
	if m := reMetaPropContent.FindStringSubmatch(html); m != nil && m[1] == "og:description" {
		description = htmlUnescape(m[2])
	}
	if description == "" {
		if m := reMetaContentProp.FindStringSubmatch(html); m != nil && m[2] == "og:description" {
			description = htmlUnescape(m[1])
		}
	}
	if description == "" {
		if m := reNameContent.FindStringSubmatch(html); m != nil && strings.EqualFold(m[1], "description") {
			description = htmlUnescape(m[2])
		}
	}
	if description == "" {
		if m := reContentName.FindStringSubmatch(html); m != nil && strings.EqualFold(m[2], "description") {
			description = htmlUnescape(m[1])
		}
	}
	// image（封面大图 og:image）
	if m := reMetaPropContent.FindStringSubmatch(html); m != nil && m[1] == "og:image" {
		image = htmlUnescape(m[2])
	}
	if image == "" {
		if m := reMetaContentProp.FindStringSubmatch(html); m != nil && m[2] == "og:image" {
			image = htmlUnescape(m[1])
		}
	}
	// icon（站点 favicon：link rel=icon 的 href；缺失时才回退 og:image）
	if m := reIconHrefRel.FindStringSubmatch(html); m != nil {
		icon = htmlUnescape(m[1])
	}
	if icon == "" {
		if m := reIconRelHref.FindStringSubmatch(html); m != nil {
			icon = htmlUnescape(m[1])
		}
	}
	if icon == "" {
		icon = image // favicon 缺失 → 回退用 og:image 作为小图标
	}
	return title, icon, image, description
}

var htmlEntityReplacer = strings.NewReplacer(
	"&amp;", "&", "&lt;", "<", "&gt;", ">",
	"&quot;", "\"", "&#39;", "'", "&apos;", "'", "&nbsp;", " ",
)

func htmlUnescape(s string) string {
	s = htmlEntityReplacer.Replace(s)
	s = strings.TrimSpace(s)
	// 折叠空白
	return strings.Join(strings.Fields(s), " ")
}
