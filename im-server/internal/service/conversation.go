package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/pkg/id"
	"github.com/yourcompany/im-server/internal/store"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/redis/go-redis/v9"
)

// ============ 创建会话 ============

// CreateDirect 创建/获取单聊会话（双方各一条 member 记录）
//
// 好友门控（需求「对方同意后才能聊天」）：仅拦截**新建**会话——已有会话原样返回，
// 删好友/拉黑后历史会话能否续聊是独立议题（侦察报告 §5，本轮明确不做）。
// 调用顺序契约（改这里前先核对四处调用点）：
//   - friend.go FriendRequestHandle：先双向 FirstOrCreate friend_relation（:131-134）再调本函数（:136）→ 天然放行；
//   - kefu.go kefuGreet：kefuBind（双向关系）先于 kefuGreet（kefu.go:107/:131 → :153）→ 天然放行；
//   - invite_friend.go：只调 kefuBind 建关系，不直接调本函数 → 不受影响；
//   - assistant.go 四处 CreateDirect(ctx, uid, -1)：虚拟助手 uid=-1 无 user 行也无好友关系 → 下方 otherID>=0 豁免。
func CreateDirect(ctx context.Context, userID, otherID int64) (*model.Conversation, error) {
	if userID == otherID {
		return nil, &errs.Err{Code: 1001, Msg: "不能和自己聊天"}
	}
	// 查已有单聊会话（ORDER BY id ASC：万一存量库有重复单聊，恒定取最早那条，
	// 保证同端/跨端拿到的会话 ID 一致）
	var conv model.Conversation
	err := store.DB.Raw(`
		SELECT c.* FROM conversation c
		JOIN conversation_member m1 ON m1.conversation_id = c.id AND m1.user_id = ?
		JOIN conversation_member m2 ON m2.conversation_id = c.id AND m2.user_id = ?
		WHERE c.type = 1 AND c.status = 1
		ORDER BY c.id ASC LIMIT 1`, userID, otherID).Scan(&conv).Error
	if err == nil && conv.ID > 0 {
		fillDirectAvatar(ctx, &conv, otherID)
		return &conv, nil
	}

	// 好友门控：只在「要新建会话」时校验双向 friend_relation（isFriend 两个方向一次 COUNT）。
	// otherID >= 0 是真实用户；虚拟助手 -1 豁免（见函数注释的调用顺序契约）。
	if otherID >= 0 && !isFriend(ctx, userID, otherID) {
		return nil, errs.NotFriends
	}

	// 创建。DirectKey = 单聊配对键（"小UID:大UID"）+ uk_direct_key 唯一索引兜底并发：
	// 上面的「先查后建」在两个请求同时通过查重时会给同一对人各建一条单聊
	// （会话列表同一个人出现两条，实测踩坑）；带键创建撞唯一索引的那个请求
	// 改为查回对方刚建好的那条原样返回，保证任何时刻每对人只有一条单聊。
	now := time.Now()
	cid := id.Next()
	lo, hi := userID, otherID
	if lo > hi {
		lo, hi = hi, lo
	}
	pairKey := fmt.Sprintf("%d:%d", lo, hi)
	conv = model.Conversation{ID: cid, Type: model.ConvDirect, MaxMembers: 2, Status: model.ConvNormal, CreatedAt: now, DirectKey: &pairKey}
	if err := store.DB.Create(&conv).Error; err != nil {
		// 并发竞态兜底：另一请求刚用同一对用户建好会话（唯一索引拒绝重复插入）
		if strings.Contains(err.Error(), "uk_direct_key") || strings.Contains(err.Error(), "1062") {
			var exist model.Conversation
			if e := store.DB.Raw(`
				SELECT c.* FROM conversation c
				JOIN conversation_member m1 ON m1.conversation_id = c.id AND m1.user_id = ?
				JOIN conversation_member m2 ON m2.conversation_id = c.id AND m2.user_id = ?
				WHERE c.type = 1 AND c.status = 1
				ORDER BY c.id ASC LIMIT 1`, userID, otherID).Scan(&exist).Error; e == nil && exist.ID > 0 {
				fillDirectAvatar(ctx, &exist, otherID)
				return &exist, nil
			}
		}
		return nil, err
	}
	store.DB.Create(&model.ConversationMember{ConversationID: cid, UserID: userID, Role: model.MemberNormal, JoinedAt: now})
	store.DB.Create(&model.ConversationMember{ConversationID: cid, UserID: otherID, Role: model.MemberNormal, JoinedAt: now})
	fillDirectAvatar(ctx, &conv, otherID)
	return &conv, nil
}

// fillDirectAvatar 单聊会话裸记录通常不带头像：实时补对方用户头像与最近上线时间。
// 与 ConversationList 的补齐逻辑对齐——否则通讯录"创建会话→进资料页"路径
// 拿到的 avatar 恒为空，资料页头图只能显示首字母（聊天窗口走列表接口所以正常）。
func fillDirectAvatar(ctx context.Context, conv *model.Conversation, otherID int64) {
	u, err := GetUserDetail(ctx, otherID)
	if err != nil {
		return
	}
	conv.LastLoginAt = u.LastLoginAt
	if conv.Avatar == "" && u.Avatar != "" {
		conv.Avatar = u.Avatar
	}
}

// groupMaxMembers 群人数上限全局配置（sys_config key=group_max_members）。
// 0 = 不限（默认）；只约束"新建群"——已有群的 MaxMembers 不受实时影响。
// intVal 同包（admin.go）：数字 JSON 反序列化为 float64、后台旧数据可能存字符串，统一在此转 int。
func groupMaxMembers(ctx context.Context) int {
	return intVal(SysConfigGet(ctx, "group_max_members", 0), 0)
}

// CreateGroup 创建群聊
func CreateGroup(ctx context.Context, ownerID int64, nameZh, nameEn string, memberIDs []int64) (*model.Conversation, error) {
	// 全局群上限：capN>0 时（群主 + 成员列表）不得超过；capN==0 表示不限
	capN := groupMaxMembers(ctx)
	if capN > 0 && len(memberIDs)+1 > capN {
		return nil, errs.GroupFull
	}
	cid := id.Next()
	now := time.Now()
	conv := model.Conversation{
		ID: cid, Type: model.ConvGroup, NameZh: nameZh, NameEn: nameEn,
		OwnerID: ownerID, MaxMembers: capN, Status: model.ConvNormal, CreatedAt: now,
	}
	if err := store.DB.Create(&conv).Error; err != nil {
		return nil, err
	}
	store.DB.Create(&model.ConversationMember{ConversationID: cid, UserID: ownerID, Role: model.MemberOwner, JoinedAt: now})
	for _, uid := range memberIDs {
		if uid == ownerID {
			continue
		}
		store.DB.Create(&model.ConversationMember{ConversationID: cid, UserID: uid, Role: model.MemberNormal, JoinedAt: now})
	}
	return &conv, nil
}

// ============ 会话列表（聚合未读 + 最后消息） ============

// ConvItem 会话列表项
type ConvItem struct {
	Conversation     model.Conversation `json:"conversation"`
	ShortID          string             `json:"shortId"` // 会话自定义唯一 ID（频道 short_id，未设置为空串）
	Unread           int64              `json:"unread"`
	LastMessage      *model.Message     `json:"lastMessage"`
	MemberCount      int64              `json:"memberCount"`
	Mute             bool               `json:"mute"`
	Pinned           bool               `json:"pinned"`
	Archived         bool               `json:"archived"`         // 个人归档开关（PC 归档入口聚合用）
	ConversationName string             `json:"conversationName"` // 单聊显示对方昵称
	PeerID           int64              `json:"peerId,string"`    // 单聊对方雪花用户 ID（PC 转账 toUserId 精确识别收款人）
	PeerOnline       bool               `json:"peerOnline"`       // 单聊对方是否在线
	PeerOnlineDev    []string           `json:"peerOnlineDev"`    // 对方在线设备
	PeerOnlineZh     string             `json:"peerOnlineZh"`     // 对方在线类型中文（手机在线/H5在线/电脑在线）
	PeerOnlineIP     []string           `json:"peerOnlineIp"`     // 对方在线 IP（需求8）
	PeerShortID      string             `json:"peerShortId"`      // 对方靓号 ID（需求12：小助手固定 10000）
	PeerVipShortID   bool               `json:"peerVipShortId"`   // 对方是否靓号（预留池已绑定）→ 客户端资料页显示「靓ID」徽标
	PeerRole         int                `json:"peerRole"`         // 对方账号角色：1 普通 / 2 管理员 / 3 客服（V 盾只对客服显示）；小助手虚拟账号恒 0
	PeerRemark       string             `json:"peerRemark"`       // 我对对方设置的备注
	PeerDeviceName   string             `json:"peerDeviceName"`   // 对方最近登录设备型号（客户端登录时上报，PC 顶栏「{型号} · 在线」）
}

// ConvMemberInfo 会话成员（含群内角色与好友备注）。
// 内嵌 PublicUser（脱敏白名单）而非 model.User —— 群成员之间互见，直接内嵌 model.User 会把
// 全群人的 balance/frozen/phone/email/registerIP/lastLoginIP/myInviteCode/isGuest/
// guestDeviceId/registerDevice/lastLoginAt 泄露出去。
// 注意：本结构体的 Role 为「群内角色」（1=群主 2=管理员 3=普通成员），与 PublicUser 新增的
// 账号角色 Role（1=普通 2=管理员 3=客服）是两回事；Go 内嵌浅层字段胜出，JSON 的 role 恒等于
// 群内角色，账号角色经新增的 accountRole 字段单独输出（客服勾依据）。
type ConvMemberInfo struct {
	PublicUser
	Role            int    `json:"role"`            // 群内角色：1=群主 2=管理员 3=普通成员
	AccountRole     int    `json:"accountRole"`     // 账号角色：1=普通 2=管理员 3=客服（V 盾客服勾；PublicUser.Role 被 Role 遮蔽，故单独出字段）
	Remark          string `json:"remark"`          // 好友备注（按当前用户视角，非好友为空）
	VipShortID      bool   `json:"vipShortId"`      // 是否靓号（预留池已绑定）→ 客户端资料页显示「靓ID」徽标
	SpeakMutedUntil int64  `json:"speakMutedUntil"` // 禁言截止时间戳（秒），0=未禁言（群主/管理员管理用）
}

// friendRemark 查询当前用户对某好友设置的备注（无则返回空）
func friendRemark(ctx context.Context, userID, friendID int64) string {
	var rel model.FriendRelation
	if err := store.DB.Where("user_id = ? AND friend_id = ?", userID, friendID).
		First(&rel).Error; err != nil {
		return ""
	}
	return rel.Remark
}

// friendRemarkMap 批量查询当前用户对多个好友的备注
func friendRemarkMap(ctx context.Context, userID int64, friendIDs []int64) map[int64]string {
	out := make(map[int64]string, len(friendIDs))
	if len(friendIDs) == 0 {
		return out
	}
	var rels []model.FriendRelation
	store.DB.Where("user_id = ? AND friend_id IN ?", userID, friendIDs).Find(&rels)
	for _, r := range rels {
		if r.Remark != "" {
			out[r.FriendID] = r.Remark
		}
	}
	return out
}

// PeerOnline 查询会话中对方的在线状态（单聊返回对方设备，群聊返回空）
func PeerOnline(ctx context.Context, conv model.Conversation, userID int64) (bool, []string) {
	if conv.Type != model.ConvDirect {
		return false, nil
	}
	otherID := directOtherID(ctx, conv.ID, userID)
	if otherID <= 0 {
		return false, nil
	}
	return IsUserOnline(ctx, otherID)
}

// ConvList 会话列表。
//
// 性能关键：这里必须是「批量查询」，不能回到逐会话循环查库的写法。
// 旧实现每个会话 3-8 次 MySQL + 3-6 次 Redis + 1 次 Mongo（全串行），
// 30 个会话 ≈ 200 次存储往返；任一存储抖动接口就 >10s（客户端 receiveTimeout），
// 多人同时进首页还会打满连接池造成雪崩——表现为「消息列表/发现页加载不出来，
// 杀掉 App 重开才好」。本版本固定 6 条 MySQL + 2 轮 Redis pipeline + 1 条 Mongo 聚合。
func ConvList(ctx context.Context, userID int64) ([]*ConvItem, error) {
	// 1. 我的成员记录（hidden=1 = 用户已「删除会话」，不再出现在列表里）
	var members []model.ConversationMember
	if err := store.DB.Where("user_id = ? AND hidden = 0", userID).Find(&members).Error; err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return []*ConvItem{}, nil
	}
	convIDs := make([]int64, 0, len(members))
	for _, m := range members {
		convIDs = append(convIDs, m.ConversationID)
	}

	// 2. 批量查会话（替代逐个 First）
	var convs []model.Conversation
	if err := store.DB.Where("id IN ?", convIDs).Find(&convs).Error; err != nil {
		return nil, err
	}
	convMap := make(map[int64]*model.Conversation, len(convs))
	for i := range convs {
		convMap[convs[i].ID] = &convs[i]
	}

	// 3. 批量成员数（一条 GROUP BY 替代逐个 COUNT）
	// 口径：排除小助手等虚拟账号（uid<0）的成员行——「N 人订阅/N 成员」指真人，
	// 助手行（uid=-1）是欢迎消息发送身份，不计入展示计数。
	type cntRow struct {
		ConversationID int64
		Cnt            int64
	}
	var cntRows []cntRow
	store.DB.Model(&model.ConversationMember{}).
		Select("conversation_id, COUNT(*) AS cnt").
		Where("conversation_id IN ? AND user_id > 0", convIDs).
		Group("conversation_id").Scan(&cntRows)
	cntMap := make(map[int64]int64, len(cntRows))
	for _, r := range cntRows {
		cntMap[r.ConversationID] = r.Cnt
	}

	// 4. 单聊对方 ID + 对方已读水位（一条 SQL 替代逐个 directOtherID/First）
	directIDs := make([]int64, 0)
	for _, m := range members {
		if c := convMap[m.ConversationID]; c != nil && c.Type == model.ConvDirect && c.Status == model.ConvNormal {
			directIDs = append(directIDs, m.ConversationID)
		}
	}
	otherMap := make(map[int64]int64, len(directIDs))    // convID -> 对方 userID
	peerReadMap := make(map[int64]int64, len(directIDs)) // convID -> 对方 LastReadMsgID
	peerSet := make(map[int64]bool)
	if len(directIDs) > 0 {
		var rows []model.ConversationMember
		store.DB.Select("conversation_id, user_id, last_read_msg_id").
			Where("conversation_id IN ? AND user_id != ?", directIDs, userID).Find(&rows)
		for _, r := range rows {
			if _, dup := otherMap[r.ConversationID]; !dup {
				otherMap[r.ConversationID] = r.UserID
				peerSet[r.UserID] = true
			}
			if r.LastReadMsgID > peerReadMap[r.ConversationID] {
				peerReadMap[r.ConversationID] = r.LastReadMsgID
			}
		}
	}
	peerIDs := make([]int64, 0, len(peerSet))
	for pid := range peerSet {
		peerIDs = append(peerIDs, pid)
	}

	// 5. 批量用户资料（一条 IN 替代逐个 GetUserDetail）
	userMap := make(map[int64]*model.User, len(peerIDs))
	if len(peerIDs) > 0 {
		var users []model.User
		store.DB.Where("id IN ?", peerIDs).Find(&users)
		for i := range users {
			userMap[users[i].ID] = &users[i]
		}
	}

	// 6. 批量好友备注（IN 查询，替代逐个 friendRemark）
	remarkMap := friendRemarkMap(ctx, userID, peerIDs)

	// 7. 批量靓号判定（一条 IN 替代逐个 IsVipShortID）
	vipMap := make(map[int64]bool, len(peerIDs))
	if len(peerIDs) > 0 {
		var rows []model.ReservedShortID
		store.DB.Select("used_by, short_id").
			Where("status = ? AND used_by IN ?", model.ReservedShortIDUsed, peerIDs).Find(&rows)
		for _, r := range rows {
			if r.ShortID != "" {
				vipMap[r.UsedBy] = true
			}
		}
	}

	// 7.5 批量查对方最近登录设备型号（device 表按 last_active_at 最新一行；一条 IN 查询，
	//     维持本函数「批量不逐个」纪律）。登录/游客/扫码登录时客户端上报 deviceName 落库。
	peerDeviceName := make(map[int64]string, len(peerIDs))
	if len(peerIDs) > 0 {
		var drows []model.Device
		store.DB.Select("user_id, device_name, last_active_at").
			Where("user_id IN ?", peerIDs).
			Order("last_active_at DESC, created_at DESC").Find(&drows)
		for _, d := range drows {
			if _, dup := peerDeviceName[d.UserID]; !dup && d.DeviceName != "" {
				peerDeviceName[d.UserID] = d.DeviceName
			}
		}
	}

	// 8. Redis 两轮 pipeline：未读一次 HMGET；在线设备 SMEMBERS + 各设备 IP GET
	unreadMap := make(map[int64]int64, len(convIDs))
	onlineDevMap := make(map[int64][]string, len(peerIDs))
	ipMap := make(map[int64][]string, len(peerIDs))
	if len(convIDs) > 0 {
		pipe := store.RDB.Pipeline()
		unreadCmd := pipe.HMGet(ctx, fmt.Sprintf("unread:%d", userID), int64Strs(convIDs)...)
		onlineCmds := make(map[int64]*redis.StringSliceCmd, len(peerIDs))
		for _, pid := range peerIDs {
			onlineCmds[pid] = pipe.SMembers(ctx, "online:"+strconv.FormatInt(pid, 10))
		}
		_, _ = pipe.Exec(ctx)
		if vals, err := unreadCmd.Result(); err == nil {
			for i, cid := range convIDs {
				if i >= len(vals) || vals[i] == nil {
					continue
				}
				if s, ok := vals[i].(string); ok {
					if n, err := strconv.ParseInt(s, 10, 64); err == nil {
						unreadMap[cid] = n
					}
				}
			}
		}
		// 第二轮：按拿到的设备列表批量查 IP（替代 OnlineIPs 里逐设备 GET）
		pipe2 := store.RDB.Pipeline()
		type ipReq struct {
			peer int64
			idx  int
		}
		ipReqs := make([]ipReq, 0)
		ipCmds := make([]*redis.StringCmd, 0)
		for pid, cmd := range onlineCmds {
			devs, _ := cmd.Result()
			if len(devs) > 0 {
				onlineDevMap[pid] = devs
			}
			for _, d := range devs {
				ipReqs = append(ipReqs, ipReq{peer: pid})
				ipCmds = append(ipCmds, pipe2.Get(ctx, fmt.Sprintf("online:%d:ip:%s", pid, d)))
			}
		}
		if len(ipCmds) > 0 {
			_, _ = pipe2.Exec(ctx)
			for i, cmd := range ipCmds {
				if ip, err := cmd.Result(); err == nil && ip != "" {
					ipMap[ipReqs[i].peer] = append(ipMap[ipReqs[i].peer], ip)
				}
			}
		}
	}

	// 9. Mongo 一条聚合取所有会话的最后一条消息（替代逐会话 FindOne）
	lastMap := make(map[int64]*model.Message, len(convIDs))
	cur, err := msgColl().Aggregate(ctx, bson.A{
		bson.M{"$match": bson.M{"conversation_id": bson.M{"$in": convIDs}}},
		bson.M{"$sort": bson.M{"msg_id": -1}},
		bson.M{"$group": bson.M{"_id": "$conversation_id", "doc": bson.M{"$first": "$$ROOT"}}},
	})
	if err == nil {
		for cur.Next(ctx) {
			var row struct {
				ID  int64         `bson:"_id"`
				Doc model.Message `bson:"doc"`
			}
			if err := cur.Decode(&row); err == nil {
				m := row.Doc
				lastMap[row.ID] = &m
			}
		}
		_ = cur.Close(ctx)
	}

	// 10. 小助手配置最多取一次（有助手会话才用得到）
	var ac AssistantConfig
	needAssistant := false
	for _, m := range members {
		if otherMap[m.ConversationID] == -1 {
			needAssistant = true
			break
		}
	}
	if needAssistant {
		ac = GetAssistantConfig(ctx, nil)
	}

	// 11. 组装（纯内存）
	// 好友集合一次查出：单聊对端的在线状态可见性（online_visible=contacts）判定用
	friends := FriendIDSet(ctx, userID)
	items := make([]*ConvItem, 0, len(members))
	for _, m := range members {
		conv := convMap[m.ConversationID]
		if conv == nil || conv.Status != model.ConvNormal {
			continue
		}
		item := &ConvItem{
			Conversation: *conv,
			ShortID:      model.StrVal(conv.ShortID),
			Unread:       unreadMap[m.ConversationID],
			Mute:         m.Mute == 1,
			Pinned:       m.Pinned == 1,
			Archived:     m.Archived == 1,
			MemberCount:  cntMap[m.ConversationID],
		}
		if conv.Type == model.ConvDirect {
			otherID := otherMap[m.ConversationID]
			if otherID != 0 {
				item.PeerID = otherID
				if otherID == -1 {
					// 小助手虚拟账号（靓号 ID 固定 10000——需求12），昵称/头像取后台助手配置
					item.ConversationName = ac.Name
					item.PeerShortID = "10000"
					// 小助手永远在线（官方账号）
					item.PeerOnline = true
					item.PeerOnlineZh = "在线"
					if av := ac.Avatar; av != "" {
						item.Conversation.Avatar = av
					}
				} else if u := userMap[otherID]; u != nil {
					item.ConversationName = u.Nickname
					item.PeerShortID = model.StrVal(u.ShortID)
					item.PeerVipShortID = vipMap[otherID]
					// 对方账号角色（V 盾只对客服 role=3 显示）；批量 IN 查询已带全列，无 N+1
					item.PeerRole = u.Role
					// 单聊头像一律用对方**实时**头像（2026-09-15 二十三批：原来仅会话行
					// Avatar 为空才兜底——老会话行存着过期/失效的头像地址，用户换了头像
					// 或默认头像回填后消息列表仍显示旧图/空占位）
					if u.Avatar != "" {
						item.Conversation.Avatar = u.Avatar
					}
					// 资料页"最近上线"数据源：聊天窗口路径的会话来自本列表
					item.Conversation.LastLoginAt = u.LastLoginAt
					// 需求：设置了备注则优先显示备注名
					if rm := remarkMap[otherID]; rm != "" {
						item.PeerRemark = rm
						item.ConversationName = rm
					}
					// 在线状态可见性：对端 online_visible=nobody 或 contacts（且我非好友）时
					// 不下发在线信息（设备/IP 一并清空，避免旁路推算在场）
					devs := onlineDevMap[otherID]
					if !OnlineVisibleTo(userID, friends, u) {
						devs = nil
						item.PeerOnlineIP = nil
					}
					item.PeerOnline = len(devs) > 0
					item.PeerOnlineDev = devs
					item.PeerOnlineZh = OnlineDeviceZh(devs)
					item.PeerDeviceName = peerDeviceName[otherID]
					if devs != nil {
						item.PeerOnlineIP = ipMap[otherID]
					}
				}
			}
		} else {
			item.ConversationName = conv.NameZh
		}
		// 最后一条消息 + 单聊 delivery_state 标记
		if last := lastMap[m.ConversationID]; last != nil {
			// 「删除双方聊天记录」软删位点：最后一条消息已被删除覆盖时不下发
			//（否则列表预览还显示已删的旧消息）。单聊有位点语义；单聊成员行
			// cleared_msg_id 双方同步推进，用请求者自己的行即可。
			if conv.Type == model.ConvDirect && last.MsgID <= m.ClearedMsgID {
				last = nil
			}
			if last != nil {
				item.LastMessage = last
				// 需求4：单聊我发的消息，对方已读 → delivery_state=read。
				// 对端关闭「发送已读回执」时不做已读推导（与 History 的行为一致）
				if conv.Type == model.ConvDirect && last.SenderID == userID {
					if otherID := otherMap[m.ConversationID]; otherID > 0 &&
						UserFlagCached(ctx, otherID, "read_receipt_enabled") == 1 {
						if peerReadMap[m.ConversationID] >= last.MsgID {
							item.LastMessage.Delivery = "read"
						} else {
							item.LastMessage.Delivery = "sent"
						}
					}
				}
			}
		}
		items = append(items, item)
	}
	// 排序：置顶优先，再按最后消息时间倒序
	sort.Slice(items, func(i, j int) bool {
		if items[i].Pinned != items[j].Pinned {
			return items[i].Pinned
		}
		ti := lastTime(items[i])
		tj := lastTime(items[j])
		return ti.After(tj)
	})
	return items, nil
}

// int64Strs Redis HMGET 字段列表
func int64Strs(ids []int64) []string {
	out := make([]string, len(ids))
	for i, v := range ids {
		out[i] = strconv.FormatInt(v, 10)
	}
	return out
}

func lastTime(it *ConvItem) time.Time {
	if it.LastMessage != nil {
		return it.LastMessage.CreatedAt
	}
	return it.Conversation.CreatedAt
}

func unreadCount(ctx context.Context, uid, convID int64) int64 {
	n, _ := store.RDB.HGet(ctx, fmt.Sprintf("unread:%d", uid), fmt.Sprintf("%d", convID)).Int64()
	return n
}

func memberCount(ctx context.Context, convID int64) int64 {
	var cnt int64
	store.DB.Model(&model.ConversationMember{}).Where("conversation_id = ?", convID).Count(&cnt)
	return cnt
}

func directOtherID(ctx context.Context, convID, userID int64) int64 {
	var ids []int64
	store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id != ?", convID, userID).Pluck("user_id", &ids)
	if len(ids) > 0 {
		return ids[0]
	}
	return 0
}

// ============ 群管理 ============

// getConv 加载会话（不存在/已解散返回 ConvNotFound）
func getConv(ctx context.Context, convID int64) (*model.Conversation, error) {
	var conv model.Conversation
	if err := store.DB.First(&conv, convID).Error; err != nil {
		return nil, errs.ConvNotFound
	}
	if conv.Status == model.ConvDisband {
		return nil, errs.ConvNotFound
	}
	return &conv, nil
}

func GroupInvite(ctx context.Context, userID, convID int64, memberIDs []int64) error {
	// 先加载会话：满员校验（MaxMembers=0 表示不限）+ 权限判断都要用
	conv, err := getConv(ctx, convID)
	if err != nil {
		return err
	}
	role := memberRole(ctx, convID, userID)
	if role != model.MemberOwner && role != model.MemberAdmin {
		// 普通成员仅在群允许时才能邀请（群管理开关：允许成员邀请）
		if conv.AllowMemberInvite != 1 {
			return errs.Forbidden
		}
	}
	// 满员校验：统计本次真正会新增的人数（去重 + 剔除已在群的），超上限直接拒绝
	if conv.MaxMembers > 0 {
		candidates := make([]int64, 0, len(memberIDs))
		seen := make(map[int64]bool, len(memberIDs))
		for _, uid := range memberIDs {
			if uid > 0 && !seen[uid] {
				seen[uid] = true
				candidates = append(candidates, uid)
			}
		}
		if len(candidates) > 0 {
			var existCnt int64
			store.DB.Model(&model.ConversationMember{}).
				Where("conversation_id = ? AND user_id IN ?", convID, candidates).Count(&existCnt)
			current := memberCount(ctx, convID)
			if current+int64(len(candidates))-existCnt > int64(conv.MaxMembers) {
				return errs.GroupFull
			}
		}
	}
	added := make([]int64, 0, len(memberIDs))
	for _, uid := range memberIDs {
		var cnt int64
		store.DB.Model(&model.ConversationMember{}).
			Where("conversation_id = ? AND user_id = ?", convID, uid).Count(&cnt)
		if cnt == 0 {
			store.DB.Create(&model.ConversationMember{
				ConversationID: convID, UserID: uid, Role: model.MemberNormal, JoinedAt: time.Now(),
			})
			added = append(added, uid)
		}
	}
	// 群事件系统提示：邀请人进群（每新增一人一条，聊天流灰色提示）
	if actor := userName(userID); actor != "" {
		for _, uid := range added {
			b, _ := json.Marshal(map[string]interface{}{
				"kind": "invite", "actor": actor, "target": userName(uid),
			})
			sendGroupSystemMsg(ctx, convID, string(b))
		}
	}
	// 【修 R-17】成员变更立即失效成员缓存（旧代码全仓无失效点）：
	// 新成员最长 10s 收不到消息、被移出的人最长 10s 还能收，都在这里收敛。
	if len(added) > 0 {
		invalidateConvMembers(ctx, convID)
		PublishMemberChanged(ctx, convID)
		// 频道（type=3）的邀请关注同样算「新订阅」：小助手发欢迎语（失败不阻断；
		// 后台配置了自定义模板 default_channel_config.welcomeMsg 则全入口生效，未配置用默认文案）
		if conv.Type == model.ConvChannel {
			dc := DefaultChannelConfigGet(ctx)
			for _, uid := range added {
				afterChannelFollow(ctx, convID, uid, dc.WelcomeMsg)
			}
		}
	}
	return nil
}

func GroupRemove(ctx context.Context, userID, convID, targetID int64) error {
	if userID == targetID {
		// 移除自己请走退出群（quit），这里直接拒绝避免误触
		return errs.Forbidden
	}
	role := memberRole(ctx, convID, userID)
	if role != model.MemberOwner && role != model.MemberAdmin {
		return errs.Forbidden
	}
	// 层级约束：不能移除群主；管理员只能移除普通成员
	targetRole := memberRole(ctx, convID, targetID)
	if targetRole == model.MemberOwner {
		return errs.Forbidden
	}
	if role == model.MemberAdmin && targetRole == model.MemberAdmin {
		return errs.Forbidden
	}
	targetName := userName(targetID)
	if err := store.DB.Where("conversation_id = ? AND user_id = ?", convID, targetID).
		Delete(&model.ConversationMember{}).Error; err != nil {
		return err
	}
	// 群事件系统提示：成员被移出群聊
	if actor := userName(userID); actor != "" && targetName != "" {
		b, _ := json.Marshal(map[string]interface{}{
			"kind": "kick", "actor": actor, "target": targetName,
		})
		sendGroupSystemMsg(ctx, convID, string(b))
	}
	// 修 R-17：被移出的人必须立刻收不到（旧实现最长 10s 仍能收）
	invalidateConvMembers(ctx, convID)
	PublishMemberChanged(ctx, convID)
	return nil
}

func GroupQuit(ctx context.Context, userID, convID int64) error {
	var cnt int64
	store.DB.Model(&model.ConversationMember{}).Where("conversation_id = ?", convID).Count(&cnt)
	if cnt <= 1 {
		// 最后一人退出 → 解散（不再发退出提示）
		store.DB.Model(&model.Conversation{}).Where("id = ?", convID).Update("status", model.ConvDisband)
	}
	name := userName(userID)
	if err := store.DB.Where("conversation_id = ? AND user_id = ?", convID, userID).
		Delete(&model.ConversationMember{}).Error; err != nil {
		return err
	}
	// 群事件系统提示：成员退出群聊
	if name != "" && cnt > 1 {
		b, _ := json.Marshal(map[string]interface{}{"kind": "quit", "target": name})
		sendGroupSystemMsg(ctx, convID, string(b))
	}
	// 修 R-17：退出者必须立刻收不到
	invalidateConvMembers(ctx, convID)
	PublishMemberChanged(ctx, convID)
	return nil
}

func GroupDisband(ctx context.Context, userID, convID int64) error {
	role := memberRole(ctx, convID, userID)
	if role != model.MemberOwner {
		return errs.Forbidden
	}
	store.DB.Model(&model.Conversation{}).Where("id = ?", convID).Update("status", model.ConvDisband)
	if err := store.DB.Where("conversation_id = ?", convID).Delete(&model.ConversationMember{}).Error; err != nil {
		return err
	}
	// 修 R-17：群已解散，成员集合立即清空
	invalidateConvMembers(ctx, convID)
	invalidateConvInfo(convID)
	PublishMemberChanged(ctx, convID)
	return nil
}

func GroupUpdate(ctx context.Context, userID, convID int64, nameZh, nameEn, announcementZh, announcementEn, avatar string) error {
	role := memberRole(ctx, convID, userID)
	if role != model.MemberOwner && role != model.MemberAdmin {
		return errs.Forbidden
	}
	updates := map[string]interface{}{}
	if nameZh != "" {
		updates["name_zh"] = nameZh
	}
	if nameEn != "" {
		updates["name_en"] = nameEn
	}
	if announcementZh != "" {
		updates["announcement_zh"] = announcementZh
	}
	if announcementEn != "" {
		updates["announcement_en"] = announcementEn
	}
	if avatar != "" {
		updates["avatar"] = avatar
	}
	if len(updates) == 0 {
		return nil
	}
	return store.DB.Model(&model.Conversation{}).Where("id = ?", convID).Updates(updates).Error
}

// ============ 群聊管理（群主/管理员） ============

// GroupSettings 群管理设置（群管理页读写）
type GroupSettings struct {
	MuteAll           bool `json:"muteAll"`           // 全员禁言：仅群主/管理员可发言
	PrivacyEnabled    bool `json:"privacyEnabled"`    // 成员隐私：普通成员不可查看成员列表
	AllowMemberInvite bool `json:"allowMemberInvite"` // 允许群成员邀请成员
	QrJoinEnabled     bool `json:"qrJoinEnabled"`     // 二维码进群
	// ShowMembers 显示群成员人数/在线人数（2026-09-22 需求5）：关闭后群聊标题
	// 下方不显示「N 成员, M 在线」、聊天信息页不显示群成员卡片（仅群主可切换）
	ShowMembers bool `json:"showMembers"`
}

// GetGroupSettings 读取群管理设置（全体成员可读：成员页需要按"允许邀请"决定是否显示邀请入口）
func GetGroupSettings(ctx context.Context, userID, convID int64) (*GroupSettings, error) {
	if !isMember(ctx, convID, userID) {
		return nil, errs.ConvNotFound
	}
	conv, err := getConv(ctx, convID)
	if err != nil {
		return nil, err
	}
	return &GroupSettings{
		MuteAll:           conv.MuteAll == 1,
		PrivacyEnabled:    conv.PrivacyEnabled == 1,
		AllowMemberInvite: conv.AllowMemberInvite == 1,
		QrJoinEnabled:     conv.QrJoinEnabled == 1,
		ShowMembers:       conv.ShowMembers == 1,
	}, nil
}

// SetGroupSettings 更新群管理设置（仅群主）
func SetGroupSettings(ctx context.Context, userID, convID int64, s *GroupSettings) error {
	if memberRole(ctx, convID, userID) != model.MemberOwner {
		return errs.Forbidden
	}
	old, err := getConv(ctx, convID)
	if err != nil {
		return err
	}
	b2i := map[bool]int{true: 1, false: 0}
	if err := store.DB.Model(&model.Conversation{}).Where("id = ?", convID).Updates(map[string]interface{}{
		"mute_all":            b2i[s.MuteAll],
		"privacy_enabled":     b2i[s.PrivacyEnabled],
		"allow_member_invite": b2i[s.AllowMemberInvite],
		"qr_join_enabled":     b2i[s.QrJoinEnabled],
		"show_members":        b2i[s.ShowMembers],
	}).Error; err != nil {
		return err
	}
	// 群事件系统提示：全员禁言开启/解除（仅状态变化时发）
	if (old.MuteAll == 1) != s.MuteAll {
		kind := "muteAllOff"
		if s.MuteAll {
			kind = "muteAllOn"
		}
		payload := map[string]interface{}{"kind": kind, "actor": userName(userID)}
		b, _ := json.Marshal(payload)
		sendGroupSystemMsg(ctx, convID, string(b))
	}
	return nil
}

// SetGroupAdmin 设置/取消管理员（仅群主；目标必须是普通成员/管理员，不能操作群主）
func SetGroupAdmin(ctx context.Context, userID, convID, targetID int64, admin bool) error {
	if memberRole(ctx, convID, userID) != model.MemberOwner {
		return errs.Forbidden
	}
	if targetID == userID {
		return errs.Forbidden
	}
	targetRole := memberRole(ctx, convID, targetID)
	if targetRole == model.MemberOwner {
		return errs.Forbidden
	}
	want := model.MemberAdmin
	if !admin {
		want = model.MemberNormal
	}
	// 目标不是成员或已是期望角色则拒绝
	if targetRole != model.MemberAdmin && targetRole != model.MemberNormal {
		return errs.ConvNotFound
	}
	if targetRole == want {
		return nil
	}
	return store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, targetID).
		Update("role", want).Error
}

// MuteMember 禁言/解除禁言成员（群主/管理员；不能禁言群主；管理员不能禁言管理员）
func MuteMember(ctx context.Context, userID, convID, targetID int64, mute bool, minutes int) error {
	if userID == targetID {
		return errs.Forbidden
	}
	role := memberRole(ctx, convID, userID)
	if role != model.MemberOwner && role != model.MemberAdmin {
		return errs.Forbidden
	}
	targetRole := memberRole(ctx, convID, targetID)
	if targetRole == model.MemberOwner {
		return errs.Forbidden
	}
	if role == model.MemberAdmin && targetRole == model.MemberAdmin {
		return errs.Forbidden
	}
	until := int64(0)
	if mute {
		if minutes <= 0 {
			minutes = 10
		}
		if minutes > 43200 { // 上限 30 天
			minutes = 43200
		}
		until = time.Now().Add(time.Duration(minutes) * time.Minute).Unix()
	}
	if err := store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, targetID).
		Update("speak_muted_until", until).Error; err != nil {
		return err
	}
	// 群事件系统提示：禁言/解除禁言
	if actor := userName(userID); actor != "" {
		kind := "unmute"
		payload := map[string]interface{}{
			"kind": kind, "actor": actor, "target": userName(targetID),
		}
		if mute {
			payload["kind"] = "mute"
			payload["minutes"] = minutes
		}
		b, _ := json.Marshal(payload)
		sendGroupSystemMsg(ctx, convID, string(b))
	}
	return nil
}

// GroupJoin 扫群二维码进群（需群开启"二维码进群"；群未解散；未满员）
func GroupJoin(ctx context.Context, userID, convID int64) (*model.Conversation, error) {
	conv, err := getConv(ctx, convID)
	if err != nil {
		return nil, err
	}
	if conv.Type != model.ConvGroup {
		return nil, errs.ConvNotFound
	}
	if conv.QrJoinEnabled != 1 {
		return nil, errs.Forbidden
	}
	var cnt int64
	store.DB.Model(&model.ConversationMember{}).Where("conversation_id = ?", convID).Count(&cnt)
	// MaxMembers=0 表示不限（老数据默认 500 保留原语义）
	if conv.MaxMembers > 0 && cnt >= int64(conv.MaxMembers) {
		return nil, errs.GroupFull
	}
	var exist int64
	store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, userID).Count(&exist)
	if exist == 0 {
		store.DB.Create(&model.ConversationMember{
			ConversationID: convID, UserID: userID, Role: model.MemberNormal, JoinedAt: time.Now(),
		})
		// 群事件系统提示：扫码新成员加入
		if name := userName(userID); name != "" {
			b, _ := json.Marshal(map[string]interface{}{"kind": "join", "target": name})
			sendGroupSystemMsg(ctx, convID, string(b))
		}
	}
	return conv, nil
}

// GroupPreview 扫码进群前的群信息预览（二次确认页用；已登录即可，不要求是成员）
func GroupPreview(ctx context.Context, convID int64) (*model.Conversation, int64, error) {
	conv, err := getConv(ctx, convID)
	if err != nil || conv.Type != model.ConvGroup {
		return nil, 0, errs.ConvNotFound
	}
	var cnt int64
	store.DB.Model(&model.ConversationMember{}).Where("conversation_id = ?", convID).Count(&cnt)
	return conv, cnt, nil
}

// SetPin / SetMute 置顶 / 免打扰
func SetPin(ctx context.Context, userID, convID int64, pinned bool) error {
	return store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, userID).
		Update("pinned", map[bool]int{true: 1, false: 0}[pinned]).Error
}

func SetMute(ctx context.Context, userID, convID int64, mute bool) error {
	return store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, userID).
		Update("mute", map[bool]int{true: 1, false: 0}[mute]).Error
}

// SetArchive 归档 / 取消归档（个人开关，同 SetPin/SetMute 手法，PUT /conversation/:id/archive）
func SetArchive(ctx context.Context, userID, convID int64, archived bool) error {
	return store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, userID).
		Update("archived", map[bool]int{true: 1, false: 0}[archived]).Error
}

// DeleteConv 删除会话（从我的列表移除）
//
// 语义：只把 conversation_member.hidden 置 1，**不动成员身份**。
// 群聊下用户仍是群成员，下次收到该群新消息时应自动重新出现（hidden 归 0）。
func DeleteConv(ctx context.Context, userID, convID int64) error {
	res := store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, userID).
		Update("hidden", 1)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ConvNotFound
	}
	return nil
}

// UnhideConv 会话重新出现：收到新消息时把 hidden 归 0，
// 避免「删掉的会话因为还有后续消息却永不显示」。
func UnhideConv(ctx context.Context, userID, convID int64) {
	store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ? AND hidden = 1", convID, userID).
		Update("hidden", 0)
}

// ConvMembers 会话成员（含用户信息，群设置展示）
// 返回值：成员列表 + 群成员总数。
// 成员隐私开启时，普通成员只下发前 15 个成员（资料页 2 排预览），总数照实返回，
// 且忽略 page/size（分页不能绕过隐私截断）。
// 可选分页：page/size 均 >0 时按 LIMIT/OFFSET 分页下发（大频道/大群成员页用，
// total 恒为全量计数）；不传或非法时维持旧行为（全量拉取），向后兼容 app 端。
func ConvMembers(ctx context.Context, userID, convID int64, page, size int) ([]ConvMemberInfo, int64, error) {
	if !isMember(ctx, convID, userID) {
		return nil, 0, errs.ConvNotFound
	}
	conv, err := getConv(ctx, convID)
	if err != nil {
		return nil, 0, err
	}
	privacyLimited := conv.PrivacyEnabled == 1 && memberRole(ctx, convID, userID) == model.MemberNormal
	var members []model.ConversationMember
	q := store.DB.Where("conversation_id = ?", convID)
	// 群主(1)→管理员(2)→普通成员(3)，同角色按进群时间先后（群主永远排第一）
	q = q.Order("role ASC, joined_at ASC, id ASC")
	paginated := page > 0 && size > 0 && !privacyLimited
	if privacyLimited {
		q = q.Limit(15)
	} else if paginated {
		q = q.Offset((page - 1) * size).Limit(size)
	}
	if err := q.Find(&members).Error; err != nil {
		return nil, 0, err
	}
	var total int64
	if privacyLimited || paginated {
		store.DB.Model(&model.ConversationMember{}).Where("conversation_id = ?", convID).Count(&total)
	} else {
		total = int64(len(members))
	}
	ids := make([]int64, 0, len(members))
	roleMap := make(map[int64]int, len(members))
	muteMap := make(map[int64]int64, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
		roleMap[m.UserID] = m.Role
		muteMap[m.UserID] = m.SpeakMutedUntil
	}
	if len(ids) == 0 {
		return []ConvMemberInfo{}, total, nil
	}
	var users []model.User
	if err := store.DB.Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	userMap := make(map[int64]model.User, len(users))
	for _, u := range users {
		userMap[u.ID] = u
	}
	// 按当前用户视角补好友备注（群聊 @ 昵称等场景可用备注名）
	remarks := friendRemarkMap(ctx, userID, ids)
	// 查看者好友集合一次查出：成员手机号可见性（phone_visible=contacts）判定用
	friends := FriendIDSet(ctx, userID)
	// 输出顺序跟随 members 排序：群主→管理员→普通成员，同角色按进群时间
	out := make([]ConvMemberInfo, 0, len(members))
	for _, m := range members {
		u, ok := userMap[m.UserID]
		if !ok {
			continue
		}
		out = append(out, ConvMemberInfo{
			PublicUser:      *PublicUserOfForSet(userID, friends, &u),
			Role:            roleMap[u.ID],
			AccountRole:     u.Role,
			Remark:          remarks[u.ID],
			VipShortID:      IsVipShortID(ctx, u.ID, u.ShortID),
			SpeakMutedUntil: muteMap[u.ID],
		})
	}
	return out, total, nil
}

// SetPinMessage 置顶/取消置顶消息（所有成员可置顶；支持多条 pinnedMsgIDs 列表）
// pinned=true 追加；pinned=false 或 msgID=0 时移除
func SetPinMessage(ctx context.Context, userID, convID, msgID int64, content string, pinned bool) error {
	if !isMember(ctx, convID, userID) {
		return errs.ConvNotFound
	}
	var conv model.Conversation
	if err := store.DB.First(&conv, convID).Error; err != nil {
		return errs.ConvNotFound
	}
	ids := []string{}
	if conv.PinnedMsgIDs != "" {
		_ = json.Unmarshal([]byte(conv.PinnedMsgIDs), &ids)
	}
	key := strconv.FormatInt(msgID, 10)
	if msgID > 0 && pinned {
		// 追加（去重）
		found := false
		for _, v := range ids {
			if v == key {
				found = true
				break
			}
		}
		if !found {
			ids = append(ids, key)
		}
	} else {
		// 移除
		out := ids[:0]
		for _, v := range ids {
			if v != key {
				out = append(out, v)
			}
		}
		ids = out
	}
	idsJSON, _ := json.Marshal(ids)
	updates := map[string]interface{}{
		"pinned_msg_ids": string(idsJSON),
	}
	// 兼容旧单条字段：对齐列表最后一条（追加=新置顶的；移除=剩余最后一条，
	// 内容快照从消息库回填——否则旧客户端置顶条会显示刚被取消的消息）
	if len(ids) > 0 {
		lastID := int64(0)
		lastContent := content
		if n, err := strconv.ParseInt(ids[len(ids)-1], 10, 64); err == nil {
			lastID = n
			if lastID != msgID {
				var m model.Message
				if err := msgColl().FindOne(ctx, bson.M{"msg_id": lastID}).Decode(&m); err == nil {
					lastContent = m.Content
				}
			}
		}
		updates["pinned_msg_id"] = lastID
		updates["pinned_msg_content"] = lastContent
	} else {
		updates["pinned_msg_id"] = 0
		updates["pinned_msg_content"] = ""
	}
	return store.DB.Model(&model.Conversation{}).Where("id = ?", convID).Updates(updates).Error
}

// PinnedMsgBrief 置顶消息简项（按置顶顺序，前端渲染卡片 + 点击跳转）
type PinnedMsgBrief struct {
	// 注意：MsgID 本身已是 string，不能再挂 ,string——该选项会把值再包一层
	// 引号下发（实测 "msgId":"\"12345...\""），客户端拿到带引号的 id 与消息
	// 列表永远匹配不上，导致点置顶恒提示「该消息已不在聊天记录中」。
	MsgID      string `json:"msgId"`
	Content    string `json:"content"`
	SenderName string `json:"senderName"`
	Type       int    `json:"type"`
	CreatedAt  string `json:"createdAt"`
}

// PinnedMessages 置顶消息列表（按置顶顺序返回完整简项，需求：支持多条 + 切换）
func PinnedMessages(ctx context.Context, userID, convID int64) ([]PinnedMsgBrief, error) {
	if !isMember(ctx, convID, userID) {
		return nil, errs.ConvNotFound
	}
	var conv model.Conversation
	if err := store.DB.First(&conv, convID).Error; err != nil {
		return nil, errs.ConvNotFound
	}
	ids := []string{}
	if conv.PinnedMsgIDs != "" {
		_ = json.Unmarshal([]byte(conv.PinnedMsgIDs), &ids)
	}
	if len(ids) == 0 && conv.PinnedMsgID > 0 {
		// 兼容旧单条字段
		ids = []string{strconv.FormatInt(conv.PinnedMsgID, 10)}
	}
	if len(ids) == 0 {
		return []PinnedMsgBrief{}, nil
	}
	intIDs := make([]int64, 0, len(ids))
	for _, v := range ids {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			intIDs = append(intIDs, n)
		}
	}
	if len(intIDs) == 0 {
		return []PinnedMsgBrief{}, nil
	}
	// 消息存 MongoDB，发送者存 MySQL——两个数据源分别查
	cur, err := msgColl().Find(ctx, bson.M{"msg_id": bson.M{"$in": intIDs}})
	if err != nil {
		return nil, err
	}
	var msgs []model.Message
	if err := cur.All(ctx, &msgs); err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return []PinnedMsgBrief{}, nil
	}
	msgMap := make(map[int64]model.Message, len(msgs))
	for _, m := range msgs {
		msgMap[m.MsgID] = m
	}
	// 发送者昵称（MySQL user 表）
	senderIDs := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		senderIDs = append(senderIDs, m.SenderID)
	}
	senderMap := make(map[int64]string)
	if len(senderIDs) > 0 {
		senders := make([]model.User, 0)
		sIn := buildInInt64(senderIDs)
		store.DB.Raw(
			"SELECT * FROM user WHERE id IN (" + sIn + ")",
		).Scan(&senders)
		for _, u := range senders {
			senderMap[u.ID] = u.Nickname
		}
	}
	// 按置顶顺序组装
	out := make([]PinnedMsgBrief, 0, len(intIDs))
	for _, id := range intIDs {
		m, ok := msgMap[id]
		if !ok {
			continue
		}
		out = append(out, PinnedMsgBrief{
			MsgID:      strconv.FormatInt(m.MsgID, 10),
			Content:    m.Content,
			SenderName: senderMap[m.SenderID],
			Type:       m.Type,
			CreatedAt:  m.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return out, nil
}

// UpdateAnnouncement 更新群公告（群主/管理员）
func UpdateAnnouncement(ctx context.Context, userID, convID int64, zh, en string) error {
	role := memberRole(ctx, convID, userID)
	if role != model.MemberOwner && role != model.MemberAdmin {
		return errs.Forbidden
	}
	updates := map[string]interface{}{}
	if zh != "" {
		updates["announcement_zh"] = zh
	}
	if en != "" {
		updates["announcement_en"] = en
	}
	if len(updates) == 0 {
		return nil
	}
	return store.DB.Model(&model.Conversation{}).Where("id = ?", convID).Updates(updates).Error
}

// buildInInt64 把 int64 列表拼成 SQL IN 子句（绕开 GORM 1.25+ IN ? 大整数 bug）
func buildInInt64(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, v := range ids {
		parts = append(parts, strconv.FormatInt(v, 10))
	}
	return strings.Join(parts, ",")
}
