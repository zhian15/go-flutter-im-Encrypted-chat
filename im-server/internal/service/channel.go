package service

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/pkg/id"
	"github.com/yourcompany/im-server/internal/store"
)

// ============ 频道（Channel，type=3 会话） ============
//
// 频道 = 复用 conversation + conversation_member 的「只有频道主能发言的群」：
//   - 关注 = conversation_member 插一行（role=MemberNormal）→ 自动进会话列表
//     （ConvList 由 conversation_member 驱动，见 conversation.go ConvList）；
//   - 取消关注 = 物理删除该行（同 GroupQuit 先例）——**不能**用 hidden=1：
//     hidden 只是不在列表显示，成员行还在 → 消息扇出照发、成员数照算，
//     且 UnhideConv 收到新消息会把 hidden 归 0 让频道重新出现（语义=「删会话」而非「取关」）；
//   - 发言权限走 message.go 发送链路：type=3 时仅 MemberOwner 可发言（错误码 4007）；
//   - 公开/私密用现有 QrJoinEnabled 表达：1=公开（可按 ID 自助关注）、0=私密（只能被邀请）；
//   - 私密频道允许频道主经现有 POST /conversation/:id/invite 邀请关注
//     （GroupInvite 对 Owner/Admin 不检查 AllowMemberInvite，conversation.go:497-503）。

// CreateChannelReq 创建频道请求
type CreateChannelReq struct {
	NameZh         string  `json:"nameZh"`
	NameEn         string  `json:"nameEn"`
	Avatar         string  `json:"avatar"`
	IsPublic       *bool   `json:"isPublic"` // 缺省 true；false=私密（不可自助关注，只能被邀请）
	AnnouncementZh string  `json:"announcementZh"`
	AnnouncementEn string  `json:"announcementEn"`
	ShortID        *string `json:"shortId"` // 可选自定义唯一 ID（类公众号微信号）；nil/空串=不设置
}

// validShortID 频道自定义 ID 格式：3-20 位，仅字母/数字/下划线。
// 允许纯数字短号：雪花 ID 18-19 位且 GET /channel/:id 先按雪花解析，短号不构成歧义。
func validShortID(s string) bool {
	if len(s) < 3 || len(s) > 20 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// CreateChannel 创建频道：conversation(type=3) + 频道主成员行。
// 频道 ID 即会话 ID（同一雪花 ID，客户端聊天/拉历史直接用它）。
func CreateChannel(ctx context.Context, ownerID int64, req *CreateChannelReq) (*model.Conversation, error) {
	// 功能开关总闸（2026-09-22 需求2）：后台 sys_config channel_enabled 默认开启，
	// 关闭后拒绝创建频道（App 新建频道入口同步隐藏，此处兜底旧版本客户端）
	if v := SysConfigGet(ctx, "channel_enabled", true); !boolVal(v) {
		return nil, errs.ChannelDisabled
	}
	if strings.TrimSpace(req.NameZh) == "" && strings.TrimSpace(req.NameEn) == "" {
		return nil, errs.ParamError
	}
	isPublic := true
	if req.IsPublic != nil {
		isPublic = *req.IsPublic
	}
	// 自定义 ID 可选：格式校验（1001）→ 全表唯一查重（3008，跨 type：一个自定义 ID
	// 只能属于一个会话，将来群聊可复用该列）；空串/nil=未设置（落 NULL，不占唯一索引）。
	var shortID *string
	if req.ShortID != nil {
		sid := strings.TrimSpace(*req.ShortID)
		if sid != "" {
			if !validShortID(sid) {
				return nil, errs.ParamError
			}
			var dup int64
			store.DB.Model(&model.Conversation{}).Where("short_id = ?", sid).Count(&dup)
			if dup > 0 {
				return nil, errs.ShortIDTaken
			}
			shortID = &sid
		}
	}
	qrJoin := 0
	if isPublic {
		qrJoin = 1
	}
	conv := &model.Conversation{
		// 主键必须显式生成雪花 ID：conversation.id 无 AUTO_INCREMENT
		// （migrations/004_conversation.sql:6），漏生成则插入 id=0，
		// 第二次创建即主键冲突（Error 1062）→ 前端「创建失败」。
		ID:                id.Next(),
		Type:              model.ConvChannel,
		NameZh:            req.NameZh,
		NameEn:            req.NameEn,
		Avatar:            req.Avatar,
		OwnerID:           ownerID,
		AnnouncementZh:    req.AnnouncementZh,
		AnnouncementEn:    req.AnnouncementEn,
		MaxMembers:        groupMaxMembers(ctx),
		Status:            model.ConvNormal,
		MuteAll:           1,      // 频道恒定「仅频道主可发言」（展示冗余；发送链路按 type=3 强制，不受此开关影响）
		AllowMemberInvite: 0,      // 成员不能拉人；频道主邀请走 /conversation/:id/invite（Owner 不受限）
		QrJoinEnabled:     qrJoin, // 1=公开可自助关注 / 0=私密只能被邀请
		ShortID:           shortID,
	}
	// Select 显式列字段：GORM 对带 default tag 的字段，零值（私密 qrJoinEnabled=0、
	// allowMemberInvite=0）会被省略回退 DB 默认（1），导致私密频道变公开、成员可拉人。
	// ShortID 也在清单里：不带 default tag，但显式列出防今后加 default 后零值回退。
	if err := store.DB.Select("ID", "Type", "NameZh", "NameEn", "Avatar", "OwnerID",
		"AnnouncementZh", "AnnouncementEn", "MaxMembers", "Status",
		"MuteAll", "AllowMemberInvite", "QrJoinEnabled", "ShortID", "CreatedAt", "UpdatedAt",
	).Create(conv).Error; err != nil {
		return nil, err
	}
	if err := store.DB.Create(&model.ConversationMember{
		ConversationID: conv.ID,
		UserID:         ownerID,
		Role:           model.MemberOwner,
		JoinedAt:       time.Now(),
	}).Error; err != nil {
		return nil, err
	}
	// 小助手自动进频道（uid=-1 普通成员，幂等）：新订阅的欢迎消息由它发出
	ensureAssistantInChannel(ctx, conv.ID)
	return conv, nil
}

// ensureAssistantInChannel 小助手（AssistantUID=-1）进频道成员表（幂等，已存在跳过）。
// 成员行口径与现有助手单聊一致（CreateDirect 同样给 -1 写 conversation_member，无外键约束）。
// 新频道创建时调用一次；存量频道在首个新订阅触发 afterChannelFollow 时补挂。
// 失败只记日志：成员行缺失只会让欢迎消息发不出（SendMessage 查不到成员行），不影响关注主流程。
func ensureAssistantInChannel(ctx context.Context, convID int64) {
	var cnt int64
	store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, AssistantUID).Count(&cnt)
	if cnt > 0 {
		return
	}
	if err := store.DB.Create(&model.ConversationMember{
		ConversationID: convID,
		UserID:         AssistantUID,
		Role:           model.MemberNormal,
		JoinedAt:       time.Now(),
	}).Error; err != nil {
		log.Printf("[channel-welcome] assistant join failed conv=%d: %v", convID, err)
		return
	}
	invalidateConvMembers(ctx, convID)
	log.Printf("[channel-welcome] assistant member ensured conv=%d (was missing)", convID)
}

// afterChannelFollow 频道关注后置钩子：由小助手在频道会话发一条欢迎消息。
// 三个入口统一走这里：用户自助关注（ChannelFollow）、注册默认关注
// （DefaultChannelFollowForUser）、频道主邀请（GroupInvite 对 type=3）。
// welcomeMsg：欢迎语模板（来自后台 default_channel_config.welcomeMsg，仅注册默认关注
// 入口下发；空=默认文案「欢迎关注「频道名」」）。支持占位符 {nickname}（订阅用户昵称，
// 可出现多次）与 {channelName}（频道名 nameZh 优先），ReplaceAll 全量替换；
// {nickname} 查询失败时整体降级为默认文案，不阻塞关注流程。
// 幂等性由调用方保证——关注入口在「已是成员」时提前返回，不会触发本钩子；
// 同一用户取关后再关注会再收到一条欢迎语（属新的订阅，符合预期）。
// 发送走 SendMessage 内部链路（落库 + 未读 + WS 广播 + 离线推送），
// 不经 HTTP 与限流；失败只记日志，不阻断关注结果。
func afterChannelFollow(ctx context.Context, convID, userID int64, welcomeMsg string) {
	if userID <= 0 {
		return
	}
	conv, err := getConv(ctx, convID)
	if err != nil || conv.Type != model.ConvChannel {
		// 关键路径日志：钩子被跳过时留下原因（欢迎信息排查用，勿删）
		log.Printf("[channel-welcome] hook skipped conv=%d user=%d reason=getConvFailedOrNotChannel err=%v", convID, userID, err)
		return
	}
	// 存量频道可能还没有小助手成员行，补挂（幂等）后再发送
	ensureAssistantInChannel(ctx, convID)
	name := conv.NameZh
	if name == "" {
		name = conv.NameEn
	}
	if name == "" {
		name = "频道"
	}
	content := fmt.Sprintf("欢迎关注「%s」", name)
	if welcomeMsg != "" {
		var u model.User
		nickname := ""
		if qerr := store.DB.Select("nickname").First(&u, userID).Error; qerr == nil {
			nickname = u.Nickname
		} else {
			log.Printf("[channel-welcome] nickname lookup failed user=%d err=%v", userID, qerr)
		}
		if rendered, ok := renderChannelWelcome(welcomeMsg, nickname, name); ok {
			content = rendered
		} else {
			// 昵称查不到/为空：降级默认文案（占位符语义不完整时不硬发）
			log.Printf("[channel-welcome] nickname empty user=%d, fallback to default welcome text", userID)
		}
	}
	if _, err := SendMessage(ctx, AssistantUID, &SendMsgReq{
		ConversationID: convID,
		Type:           model.MsgText,
		Content:        content,
		ClientMsgID:    newUUID(),
	}); err != nil {
		log.Printf("[channel-welcome] send failed conv=%d user=%d err=%v", convID, userID, err)
	} else {
		// 关键路径日志：关注→钩子→发送成功（欢迎信息排查用，勿删）
		log.Printf("[channel-welcome] send ok conv=%d user=%d", convID, userID)
	}
}

// renderChannelWelcome 渲染频道欢迎语模板（纯函数，不依赖 DB，便于单测）。
// 占位符全量替换（可出现多次）：{nickname}/{订阅人昵称} → 订阅用户昵称，
// {channelName}/{频道名} → 频道名（nameZh 优先）；英文为原始写法、中文为后台文案别名。
// 返回 ok=false：模板含昵称占位符但昵称为空（查询失败/昵称为空/纯空白），
// 调用方应整体降级为默认文案，不硬发半渲染内容。
func renderChannelWelcome(tpl, nickname, channelName string) (string, bool) {
	needNick := strings.Contains(tpl, "{nickname}") || strings.Contains(tpl, "{订阅人昵称}")
	if needNick && strings.TrimSpace(nickname) == "" {
		return "", false
	}
	s := strings.ReplaceAll(tpl, "{nickname}", nickname)
	s = strings.ReplaceAll(s, "{订阅人昵称}", nickname)
	s = strings.ReplaceAll(s, "{channelName}", channelName)
	s = strings.ReplaceAll(s, "{频道名}", channelName)
	return s, true
}

// ChannelDetail 频道详情（对外展示）
type ChannelDetail struct {
	Conversation model.Conversation `json:"conversation"` // 原样会话对象（id 即频道 ID，type=3）
	ShortID      string             `json:"shortId"`      // 自定义唯一 ID（未设置为空串；conversation.shortId 为 null|string）
	IsPublic     bool               `json:"isPublic"`
	OwnerID      int64              `json:"ownerId,string"`
	OwnerName    string             `json:"ownerName"`
	OwnerAvatar  string             `json:"ownerAvatar"`
	FollowerCnt  int64              `json:"followerCount"`
	Followed     bool               `json:"followed"` // 当前用户是否已关注
}

// ChannelInfo 频道信息（按 ID 搜索/进频道页用）。
// 公开频道任何人可见；私密频道仅成员/频道主可见（非成员按不存在处理 → 搜不到）。
func ChannelInfo(ctx context.Context, viewerID, convID int64) (*ChannelDetail, error) {
	conv, err := getConv(ctx, convID)
	if err != nil || conv.Type != model.ConvChannel {
		return nil, errs.ConvNotFound
	}
	role := memberRole(ctx, convID, viewerID)
	if conv.QrJoinEnabled != 1 && role == 0 {
		// 私密频道非成员：不暴露存在性
		return nil, errs.ConvNotFound
	}
	d := &ChannelDetail{
		Conversation: *conv,
		ShortID:      model.StrVal(conv.ShortID),
		IsPublic:     conv.QrJoinEnabled == 1,
		OwnerID:      conv.OwnerID,
		FollowerCnt:  memberCount(ctx, convID),
		Followed:     role != 0,
	}
	var owner model.User
	if err := store.DB.Select("id", "nickname", "avatar").First(&owner, conv.OwnerID).Error; err == nil {
		d.OwnerName = owner.Nickname
		d.OwnerAvatar = owner.Avatar
	}
	return d, nil
}

// ChannelInfoByRef GET /channel/:id 的 :id 两段式解析（向后兼容）：
// 先按雪花 int64 解析查频道；解析失败或按雪花查不到时，再按 short_id 精确查
// type=3 会话（自定义 ID 打开频道）。两段都落空返回 4001。
func ChannelInfoByRef(ctx context.Context, viewerID int64, ref string) (*ChannelDetail, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errs.ParamError
	}
	if id64, perr := strconv.ParseInt(ref, 10, 64); perr == nil && id64 > 0 {
		if d, err := ChannelInfo(ctx, viewerID, id64); err == nil {
			return d, nil
		}
	}
	var conv model.Conversation
	if err := store.DB.Where("short_id = ? AND type = ?", ref, model.ConvChannel).
		First(&conv).Error; err != nil {
		return nil, errs.ConvNotFound
	}
	return ChannelInfo(ctx, viewerID, conv.ID)
}

// ChannelFollow 关注频道（= 进群，公开频道自助；幂等；已关注直接返回）。
func ChannelFollow(ctx context.Context, userID, convID int64) (*model.Conversation, error) {
	conv, err := getConv(ctx, convID)
	if err != nil || conv.Type != model.ConvChannel {
		return nil, errs.ConvNotFound
	}
	if conv.Status != model.ConvNormal {
		return nil, errs.ConvNotFound
	}
	var exist int64
	store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, userID).Count(&exist)
	if exist > 0 {
		return conv, nil // 幂等
	}
	// 私密频道：不可自助关注（只能被频道主邀请，走 /conversation/:id/invite）
	if conv.QrJoinEnabled != 1 {
		return nil, errs.Forbidden
	}
	// 满员校验（MaxMembers=0 表示不限，与群一致）
	var cnt int64
	store.DB.Model(&model.ConversationMember{}).Where("conversation_id = ?", convID).Count(&cnt)
	if conv.MaxMembers > 0 && cnt >= int64(conv.MaxMembers) {
		return nil, errs.GroupFull
	}
	if err := store.DB.Create(&model.ConversationMember{
		ConversationID: convID,
		UserID:         userID,
		Role:           model.MemberNormal,
		JoinedAt:       time.Now(),
	}).Error; err != nil {
		return nil, err
	}
	// 清掉历史残留未读（曾关注过又取关的场景），关注从 0 开始
	store.RDB.HDel(ctx, "unread:"+strconv.FormatInt(userID, 10), strconv.FormatInt(convID, 10))
	// 【修 R-17】成员变更立即失效缓存：新关注者最长 10s 收不到 → 收敛
	invalidateConvMembers(ctx, convID)
	PublishMemberChanged(ctx, convID)
	// 新订阅欢迎语（小助手身份，失败不阻断关注）：后台配置了自定义模板
	// default_channel_config.welcomeMsg 则全入口生效（对齐注册默认关注链路），未配置用默认文案
	dc := DefaultChannelConfigGet(ctx)
	afterChannelFollow(ctx, convID, userID, dc.WelcomeMsg)
	return conv, nil
}

// ChannelUnfollow 取消关注 = 物理删除成员行（同 GroupQuit 先例）。
// 不能用 hidden=1：hidden 语义是「删会话」，成员身份保留 → 扇出照发、成员数照算，
// 且频道一有新消息 hidden 会被归 0 重新出现在列表（= 取关无效）。
func ChannelUnfollow(ctx context.Context, userID, convID int64) error {
	conv, err := getConv(ctx, convID)
	if err != nil || conv.Type != model.ConvChannel {
		return errs.ConvNotFound
	}
	role := memberRole(ctx, convID, userID)
	if role == 0 {
		return errs.ConvNotFound // 本来就没关注
	}
	if role == model.MemberOwner {
		// 频道主不能取关自己的频道（频道不能没有主）；要退出请解散：POST /conversation/:id/disband
		return errs.Forbidden
	}
	if err := store.DB.Where("conversation_id = ? AND user_id = ?", convID, userID).
		Delete(&model.ConversationMember{}).Error; err != nil {
		return err
	}
	store.RDB.HDel(ctx, "unread:"+strconv.FormatInt(userID, 10), strconv.FormatInt(convID, 10))
	// 【修 R-17】取关者必须立刻收不到（成员缓存最长 10s 陈旧）
	invalidateConvMembers(ctx, convID)
	PublishMemberChanged(ctx, convID)
	return nil
}
