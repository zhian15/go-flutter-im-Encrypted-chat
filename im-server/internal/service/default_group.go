package service

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/store"
)

// ============ 注册默认加入群聊 ============
// 后台「系统设置」可配置：开启后，新用户注册（含游客注册）自动加入所选群聊。
// 配置存 sys_config 键 default_group_config（通用 PUT /admin/configs/:key 整体 JSON 存取），
// 格式 {"enabled":true,"groupIds":["353...","354..."]}。与 kefu_config 同一套模式。
// 【铁律】群 ID 必须**字符串**存取：雪花 ID 超过 JS 2^53，前端 Number 化会丢精度
// （曾因此存出 353794126644248600 这种结尾被舍入的假 ID → 查群 not found）。

// GroupIdList 群 ID 列表：JSON 进出全用字符串（容忍历史数据存成数字，入参时解析回 int64）
type GroupIdList []int64

// MarshalJSON 输出为字符串数组，避免 JS 端 JSON.parse 时丢精度
func (g GroupIdList) MarshalJSON() ([]byte, error) {
	out := make([]string, len(g))
	for i, v := range g {
		out[i] = strconv.FormatInt(v, 10)
	}
	return json.Marshal(out)
}

// UnmarshalJSON 接受字符串或数字数组（历史数字配置也能读，但数字本身可能已丢精度，
// 只能靠后台重新保存纠正）
func (g *GroupIdList) UnmarshalJSON(b []byte) error {
	var raw []interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make([]int64, 0, len(raw))
	for _, x := range raw {
		switch v := x.(type) {
		case string:
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
				out = append(out, n)
			}
		case float64:
			out = append(out, int64(v))
		}
	}
	*g = out
	return nil
}

// DefaultGroupConfig 注册默认进群配置
type DefaultGroupConfig struct {
	Enabled  bool        `json:"enabled"`            // 是否开启注册自动进群
	GroupIds GroupIdList `json:"groupIds,omitempty"` // 要加入的群会话 ID 列表（字符串存取）
}

// DefaultGroupConfigGet 读配置（默认关闭）。
// 可观测性约定与 KefuConfigGet 相同：缺行 / 解析失败各打一条 [default-group] 日志，
// 便于区分「后台没保存成功」和「存的值格式不对」。
func DefaultGroupConfigGet(ctx context.Context) DefaultGroupConfig {
	def := DefaultGroupConfig{Enabled: false}
	v := SysConfigGet(ctx, "default_group_config", nil)
	if v == nil {
		log.Printf("[default-group] config missing: sys_config has no row with key=default_group_config, fallback to defaults (enabled=false)")
		return def
	}
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("[default-group] config marshal failed: %v, fallback to defaults", err)
		return def
	}
	var dc DefaultGroupConfig
	if err := json.Unmarshal(b, &dc); err != nil {
		log.Printf(`[default-group] config parse failed: %v — 请检查 sys_config.default_group_config 存的是否为对象格式，例如 {"enabled":true,"groupIds":[1,2]} — raw=%s`, err, string(b))
		return def
	}
	if dc.GroupIds == nil {
		dc.GroupIds = []int64{}
	}
	return dc
}

// DefaultGroupJoinForUser 新用户注册后按配置自动加入默认群聊。
// 与 KefuAddForUser 同一调用时机（注册成功后、失败不阻断注册）；
// 幂等：已是成员直接跳过；进群为静默插入（不发群系统消息，避免每次注册刷屏）。
// 单个群失败只记日志并继续下一个群，不让一个脏配置拖垮其余群。
func DefaultGroupJoinForUser(ctx context.Context, userID int64) error {
	if userID <= 0 {
		log.Printf("[default-group] skip: invalid userID=%d", userID)
		return nil
	}
	dc := DefaultGroupConfigGet(ctx)
	if !dc.Enabled {
		log.Printf("[default-group] skip: disabled (user=%d, default_group_config missing or enabled=false)", userID)
		return nil
	}
	if len(dc.GroupIds) == 0 {
		log.Printf("[default-group] skip: enabled but groupIds is empty (user=%d)", userID)
		return nil
	}
	joined := 0
	for _, gid := range dc.GroupIds {
		if gid <= 0 {
			continue
		}
		var conv model.Conversation
		if err := store.DB.WithContext(ctx).First(&conv, gid).Error; err != nil {
			log.Printf("[default-group] skip group %d: conversation not found (user=%d)", gid, userID)
			continue
		}
		if conv.Type != model.ConvGroup || conv.Status != model.ConvNormal {
			log.Printf("[default-group] skip group %d: not an active group (type=%d status=%d, user=%d)", gid, conv.Type, conv.Status, userID)
			continue
		}
		var cnt int64
		store.DB.WithContext(ctx).Model(&model.ConversationMember{}).
			Where("conversation_id = ?", gid).Count(&cnt)
		if cnt >= int64(conv.MaxMembers) {
			log.Printf("[default-group] skip group %d: full (%d/%d, user=%d)", gid, cnt, conv.MaxMembers, userID)
			continue
		}
		var exist int64
		store.DB.WithContext(ctx).Model(&model.ConversationMember{}).
			Where("conversation_id = ? AND user_id = ?", gid, userID).Count(&exist)
		if exist > 0 {
			joined++ // 已是成员也算成功（幂等）
			continue
		}
		if err := store.DB.WithContext(ctx).Create(&model.ConversationMember{
			ConversationID: gid, UserID: userID, Role: model.MemberNormal, JoinedAt: time.Now(),
		}).Error; err != nil {
			log.Printf("[default-group] join group %d failed (user=%d): %v", gid, userID, err)
			continue
		}
		joined++
	}
	log.Printf("[default-group] ok: user %d joined %d/%d configured groups", userID, joined, len(dc.GroupIds))
	return nil
}
