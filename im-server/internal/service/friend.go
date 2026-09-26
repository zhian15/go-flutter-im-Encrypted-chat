package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"
)

// FriendInfo 好友信息（User + 我设置的备注）
type FriendInfo struct {
	model.User
	Remark string `json:"remark"` // 我对该好友的备注（未设置则空）
}

// FriendList 我的好友列表
func FriendList(ctx context.Context, userID int64) ([]FriendInfo, error) {
	var ids []int64
	store.DB.Model(&model.FriendRelation{}).
		Where("user_id = ?", userID).Pluck("friend_id", &ids)

	// 兼容单向往反（对方添加我但未回填）——建好友时双向写入，此处再补查反向
	var reverse []int64
	store.DB.Model(&model.FriendRelation{}).
		Where("friend_id = ?", userID).Pluck("user_id", &reverse)
	ids = append(ids, reverse...)

	if len(ids) == 0 {
		return []FriendInfo{}, nil
	}
	var users []model.User
	store.DB.Where("id IN ? AND status = ?", ids, model.StatusNormal).
		Order("id asc").Find(&users)

	// 备注（仅查我主动设置的）
	var rels []model.FriendRelation
	store.DB.Where("user_id = ? AND friend_id IN ?", userID, ids).Find(&rels)
	remarkMap := make(map[int64]string, len(rels))
	for _, r := range rels {
		remarkMap[r.FriendID] = r.Remark
	}
	out := make([]FriendInfo, 0, len(users))
	for _, u := range users {
		out = append(out, FriendInfo{User: u, Remark: remarkMap[u.ID]})
	}
	return out, nil
}

// FriendRequestAdd 发起好友申请（全员可见，直接搜索添加）
// source：申请来源 1 搜索添加 2 扫码添加 3 名片；nil → 1，1/2/3 之外的值净化归 1
func FriendRequestAdd(ctx context.Context, fromID, toID int64, message string, source *int) error {
	if fromID == toID {
		return &errs.Err{Code: 1001, Msg: "不能添加自己"}
	}
	// 来源净化：合法枚举 1/2/3，非法/缺省一律归 1（搜索添加）
	src := 1
	if source != nil && *source >= 2 && *source <= 3 {
		src = *source
	}
	var target model.User
	if err := store.DB.First(&target, toID).Error; err != nil || target.Status != model.StatusNormal {
		return &errs.Err{Code: 3001, Msg: "用户不存在"}
	}
	// 后台管理员账号不参与好友体系（搜索已过滤，这里再兜底拦截直连 ID 申请）
	if target.Role == model.RoleAdmin {
		return &errs.Err{Code: 3004, Msg: "该账号不可添加"}
	}
	// 已是好友
	var cnt int64
	store.DB.Model(&model.FriendRelation{}).
		Where("(user_id = ? AND friend_id = ?) OR (user_id = ? AND friend_id = ?)",
			fromID, toID, toID, fromID).Count(&cnt)
	if cnt > 0 {
		return &errs.Err{Code: 3002, Msg: "你们已经是好友"}
	}
	// 重复申请拦截
	var pending int64
	store.DB.Model(&model.FriendRequest{}).
		Where("from_user = ? AND to_user = ? AND status = ?", fromID, toID, model.FriendReqPending).
		Count(&pending)
	if pending > 0 {
		return &errs.Err{Code: 3003, Msg: "申请已发送，请等待对方处理"}
	}
	if message == "" {
		message = "我是 " + selfNickname(fromID)
	}
	if err := store.DB.Create(&model.FriendRequest{
		FromUser: fromID, ToUser: toID, Message: message, Status: model.FriendReqPending, Source: src,
	}).Error; err != nil {
		return err
	}
	// 通知对方：有新的好友申请（前端刷新申请列表 + 红点；ID 字符串防精度丢失）
	data, _ := json.Marshal(map[string]interface{}{
		"fromUserId":   fmt.Sprintf("%d", fromID),
		"fromUserName": selfNickname(fromID),
		"message":      message,
	})
	_ = PublishEvent(ctx, &Event{Type: "friend.request", ToUIDs: []int64{toID}, Data: data})
	return nil
}

// FriendRequestIncoming 我收到的申请
func FriendRequestIncoming(ctx context.Context, userID int64) ([]model.FriendRequest, error) {
	var reqs []model.FriendRequest
	err := store.DB.Where("to_user = ? AND status = ?", userID, model.FriendReqPending).
		Order("id desc").Limit(50).Find(&reqs).Error
	return reqs, err
}

// FriendRequestOutgoing 我发出的申请
func FriendRequestOutgoing(ctx context.Context, userID int64) ([]model.FriendRequest, error) {
	var reqs []model.FriendRequest
	err := store.DB.Where("from_user = ? AND status = ?", userID, model.FriendReqPending).
		Order("id desc").Limit(50).Find(&reqs).Error
	return reqs, err
}

// FriendRequestHandle 处理申请（同意：双向写入好友关系）
func FriendRequestHandle(ctx context.Context, userID int64, reqID int64, agree bool) error {
	var req model.FriendRequest
	if err := store.DB.First(&req, reqID).Error; err != nil || req.ToUser != userID {
		return errs.FriendReqNotFound
	}
	now := time.Now()
	if agree {
		// 双向好友（来源取申请记录的 source；已存在的关系用 FirstOrCreate 不覆盖其原 source）
		store.DB.FirstOrCreate(&model.FriendRelation{UserID: req.FromUser, FriendID: req.ToUser, Source: req.Source, CreatedAt: now},
			model.FriendRelation{UserID: req.FromUser, FriendID: req.ToUser})
		store.DB.FirstOrCreate(&model.FriendRelation{UserID: req.ToUser, FriendID: req.FromUser, Source: req.Source, CreatedAt: now},
			model.FriendRelation{UserID: req.ToUser, FriendID: req.FromUser})
		// 需求：通过后自动建会话 + 发一条欢迎消息给对方（"我已通过你的好友申请，请和我开始聊天吧！"）
		if conv, err := CreateDirect(ctx, req.ToUser, req.FromUser); err == nil && conv != nil {
			// 以通过者（req.ToUser）身份发送欢迎消息
			SendMessage(ctx, req.ToUser, &SendMsgReq{
				ConversationID: conv.ID,
				Type:           1,
				Content:        "我已通过你的好友申请，请和我开始聊天吧！",
			})
		}
		// 通知对方：好友申请已通过
		evData, _ := json.Marshal(map[string]interface{}{
			"fromUserId": fmt.Sprintf("%d", req.ToUser),
			"message":    "我已通过你的好友申请",
		})
		_ = PublishEvent(ctx, &Event{Type: "friend.accepted", ToUIDs: []int64{req.FromUser}, Data: evData})
	}
	return store.DB.Model(&req).Update("status",
		map[bool]int{true: model.FriendReqAgreed, false: model.FriendReqRejected}[agree]).Error
}

// FriendDelete 删除好友（双向删除）
func FriendDelete(ctx context.Context, userID, friendID int64) error {
	res := store.DB.Where("user_id = ? AND friend_id = ?", userID, friendID).
		Delete(&model.FriendRelation{})
	store.DB.Where("user_id = ? AND friend_id = ?", friendID, userID).
		Delete(&model.FriendRelation{})
	if res.RowsAffected == 0 {
		return &errs.Err{Code: 3004, Msg: "不是好友关系"}
	}
	return nil
}

// FriendSetRemark 设置备注
func FriendSetRemark(ctx context.Context, userID, friendID int64, remark string) error {
	return store.DB.Model(&model.FriendRelation{}).
		Where("user_id = ? AND friend_id = ?", userID, friendID).
		Update("remark", remark).Error
}

// BlacklistAdd 拉黑（并自动解除好友）
func BlacklistAdd(ctx context.Context, userID, blockID int64) error {
	store.DB.FirstOrCreate(&model.Blacklist{UserID: userID, BlockUserID: blockID},
		model.Blacklist{UserID: userID, BlockUserID: blockID})
	FriendDelete(ctx, userID, blockID)
	return nil
}

// BlacklistRemove 移出黑名单
func BlacklistRemove(ctx context.Context, userID, blockID int64) error {
	return store.DB.Where("user_id = ? AND block_user_id = ?", userID, blockID).
		Delete(&model.Blacklist{}).Error
}

// BlacklistList 我的黑名单（返回脱敏 PublicUser，不泄露他人余额/手机号等隐私字段）
func BlacklistList(ctx context.Context, userID int64) ([]PublicUser, error) {
	var ids []int64
	store.DB.Model(&model.Blacklist{}).Where("user_id = ?", userID).Pluck("block_user_id", &ids)
	if len(ids) == 0 {
		return []PublicUser{}, nil
	}
	var users []model.User
	store.DB.Where("id IN ?", ids).Find(&users)
	out := make([]PublicUser, 0, len(users))
	for i := range users {
		out = append(out, *PublicUserOf(&users[i]))
	}
	return out, nil
}

// CommonGroupCounts 与各好友的「共同群组」数（第十批，好友资料页展示）。
// 统计互为成员的群（type=2 群聊、status=1 正常；频道 type=3 不算）；查看者与
// 条目必然互为好友（/friend/list 上下文），无额外隐私门控。
// 一次 JOIN 聚合避免 N+1；无共同群的好友不出现在返回 map 中（调用方按 0 取）。
func CommonGroupCounts(ctx context.Context, uid int64, friendIDs []int64) map[int64]int64 {
	out := make(map[int64]int64, len(friendIDs))
	if uid <= 0 || len(friendIDs) == 0 {
		return out
	}
	type row struct {
		UserID int64
		Cnt    int64
	}
	var rows []row
	if err := store.DB.WithContext(ctx).Raw(`
		SELECT cm.user_id AS user_id, COUNT(*) AS cnt
		FROM conversation_member cm
		JOIN conversation c ON c.id = cm.conversation_id AND c.type = 2 AND c.status = 1
		WHERE cm.user_id IN (?) AND cm.conversation_id IN (
			SELECT conversation_id FROM conversation_member WHERE user_id = ?
		)
		GROUP BY cm.user_id`, friendIDs, uid).Scan(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out[r.UserID] = r.Cnt
	}
	return out
}

// ============ 共同群组列表（好友资料页「共同群组」） ============

// MutualGroup 共同群组轻量卡
type MutualGroup struct {
	ID          int64  `json:"id,string"` // 群会话 ID（雪花字符串）
	Name        string `json:"name"`      // 群名（nameZh 优先，空则 nameEn）
	NameEn      string `json:"nameEn"`
	Avatar      string `json:"avatar"`
	MemberCount int64  `json:"memberCount"`
}

// MutualGroups 「我」与目标用户共同加入的群列表（type=2 且未解散）。
// JOIN 思路与 CommonGroupCounts 一致，返回具体群信息；按成员数降序。
// 权限：目标用户存在且状态正常即可（好友与否不限制）。
func MutualGroups(ctx context.Context, uid, otherID int64) ([]MutualGroup, error) {
	if uid <= 0 || otherID <= 0 {
		return []MutualGroup{}, nil
	}
	var target model.User
	if err := store.DB.Select("id", "status").First(&target, otherID).Error; err != nil {
		return nil, errs.NotFound
	}
	if target.Status != model.StatusNormal {
		return nil, errs.NotFound
	}
	type convRow struct {
		ID          int64
		NameZh      string
		NameEn      string
		Avatar      string
		MemberCount int64
	}
	var rows []convRow
	if err := store.DB.WithContext(ctx).Raw(`
		SELECT c.id, c.name_zh AS name_zh, c.name_en AS name_en, c.avatar AS avatar,
		       (SELECT COUNT(*) FROM conversation_member cm2 WHERE cm2.conversation_id = c.id) AS member_count
		FROM conversation c
		WHERE c.type = 2 AND c.status = 1
		  AND c.id IN (SELECT conversation_id FROM conversation_member WHERE user_id = ?)
		  AND c.id IN (SELECT conversation_id FROM conversation_member WHERE user_id = ?)
		ORDER BY member_count DESC, c.id DESC`, uid, otherID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]MutualGroup, 0, len(rows))
	for _, r := range rows {
		name := r.NameZh
		if name == "" {
			name = r.NameEn
		}
		out = append(out, MutualGroup{
			ID:          r.ID,
			Name:        name,
			NameEn:      r.NameEn,
			Avatar:      r.Avatar,
			MemberCount: r.MemberCount,
		})
	}
	return out, nil
}

func selfNickname(userID int64) string {
	var u model.User
	if err := store.DB.First(&u, userID).Error; err == nil {
		return u.Nickname
	}
	return "用户"
}
