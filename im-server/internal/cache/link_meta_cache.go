// Package cache 链接元数据 Redis 缓存封装。
// key = linkmeta:<sha256(url)>；命中结果 TTL 24h，抓取失败/非 html 的负结果 TTL 5min。
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/yourcompany/im-server/internal/store"
)

const (
	linkMetaTTL    = 24 * time.Hour
	linkMetaNegTTL = 5 * time.Minute
	linkMetaNegVal = "-" // 负结果哨兵
)

// LinkMeta OG 元数据（缓存内容）。
type LinkMeta struct {
	Title       string `json:"title"`
	Icon        string `json:"icon"`  // 站点 favicon（link rel=icon），缺失时回退为 og:image
	Image       string `json:"image"` // 封面大图（og:image）
	Description string `json:"description"`
}

func linkMetaKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return "linkmeta:" + hex.EncodeToString(h[:])
}

// GetLinkMeta 读取缓存；命中返回 (meta,true)；未命中或负结果返回 (_,false)。
// 负结果（哨兵）也返回 false，交由调用方走降级分支，不视为错误。
func GetLinkMeta(ctx context.Context, raw string) (LinkMeta, bool) {
	v, err := store.RDB.Get(ctx, linkMetaKey(raw)).Result()
	if err != nil {
		return LinkMeta{}, false
	}
	if v == linkMetaNegVal {
		return LinkMeta{}, false
	}
	var m LinkMeta
	if json.Unmarshal([]byte(v), &m) != nil {
		return LinkMeta{}, false
	}
	return m, true
}

// SetLinkMeta 写入 OG 元数据（TTL 24h）。
func SetLinkMeta(ctx context.Context, raw string, m LinkMeta) {
	b, _ := json.Marshal(m)
	store.RDB.Set(ctx, linkMetaKey(raw), string(b), linkMetaTTL)
}

// SetLinkMetaNegative 写入负结果（TTL 5min），避免对失败 URL 短时间反复抓取。
func SetLinkMetaNegative(ctx context.Context, raw string) {
	store.RDB.Set(ctx, linkMetaKey(raw), linkMetaNegVal, linkMetaNegTTL)
}
