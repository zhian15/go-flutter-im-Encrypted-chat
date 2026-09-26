package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/store"
)

// ============ AI 翻译（DeepSeek / OpenAI 兼容接口） ============
//
// 配置（后台 ConfigView「AI 翻译」区块，存 sys_config KV，无需建表）：
//   ai_translate_enabled   总开关（"0"=关闭：客户端不显示译按钮、接口直接拒绝）
//   ai_translate_api_base  接口地址（OpenAI 兼容，默认 https://api.deepseek.com）
//   ai_translate_api_key   API Key（为空 = 整个翻译功能不可用）
//   ai_translate_model     模型名（默认 deepseek-chat）
//   ai_auto_daily_limit    自动翻译每日次数（0 = 关闭自动翻译）
//   ai_manual_daily_limit  手动翻译每日次数（0 = 不限量）
//
// 成本控制：
//   - 译文缓存：Redis ait:cache:<sha256(text|lang)>，TTL 7 天，同一句话全世界只调一次 AI
//   - 每日计数：Redis ait:cnt:<kind>:<uid>:<yyyymmdd>，TTL 48 小时自然过期
//   - 缓存命中不计入当日次数

const (
	aiTranslateEnabledKey = "ai_translate_enabled"
	aiTranslateBaseKey    = "ai_translate_api_base"
	aiTranslateKeyKey     = "ai_translate_api_key"
	aiTranslateModelKey   = "ai_translate_model"
	aiAutoDailyLimitKey   = "ai_auto_daily_limit"
	aiManualDailyLimitKey = "ai_manual_daily_limit"
)

// 业务错误：handler 层 errCode 会映射为提示，文案直接可读
var (
	ErrTranslateNotConfigured = errors.New("AI 翻译未配置，请联系管理员")
	ErrTranslateAutoDisabled  = errors.New("自动翻译未开启")
	ErrTranslateAutoLimit     = errors.New("今日自动翻译次数已用完")
	ErrTranslateManualLimit   = errors.New("今日手动翻译次数已用完")
	ErrTranslateEmpty         = errors.New("翻译内容为空")
	ErrTranslateFailed        = errors.New("翻译失败，请稍后重试")
)

// TranslateConfig AI 翻译配置（后台可配）
type TranslateConfig struct {
	Enabled          bool   `json:"enabled"` // 总开关，关闭 = 翻译功能整体不可用
	APIBase          string `json:"apiBase"`
	APIKey           string `json:"apiKey"`
	Model            string `json:"model"`
	AutoDailyLimit   int    `json:"autoDailyLimit"`
	ManualDailyLimit int    `json:"manualDailyLimit"`
}

// GetTranslateConfig 读配置（缺省值见各字段注释）
func GetTranslateConfig(ctx context.Context) TranslateConfig {
	base := strVal(SysConfigGet(ctx, aiTranslateBaseKey, "https://api.deepseek.com"))
	if strings.TrimSpace(base) == "" {
		base = "https://api.deepseek.com"
	}
	model := strVal(SysConfigGet(ctx, aiTranslateModelKey, "deepseek-chat"))
	if strings.TrimSpace(model) == "" {
		model = "deepseek-chat"
	}
	// 总开关：未配置过默认开启；后台 a-switch 存的是 JSON boolean，
	// 也兼容字符串 "0"/"false"/"off"（显式关闭值）
	enabled := translateBoolVal(SysConfigGet(ctx, aiTranslateEnabledKey, true), true)
	return TranslateConfig{
		Enabled:          enabled,
		APIBase:          strings.TrimRight(base, "/"),
		APIKey:           strVal(SysConfigGet(ctx, aiTranslateKeyKey, "")),
		Model:            model,
		AutoDailyLimit:   intVal(SysConfigGet(ctx, aiAutoDailyLimitKey, 50), 50),
		ManualDailyLimit: intVal(SysConfigGet(ctx, aiManualDailyLimitKey, 0), 0),
	}
}

// TranslateEnabled 翻译功能是否可用（总开关开 且 已配 API Key）
func TranslateEnabled(ctx context.Context) bool {
	cfg := GetTranslateConfig(ctx)
	return cfg.Enabled && cfg.APIKey != ""
}

// translateBoolVal 宽松布尔解析：JSON boolean / 字符串
// （"1"/"true"/"on"=开；"0"/"false"/"off"=关；空串回落默认值）
func translateBoolVal(v interface{}, def bool) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		s := strings.ToLower(strings.TrimSpace(b))
		if s == "" {
			return def
		}
		return s != "0" && s != "false" && s != "off"
	}
	return def
}

// 目标语种 → 提示词里的语言名（客户端传 ISO 缩写；未知值原样透传给 AI）
var translateLangNames = map[string]string{
	"zh":  "简体中文",
	"zhT": "繁體中文",
	"en":  "English",
	"ja":  "日本語",
}

// Translate 翻译一条文本。
//   - auto=true  走自动翻译额度（limit 0 = 关闭）
//   - auto=false 走手动翻译额度（limit 0 = 不限量）
//   - 缓存命中直接返回，不消耗当日次数
func Translate(ctx context.Context, userID int64, text, targetLang string, auto bool) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ErrTranslateEmpty
	}
	if len(text) > 5000 {
		text = text[:5000] // 单条上限 5000 字符，防刷
	}
	cfg := GetTranslateConfig(ctx)
	if !cfg.Enabled || cfg.APIKey == "" {
		return "", ErrTranslateNotConfigured
	}
	lang, ok := translateLangNames[strings.TrimSpace(targetLang)]
	if !ok || lang == "" {
		lang = "简体中文" // 客户端未传/传错时回落中文
	}

	// 1) 译文缓存（命中不计次数）
	cacheKey := "ait:cache:" + hashKey(text, lang)
	if rdb := store.RDB; rdb != nil {
		if hit, err := rdb.Get(ctx, cacheKey).Result(); err == nil && hit != "" {
			return hit, nil
		}
	}

	// 2) 每日额度
	kind := "manual"
	limit := cfg.ManualDailyLimit
	if auto {
		kind = "auto"
		limit = cfg.AutoDailyLimit
	}
	if err := consumeTranslateQuota(ctx, userID, kind, limit); err != nil {
		return "", err
	}

	// 3) 调 AI 接口
	out, err := callTranslateAPI(ctx, cfg, text, lang)
	if err != nil {
		return "", err
	}

	// 4) 写缓存（7 天）；写失败不影响返回
	if rdb := store.RDB; rdb != nil && out != "" {
		rdb.Set(ctx, cacheKey, out, 7*24*time.Hour)
	}
	return out, nil
}

// TranslateUsage 当日用量（客户端设置页展示）
func TranslateUsage(ctx context.Context, userID int64) map[string]interface{} {
	cfg := GetTranslateConfig(ctx)
	_, day := translateCounterKeys(userID)
	autoUsed, _ := counterGet(ctx, "auto", userID, day)
	manualUsed, _ := counterGet(ctx, "manual", userID, day)
	return map[string]interface{}{
		"autoUsed":    autoUsed,
		"autoLimit":   cfg.AutoDailyLimit, // 0 = 关闭自动翻译
		"manualUsed":  manualUsed,
		"manualLimit": cfg.ManualDailyLimit, // 0 = 不限量
		// configured = 总开关开 且 已配 API Key：客户端据此决定是否显示「译」按钮
		"configured": cfg.Enabled && cfg.APIKey != "",
	}
}

// ---- 内部实现 ----

func hashKey(text, lang string) string {
	h := sha256.Sum256([]byte(text + "\x00" + lang))
	return hex.EncodeToString(h[:16])
}

// translateCounterKeys 当天计数 key 的日期后缀（本地时区）
func translateCounterKeys(userID int64) (string, string) {
	day := time.Now().Format("20060102")
	return fmt.Sprintf("ait:cnt:%d:%s", userID, day), day
}

// counterGet 读当日已用次数
func counterGet(ctx context.Context, kind string, userID int64, day string) (int, error) {
	if store.RDB == nil {
		return 0, errors.New("redis unavailable")
	}
	key := fmt.Sprintf("ait:cnt:%s:%d:%s", kind, userID, day)
	n, err := store.RDB.Get(ctx, key).Int()
	if err != nil {
		return 0, nil // 无 key = 0 次
	}
	return n, nil
}

// consumeTranslateQuota 占用一个当日名额。超限时把 INCR 回退并返回业务错误。
func consumeTranslateQuota(ctx context.Context, userID int64, kind string, limit int) error {
	if store.RDB == nil {
		return ErrTranslateFailed
	}
	// 自动翻译 limit==0 表示后台未开启自动翻译
	if kind == "auto" && limit <= 0 {
		return ErrTranslateAutoDisabled
	}
	_, day := translateCounterKeys(userID)
	key := fmt.Sprintf("ait:cnt:%s:%d:%s", kind, userID, day)

	n, err := store.RDB.Incr(ctx, key).Result()
	if err != nil {
		return ErrTranslateFailed
	}
	if n == 1 {
		store.RDB.Expire(ctx, key, 48*time.Hour) // 跨天自然过期
	}
	// limit<=0 且 kind==manual → 不限量
	if limit > 0 && int(n) > limit {
		store.RDB.Decr(ctx, key)
		if kind == "auto" {
			return ErrTranslateAutoLimit
		}
		return ErrTranslateManualLimit
	}
	return nil
}

// callTranslateAPI 调 OpenAI 兼容 chat/completions
func callTranslateAPI(ctx context.Context, cfg TranslateConfig, text, lang string) (string, error) {
	url := buildCompletionsURL(cfg.APIBase)
	sys := fmt.Sprintf(
		"You are a professional translation engine. Translate the user message into %s. "+
			"Output ONLY the translation, no explanations, no quotes, no extra punctuation. "+
			"If the message is already in %s (or is not translatable text such as numbers, links or emoji), output it unchanged.",
		lang, lang)

	body, _ := json.Marshal(map[string]interface{}{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": sys},
			{"role": "user", "content": text},
		},
		"temperature": 0.2,
		"stream":      false,
	})

	ctx2, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx2, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", ErrTranslateFailed
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", ErrTranslateFailed
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", ErrTranslateFailed
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return "", ErrTranslateFailed
	}
	result := strings.TrimSpace(out.Choices[0].Message.Content)
	result = strings.Trim(result, "\"“”") // 个别模型喜欢给译文加引号
	if result == "" {
		return "", ErrTranslateFailed
	}
	return result, nil
}

// buildCompletionsURL 兼容三种 base 写法：
//
//	https://api.deepseek.com                → +/v1/chat/completions
//	https://api.deepseek.com/v1             → +/chat/completions
//	https://xx/v1/chat/completions          → 原样
func buildCompletionsURL(base string) string {
	b := strings.TrimRight(base, "/")
	switch {
	case strings.HasSuffix(b, "/chat/completions"):
		return b
	case strings.HasSuffix(b, "/v1"):
		return b + "/chat/completions"
	default:
		return b + "/v1/chat/completions"
	}
}
