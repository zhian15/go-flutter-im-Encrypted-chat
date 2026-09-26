package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ============ 智能小助手 ============
// 后台配置：助手昵称/头像/新注册自动添加开关
// 助手用固定虚拟 uid（-1 不占真实用户表），向用户发消息
// （虚拟 UID 常量 AssistantUID = -1 定义于 moment.go，全 service 包共用）

// AssistantConfig 小助手配置（存 sys_config assistant_config JSON）
type AssistantConfig struct {
	Enabled     bool   `json:"enabled"`
	Name        string `json:"name"`
	Avatar      string `json:"avatar"`
	AutoAdd     bool   `json:"autoAdd"`     // 新注册是否自动添加助手
	WelcomeText string `json:"welcomeText"` // 自动添加后发送的欢迎语
}

// GetAssistantConfig 读小助手配置（默认关闭）
func GetAssistantConfig(ctx context.Context, cfg *config.Config) AssistantConfig {
	def := AssistantConfig{
		Enabled:     false,
		Name:        "小助手",
		Avatar:      "",
		AutoAdd:     false,
		WelcomeText: "你好，我是小助手，有问题随时找我～",
	}
	v := SysConfigGet(ctx, "assistant_config", "")
	if s, ok := v.(string); ok && s != "" && s != "null" {
		var ac AssistantConfig
		if jsonUnmarshal([]byte(s), &ac) == nil {
			return ac
		}
	}
	return def
}

// SaveAssistantConfig 保存小助手配置
func SaveAssistantConfig(ctx context.Context, ac AssistantConfig) error {
	b, _ := jsonMarshal(ac)
	return SysConfigSet(ctx, "assistant_config", string(b))
}

// AssistantAddForUser 新用户注册后自动添加小助手 + 发送欢迎语
func AssistantAddForUser(ctx context.Context, cfg *config.Config, userID int64) error {
	// 功能开关总闸（2026-09-22 需求2）：后台 sys_config assistant_enabled，
	// 默认开启；关闭后注册/游客登录一律不自动添加小助手（App 通讯录入口同步隐藏）
	if v := SysConfigGet(ctx, "assistant_enabled", true); !boolVal(v) {
		return nil
	}
	ac := GetAssistantConfig(ctx, cfg)
	if !ac.Enabled || !ac.AutoAdd || userID <= 0 {
		return nil
	}
	// 建会话（assistant 虚拟 uid = -1）
	conv, err := CreateDirect(ctx, userID, -1)
	if err != nil {
		return err
	}
	// 发欢迎消息
	if ac.WelcomeText != "" {
		SendMessage(ctx, -1, &SendMsgReq{
			ConversationID: conv.ID,
			Type:           1,
			Content:        ac.WelcomeText,
		})
	}
	return nil
}

// AssistantPush 以助手身份向指定用户推送消息（后台新建推送：文字/图片）
func AssistantPush(ctx context.Context, cfg *config.Config, userID int64, content string, fileURL string) error {
	ac := GetAssistantConfig(ctx, cfg)
	if !ac.Enabled {
		return &errs.Err{Code: 500, Msg: "小助手未启用"}
	}
	if userID <= 0 {
		return &errs.Err{Code: 1001, Msg: "用户无效"}
	}
	conv, err := CreateDirect(ctx, userID, -1)
	if err != nil {
		return err
	}
	req := &SendMsgReq{
		ConversationID: conv.ID,
		Type:           1,
		Content:        content,
	}
	if fileURL != "" {
		req.Type = 2 // 图片
		req.File = map[string]interface{}{
			"url":  fileURL,
			"name": "assistant-image",
			"size": 0,
		}
	}
	_, err = SendMessage(ctx, -1, req)
	return err
}

// AssistantNotify 以小助手（uid=-1）身份向指定用户发一条系统提醒（文字）。
// 与 AssistantPush 的区别：不检查 Enabled 开关——充值/提现审核提醒不应受
// "小助手自动添加"开关影响。内部 SendMessage 已完成落库、未读计数、
// PublishEvent WS 广播与离线推送，无需再单独推事件。
func AssistantNotify(ctx context.Context, userID int64, content string) error {
	if userID <= 0 {
		return &errs.Err{Code: 1001, Msg: "用户无效"}
	}
	conv, err := CreateDirect(ctx, userID, -1)
	if err != nil {
		return err
	}
	_, err = SendMessage(ctx, -1, &SendMsgReq{
		ConversationID: conv.ID,
		Type:           1,
		Content:        content,
	})
	return err
}

func jsonMarshal(v interface{}) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v interface{}) error { return json.Unmarshal(b, v) }

// AssistantAvatar 后台配置的小助手头像（未设置返回空）
// GetAssistantConfig 不依赖 cfg（仅读 sys_config），传 nil 安全
func AssistantAvatar(ctx context.Context) string {
	return GetAssistantConfig(ctx, nil).Avatar
}

// ============ 后台「助手会话消息」管理 ============

// AssistantConvItem 会话列表项（助手 ↔ 用户）
type AssistantConvItem struct {
	// 注意：UserID 本身就是 string，不能再加 ",string" 选项——
	// Go 的 ,string 会把字符串值再 JSON 编码一层（值变成 "\"123\""），
	// 前端原样传回后 ParseInt 失败 → 后台打开助手会话一直报「参数错误」。
	UserID      string         `json:"userId"`
	Nickname    string         `json:"nickname"`
	Account     string         `json:"account"`
	Avatar      string         `json:"avatar"`
	LastMessage *model.Message `json:"lastMessage"`
}

// AssistantConvPage 会话列表返回：list 为按最近消息倒序截断后的前 limit 条，total 为会话总数
type AssistantConvPage struct {
	List  []AssistantConvItem `json:"list"`
	Total int64               `json:"total"`
}

// AssistantConvList 所有与小助手的会话（后台查看谁能看助手、最后一条消息）。
// 排序：最近有消息的排最前（msg_id 降序），从未有消息的排最后；只返回最近 limit 条
// （会话可能成百上千，全量直出又长又乱——后台默认看最近活跃的即可）。
func AssistantConvList(ctx context.Context, limit int) (AssistantConvPage, error) {
	var members []model.ConversationMember
	if err := store.DB.Where("user_id = ?", -1).Find(&members).Error; err != nil {
		return AssistantConvPage{}, err
	}
	out := make([]AssistantConvItem, 0, len(members))
	for _, m := range members {
		otherID := directOtherID(ctx, m.ConversationID, -1)
		if otherID <= 0 {
			continue
		}
		it := AssistantConvItem{UserID: fmt.Sprintf("%d", otherID)}
		var u model.User
		if err := store.DB.First(&u, otherID).Error; err == nil {
			it.Nickname = u.Nickname
			it.Account = u.Account
			it.Avatar = u.Avatar
		}
		var last model.Message
		if err := msgColl().FindOne(ctx,
			bson.M{"conversation_id": m.ConversationID},
			options.FindOne().SetSort(bson.D{{Key: "msg_id", Value: -1}})).Decode(&last); err == nil {
			it.LastMessage = &last
		}
		out = append(out, it)
	}
	// 最近活跃倒序：有消息的按最后一条 msg_id 降序；无消息的沉底（按 userID 升序稳定排列）
	sort.Slice(out, func(i, j int) bool {
		mi, mj := int64(0), int64(0)
		if out[i].LastMessage != nil {
			mi = out[i].LastMessage.MsgID
		}
		if out[j].LastMessage != nil {
			mj = out[j].LastMessage.MsgID
		}
		if mi != mj {
			return mi > mj
		}
		return out[i].UserID < out[j].UserID
	})
	total := int64(len(out))
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return AssistantConvPage{List: out, Total: total}, nil
}

// AssistantMessages 某用户与助手的会话消息（复用 History；助手 uid=-1 是会话成员）
// beforeMsgID>0 时向前翻页；返回按时间正序
func AssistantMessages(ctx context.Context, userID, beforeMsgID, limit int64) ([]model.Message, error) {
	if userID <= 0 {
		return nil, &errs.Err{Code: 1001, Msg: "参数错误"}
	}
	// 已有会话直接返回；没有则补建（后台主动回复时也能用）
	conv, err := CreateDirect(ctx, userID, -1)
	if err != nil {
		return nil, err
	}
	return History(ctx, -1, conv.ID, beforeMsgID, limit)
}
