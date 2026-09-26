package service

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/store"
)

// ============ 客服 ============
// 后台把普通用户设为客服（user.role = RoleKefu = 3）；
// 系统配置 kefu_config：是否自动添加客服 + 添加方式（轮流 round / 全部 all）。
// 新用户注册后按配置自动与其建立好友关系（不走申请审批，仿 FriendRequestHandle 的双向 FirstOrCreate）。

// KefuConfig 客服配置（存 sys_config 键 kefu_config，通用 PUT /admin/configs/:key 整体 JSON 存取）
type KefuConfig struct {
	AutoAdd  bool   `json:"autoAdd"`            // 是否自动添加客服
	Mode     string `json:"mode"`               // "round" 轮流 / "all" 全部
	Greeting string `json:"greeting,omitempty"` // 客服自动打招呼内容（空字符串 = 不发；支持 {nickname} 占位符替换为客服昵称）
}

// DefaultKefuGreeting 默认打招呼文案（KefuConfigGet 在 greeting 为空时返回，让前端首次进入有占位可改）
const DefaultKefuGreeting = "你好，我是 {nickname}，很高兴为您服务~"

// KefuConfigGet 读客服配置（默认关闭 + 轮流 + 默认打招呼）。
// 注意：SysConfigGet 对 {"value": {...}} 包装会解包返回内层对象（map[string]interface{}），
// 这里 JSON round-trip 转回结构体；mode 非法时按 round 处理；greeting 为空时填充默认文案。
//
// 可观测性约定：三个「回落默认值」分支在返回前必须打一条 [kefu] config ... 日志。
// 原因：这三种情况最终都表现为 autoAdd=false（下游只打 [kefu] skip: autoAdd disabled），
// 但修法完全不同——「sys_config 里根本没这行」要补配置，「存的值解析不了」要改存的值格式。
// 不打日志的话用户看到 autoAdd=false 无法区分该补配置还是该改格式。
func KefuConfigGet(ctx context.Context) KefuConfig {
	def := KefuConfig{AutoAdd: false, Mode: "round", Greeting: DefaultKefuGreeting}
	v := SysConfigGet(ctx, "kefu_config", nil)
	if v == nil {
		// (a) sys_config 表里没有 config_key = 'kefu_config' 的行（后台没保存成功 / 从未 seed）
		log.Printf("[kefu] config missing: sys_config has no row with key=kefu_config, fallback to defaults (autoAdd=false)")
		return def
	}
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("[kefu] config marshal failed: %v, fallback to defaults", err)
		return def
	}
	var kc KefuConfig
	if err := json.Unmarshal(b, &kc); err != nil {
		// (b) 这行存在但存的不是对象（如裸 true / 字符串 / 数字）→ 解析失败 → 当作关闭。
		// 用反引号原始字符串，避免文案里的双引号需要转义；同时打印原始存储值便于排查。
		log.Printf(`[kefu] config parse failed: %v — 请检查 sys_config.kefu_config 存的是否为对象格式，例如 {"autoAdd":true,"mode":"round","greeting":"..."}；存成裸 true 或字符串会导致解析失败并被当作关闭 — raw=%s`, err, string(b))
		return def
	}
	if kc.Mode != "all" {
		kc.Mode = "round"
	}
	if kc.Greeting == "" {
		kc.Greeting = DefaultKefuGreeting
	}
	// 解析成功：确认配置确实读到了（autoAdd=false 时用来排除"读不到/解析失败"这两种可能）
	log.Printf("[kefu] config loaded: autoAdd=%v mode=%s", kc.AutoAdd, kc.Mode)
	return kc
}

// KefuList 在职客服列表：role=RoleKefu 且状态正常（StatusNormal=1；StatusDisabled=2 为封禁，不参与分配）
func KefuList(ctx context.Context) ([]model.User, error) {
	var list []model.User
	err := store.DB.Where("role = ? AND status = ?", model.RoleKefu, model.StatusNormal).
		Order("id ASC").Find(&list).Error
	return list, err
}

// KefuAddForUser 新用户注册后按配置自动添加客服好友（失败不阻断注册，调用方记 log）
//
// 可观测性约定：所有「跳过」分支在返回前必须打一条 [kefu] skip: ... 日志。
// 历史上这些分支直接 return nil，调用方只在 err != nil 时打日志，导致
// 「后台已开启自动加客服，但新用户没加上，且服务端一行日志都没有」无法自证。
func KefuAddForUser(ctx context.Context, userID int64) error {
	if userID <= 0 {
		log.Printf("[kefu] skip: invalid userID=%d", userID)
		return nil
	}
	kc := KefuConfigGet(ctx)
	if !kc.AutoAdd {
		log.Printf("[kefu] skip: autoAdd disabled (user=%d, kefu_config missing or autoAdd=false)", userID)
		return nil
	}
	list, err := KefuList(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		// 客服不是配 ID，而是 user 表里 role=3(RoleKefu) 且 status=1(StatusNormal) 的行。
		// 后台「清空数据→用户数据」会按 role <> 2 删除用户，把 role=3 的客服一起删掉，
		// 这是「以前可以、现在不行」最常见的成因：去「用户管理」把某个用户设为客服即可恢复。
		log.Printf("[kefu] skip: no available kefu user (role=3 AND status=1) in user table (user=%d)", userID)
		return nil
	}

	// bound 统计本次实际建立好友关系的客服数，用于成功路径的 [kefu] ok 日志
	bound := 0

	if kc.Mode == "all" {
		for _, k := range list {
			if err := kefuBind(ctx, userID, k.ID); err != nil {
				log.Printf("[kefu] bind failed after %d/%d kefu bound (user=%d, kefu=%d): %v", bound, len(list), userID, k.ID, err)
				return err
			}
			bound++
			// 加完好友顺手发一条招呼（失败仅 log，不阻断后续客服分配）
			kefuGreet(ctx, userID, k, kc.Greeting)
		}
		log.Printf("[kefu] ok: bound %d kefu to user %d", bound, userID)
		return nil
	}

	// round：sys_config 键 kefu_rr_index 存计数器（读→+1→写回，非严格原子可接受）
	idx := 0
	if v := SysConfigGet(ctx, "kefu_rr_index", nil); v != nil {
		if f, ok := v.(float64); ok && f >= 0 {
			idx = int(f)
		}
	}
	// 计数器写回失败不阻断：轮流分配会退化成「总是取第 idx 个客服」，但好友关系仍然建立成功
	if err := SysConfigSet(ctx, "kefu_rr_index", idx+1); err != nil {
		log.Printf("[kefu] persist kefu_rr_index failed (user=%d, idx=%d): %v", userID, idx+1, err)
	}
	k := list[idx%len(list)]
	if err := kefuBind(ctx, userID, k.ID); err != nil {
		log.Printf("[kefu] bind failed (user=%d, kefu=%d, mode=round): %v", userID, k.ID, err)
		return err
	}
	bound++
	kefuGreet(ctx, userID, k, kc.Greeting)
	log.Printf("[kefu] ok: bound %d kefu to user %d", bound, userID)
	return nil
}

// kefuGreet 客服自动打招呼：客服以自己身份向新用户发一条文本消息。
// 行为对齐 AssistantNotify（CreateDirect + SendMessage），复用同一个单聊会话，
// 消息走 WS 广播 + 极光离线推送，无需新接口。
// greeting 为空、客服昵称为空时直接跳过（不打扰）。
func kefuGreet(ctx context.Context, userID int64, kefu model.User, greeting string) {
	if greeting == "" || userID <= 0 || kefu.ID <= 0 {
		return
	}
	text := greeting
	if kefu.Nickname != "" {
		text = strings.ReplaceAll(greeting, "{nickname}", kefu.Nickname)
	}
	conv, err := CreateDirect(ctx, userID, kefu.ID)
	if err != nil || conv == nil {
		log.Printf("[kefuGreet] CreateDirect failed: user=%d kefu=%d err=%v", userID, kefu.ID, err)
		return
	}
	if _, err := SendMessage(ctx, kefu.ID, &SendMsgReq{
		ConversationID: conv.ID,
		Type:           model.MsgText,
		Content:        text,
		ClientMsgID:    newUUID(), // 防止重复发送场景下因空 ClientMsgID 被服务端生成多份
	}); err != nil {
		log.Printf("[kefuGreet] SendMessage failed: user=%d kefu=%d err=%v", userID, kefu.ID, err)
	}
}

// kefuBind 双向建立 user↔kefu 好友关系（已存在则跳过，仿 friend.go FriendRequestHandle 的 FirstOrCreate 写法）
func kefuBind(ctx context.Context, userID, kefuID int64) error {
	if userID <= 0 || kefuID <= 0 || userID == kefuID {
		return nil
	}
	if err := store.DB.FirstOrCreate(&model.FriendRelation{UserID: userID, FriendID: kefuID, Source: 1}, model.FriendRelation{UserID: userID, FriendID: kefuID}).Error; err != nil {
		return err
	}
	return store.DB.FirstOrCreate(&model.FriendRelation{UserID: kefuID, FriendID: userID, Source: 1}, model.FriendRelation{UserID: kefuID, FriendID: userID}).Error
}
