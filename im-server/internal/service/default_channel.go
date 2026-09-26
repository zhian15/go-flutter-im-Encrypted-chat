package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/store"
)

// ============ 注册默认关注频道 ============
// 与 default_group.go 同一套模式：后台「系统设置」配置 sys_config 键
// default_channel_config，格式 {"enabled":true,"channelIds":["雪花ID字符串"]}；
// 新用户注册（含游客注册/游客重复登录）成功后自动**关注**所选频道
// （频道 = conversation type=3，关注 = conversation_member 插一行，见 channel.go）。
// 【铁律】频道 ID 必须**字符串**存取（雪花 ID 超 JS 2^53，同 default_group_config 注释）。

// DefaultChannelConfig 注册默认关注频道配置
type DefaultChannelConfig struct {
	Enabled    bool        `json:"enabled"`              // 是否开启注册自动关注频道
	ChannelIds GroupIdList `json:"channelIds,omitempty"` // 要关注的频道 ID 列表（复用 GroupIdList 的字符串编解码）
	// WelcomeMsg 欢迎语模板（后台聊天设置下发，空=用默认文案「欢迎关注「频道名」」）。
	// 占位符全量替换（可出现多次）：{nickname}/{订阅人昵称}（订阅用户昵称）、
	// {channelName}/{频道名}（频道名 nameZh 优先），英文为原始写法、中文为别名；
	// 占位符可不放。所有关注入口（注册自动关注/手动关注/频道主邀请）统一下发本模板，
	// 昵称查询失败时降级为默认文案，不阻塞关注流程。
	WelcomeMsg string `json:"welcomeMsg,omitempty"`
}

// DefaultChannelConfigGet 读配置（默认关闭），可观测性日志约定同 DefaultGroupConfigGet
func DefaultChannelConfigGet(ctx context.Context) DefaultChannelConfig {
	def := DefaultChannelConfig{Enabled: false}
	v := SysConfigGet(ctx, "default_channel_config", nil)
	if v == nil {
		log.Printf("[default-channel] config missing: sys_config has no row with key=default_channel_config, fallback to defaults (enabled=false)")
		return def
	}
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("[default-channel] config marshal failed: %v, fallback to defaults", err)
		return def
	}
	var dc DefaultChannelConfig
	if err := json.Unmarshal(b, &dc); err != nil {
		log.Printf(`[default-channel] config parse failed: %v — 请检查 sys_config.default_channel_config 存的是否为对象格式，例如 {"enabled":true,"channelIds":["353..."]} — raw=%s`, err, string(b))
		return def
	}
	if dc.ChannelIds == nil {
		dc.ChannelIds = []int64{}
	}
	return dc
}

// DefaultChannelFollowForUser 注册/游客登录成功后按配置自动关注默认频道。
// 失败不阻断注册（与 DefaultGroupJoinForUser 同风格，只打日志）；
// 幂等：已是成员直接跳过（也不发欢迎消息）；
// 私密频道（qrJoinEnabled=0）也允许——这是频道主的配置行为，等价于频道主邀请。
// 新关注的频道由小助手发欢迎语（afterChannelFollow），失败不阻断注册。
// 单个频道失败只记日志并继续，不让一个脏配置拖垮其余频道。
func DefaultChannelFollowForUser(ctx context.Context, userID int64) error {
	if userID <= 0 {
		log.Printf("[default-channel] skip: invalid userID=%d", userID)
		return nil
	}
	// 功能开关总闸（2026-09-22 需求2）：后台 sys_config channel_enabled，
	// 默认开启；关闭后注册/游客登录一律不自动关注频道（App 新建频道入口同步隐藏）
	if v := SysConfigGet(ctx, "channel_enabled", true); !boolVal(v) {
		log.Printf("[default-channel] skip: channel_enabled=false (user=%d)", userID)
		return nil
	}
	dc := DefaultChannelConfigGet(ctx)
	if !dc.Enabled {
		log.Printf("[default-channel] skip: disabled (user=%d, default_channel_config missing or enabled=false)", userID)
		return nil
	}
	if len(dc.ChannelIds) == 0 {
		log.Printf("[default-channel] skip: enabled but channelIds is empty (user=%d)", userID)
		return nil
	}
	followed := 0
	changed := false
	for _, cid := range dc.ChannelIds {
		if cid <= 0 {
			continue
		}
		var conv model.Conversation
		if err := store.DB.WithContext(ctx).First(&conv, cid).Error; err != nil {
			log.Printf("[default-channel] skip channel %d: conversation not found (user=%d)", cid, userID)
			continue
		}
		if conv.Type != model.ConvChannel || conv.Status != model.ConvNormal {
			log.Printf("[default-channel] skip channel %d: not an active channel (type=%d status=%d, user=%d)", cid, conv.Type, conv.Status, userID)
			continue
		}
		// 满员校验（MaxMembers=0 表示不限）
		if conv.MaxMembers > 0 {
			var cnt int64
			store.DB.WithContext(ctx).Model(&model.ConversationMember{}).
				Where("conversation_id = ?", cid).Count(&cnt)
			if cnt >= int64(conv.MaxMembers) {
				log.Printf("[default-channel] skip channel %d: full (%d/%d, user=%d)", cid, cnt, conv.MaxMembers, userID)
				continue
			}
		}
		var exist int64
		store.DB.WithContext(ctx).Model(&model.ConversationMember{}).
			Where("conversation_id = ? AND user_id = ?", cid, userID).Count(&exist)
		if exist > 0 {
			followed++ // 已关注也算成功（幂等）
			continue
		}
		if err := store.DB.WithContext(ctx).Create(&model.ConversationMember{
			ConversationID: cid, UserID: userID, Role: model.MemberNormal, JoinedAt: time.Now(),
		}).Error; err != nil {
			log.Printf("[default-channel] follow channel %d failed (user=%d): %v", cid, userID, err)
			continue
		}
		followed++
		changed = true
		// 新订阅欢迎语（小助手身份，幂等补挂成员行）；下发后台配置的自定义模板
		// welcomeMsg（支持 {nickname}/{channelName} 占位符）；失败只记日志不阻断注册
		afterChannelFollow(ctx, cid, userID, dc.WelcomeMsg)
	}
	// 【修 R-17】新增关注后失效成员缓存，新关注者立即可收频道消息
	if changed {
		for _, cid := range dc.ChannelIds {
			if cid > 0 {
				invalidateConvMembers(ctx, cid)
			}
		}
	}
	log.Printf("[default-channel] ok: user %d followed %d/%d configured channels", userID, followed, len(dc.ChannelIds))
	return nil
}
