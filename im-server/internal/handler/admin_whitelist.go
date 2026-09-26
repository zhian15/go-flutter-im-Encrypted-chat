package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/repo"
	"github.com/yourcompany/im-server/internal/whitelist"
)

// AdminWebWhitelistListHandler GET /api/v1/admin/web-whitelist
// Query: keyword page page_size → {list,total}
func AdminWebWhitelistListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		keyword := c.Query("keyword")
		page := atoiDefault(c.Query("page"), 1)
		size := atoiDefault(c.Query("page_size"), 20)
		if page <= 0 {
			page = 1
		}
		if size <= 0 || size > 100 {
			size = 20
		}
		list, total, err := repo.WebWhitelistList(c.Request.Context(), keyword, page, size)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": list, "total": total}})
	}
}

// webWhitelistCreateBody 创建白名单请求体（宽容绑定）。
//
// 兼容说明（跨端契约，勿收紧）：
//   - 字段名同时接受 snake_case 与 camelCase：display_name/displayName、
//     native_bridge/nativeBridge；logo/cover 两种写法同名。
//   - enabled / nativeBridge 同时接受 JSON boolean 与 number：true/false、1/0、
//     以及字符串形式的 "1"/"0"/"true"/"false"；非法值视为未提供，走默认值。
//     开关类字段必须绑成 json.RawMessage 再归一化，直接绑 *int 会让传 boolean 的客户端 400。
type webWhitelistCreateBody struct {
	Domain            string          `json:"domain"`
	DisplayName       string          `json:"display_name"`
	DisplayNameCamel  string          `json:"displayName"`
	Logo              string          `json:"logo"`
	Cover             string          `json:"cover"`
	Enabled           json.RawMessage `json:"enabled"`
	NativeBridge      json.RawMessage `json:"native_bridge"`
	NativeBridgeCamel json.RawMessage `json:"nativeBridge"`
}

// DisplayNameValue 取展示名：优先 snake_case，未填则用 camelCase。
func (b webWhitelistCreateBody) DisplayNameValue() string {
	if b.DisplayName != "" {
		return b.DisplayName
	}
	return b.DisplayNameCamel
}

// EnabledValue 取启用开关：未提供或非法时默认 1（启用）。
func (b webWhitelistCreateBody) EnabledValue() int {
	if v, ok := toInt01(b.Enabled); ok {
		return v
	}
	return 1
}

// NativeBridgeValue 取原生桥开关：未提供或非法时默认 0（关闭）。
func (b webWhitelistCreateBody) NativeBridgeValue() int {
	if v, ok := toInt01(b.NativeBridge); ok {
		return v
	}
	if v, ok := toInt01(b.NativeBridgeCamel); ok {
		return v
	}
	return 0
}

// webWhitelistUpdateBody 更新白名单请求体（宽容绑定，全部字段可选）。
// 命名/类型兼容规则同 webWhitelistCreateBody。
type webWhitelistUpdateBody struct {
	Domain            *string         `json:"domain"`
	DisplayName       *string         `json:"display_name"`
	DisplayNameCamel  *string         `json:"displayName"`
	Logo              *string         `json:"logo"`
	Cover             *string         `json:"cover"`
	Enabled           json.RawMessage `json:"enabled"`
	NativeBridge      json.RawMessage `json:"native_bridge"`
	NativeBridgeCamel json.RawMessage `json:"nativeBridge"`
}

// DisplayNamePtr 取展示名指针（nil 表示不修改）：优先 snake_case，再 camelCase。
func (b webWhitelistUpdateBody) DisplayNamePtr() *string {
	if b.DisplayName != nil {
		return b.DisplayName
	}
	return b.DisplayNameCamel
}

// EnabledPtr 取启用开关指针（nil 表示不修改）。
func (b webWhitelistUpdateBody) EnabledPtr() *int {
	if v, ok := toInt01(b.Enabled); ok {
		return &v
	}
	return nil
}

// NativeBridgePtr 取原生桥开关指针（nil 表示不修改）。
func (b webWhitelistUpdateBody) NativeBridgePtr() *int {
	if v, ok := toInt01(b.NativeBridge); ok {
		return &v
	}
	if v, ok := toInt01(b.NativeBridgeCamel); ok {
		return &v
	}
	return nil
}

// toInt01 归一化「开关」类字段：同时接受 JSON boolean 与 number。
//
//   - true / 1 / "1" / "true" / 非 0 数字 → 1
//   - false / 0 / "0" / "false" → 0
//   - null / 缺失 / 非法值 → (0, false)，表示「未提供」，由调用方使用默认值。
func toInt01(raw json.RawMessage) (int, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0, false
	}
	switch strings.ToLower(s) {
	case "true":
		return 1, true
	case "false":
		return 0, true
	}
	// 字符串数字（"1"/"0"）：去引号后按数字处理
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return toInt01(json.RawMessage(strings.TrimSpace(s[1 : len(s)-1])))
	}
	if n, err := strconv.Atoi(s); err == nil {
		return boolTo01(n != 0), true
	}
	// 浮点形式（1.0 / 0.0）
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return boolTo01(f != 0), true
	}
	return 0, false
}

// boolTo01 布尔转 1/0。
func boolTo01(v bool) int {
	if v {
		return 1
	}
	return 0
}

// AdminWebWhitelistCreateHandler POST /api/v1/admin/web-whitelist
// Body: {domain,display_name|displayName,enabled,native_bridge|nativeBridge,logo,cover} → {id,...}
func AdminWebWhitelistCreateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var body webWhitelistCreateBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误: " + err.Error()})
			return
		}
		domain := whitelist.NormalizeHost(body.Domain)
		if !isValidDomainHost(domain) {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "domain 格式非法（须为合法 host，禁止含 scheme/路径/端口）"})
			return
		}
		w, err := repo.WebWhitelistCreate(c.Request.Context(), &model.WebWhitelist{
			Domain:       domain,
			DisplayName:  body.DisplayNameValue(),
			Enabled:      body.EnabledValue(),
			NativeBridge: body.NativeBridgeValue(),
			Logo:         strings.TrimSpace(body.Logo),
			Cover:        strings.TrimSpace(body.Cover),
		})
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": w})
	}
}

// AdminWebWhitelistUpdateHandler PUT /api/v1/admin/web-whitelist/:id
func AdminWebWhitelistUpdateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误: id 非法"})
			return
		}
		var body webWhitelistUpdateBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误: " + err.Error()})
			return
		}
		var domainPtr *string
		if body.Domain != nil {
			d := whitelist.NormalizeHost(*body.Domain)
			if !isValidDomainHost(d) {
				c.JSON(http.StatusOK, gin.H{"code": 400, "message": "domain 格式非法"})
				return
			}
			domainPtr = &d
		}
		var logoPtr, coverPtr *string
		if body.Logo != nil {
			v := strings.TrimSpace(*body.Logo)
			logoPtr = &v
		}
		if body.Cover != nil {
			v := strings.TrimSpace(*body.Cover)
			coverPtr = &v
		}
		w, err := repo.WebWhitelistUpdate(c.Request.Context(), id, domainPtr, body.DisplayNamePtr(),
			logoPtr, coverPtr, body.EnabledPtr(), body.NativeBridgePtr())
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": errCode(err), "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": w})
	}
}

// AdminWebWhitelistDeleteHandler DELETE /api/v1/admin/web-whitelist/:id
func AdminWebWhitelistDeleteHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误: id 非法"})
			return
		}
		if err := repo.WebWhitelistDelete(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "删除失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	}
}

// isValidDomainHost 校验白名单 domain 合法性：禁止 scheme/路径/端口/@，
// 各 DNS label 仅含字母数字连字符（允许 Unicode 字母以支持国际化域名）。
func isValidDomainHost(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	if strings.ContainsAny(s, ":/@?#") {
		return false
	}
	labels := strings.Split(s, ".")
	for _, l := range labels {
		if l == "" || strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
			return false
		}
		for _, ch := range l {
			if !(unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '-') {
				return false
			}
		}
	}
	return true
}
