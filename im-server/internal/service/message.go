package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/pkg/id"
	"github.com/yourcompany/im-server/internal/repo"
	"github.com/yourcompany/im-server/internal/store"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func msgColl() *mongo.Collection     { return store.Mongo.Collection("message") }
func receiptColl() *mongo.Collection { return store.Mongo.Collection("message_receipt") }

// ============ 发送消息 ============

type SendMsgReq struct {
	ConversationID int64                  `json:"conversationId,string" binding:"required"`
	ClientMsgID    string                 `json:"clientMsgId"` // 客户端幂等 ID（UUID），重试重发去重
	Type           int                    `json:"type"`        // 1 文本
	Content        string                 `json:"content" binding:"required"`
	File           map[string]interface{} `json:"file"`
	Mention        []int64                `json:"mention"`
	ReplyTo        int64                  `json:"replyTo,string"`
	PayPassword    string                 `json:"payPassword"` // 支付密码（仅红包/转账时由客户端携带；服务端强制校验）
	Silent         bool                   `json:"silent"`      // 通话信令纯透传（不落库/不写未读/不进历史，2026-09-16）
}

// SendMessage 发送消息：幂等落库（先落库后确认）→ 未读 → Redis 广播推送
func SendMessage(ctx context.Context, senderID int64, req *SendMsgReq) (*model.Message, error) {
	// 【发送主流程提速 R-10】落库前的 3 次 MySQL 往返（isMember / 会话查询 / 成员角色）
	// 合并为 **1 次**成员行查询：这一行同时给出「是否成员」「角色」「个人禁言到期时间」。
	// 会话行（群禁言开关）走 10s 进程内缓存，绝大多数命中，不再每次查库。
	var mem model.ConversationMember
	if err := store.DB.Where("conversation_id = ? AND user_id = ?", req.ConversationID, senderID).
		First(&mem).Error; err != nil {
		return nil, errs.ConvNotFound
	}
	// 群禁言校验（在幂等判断之前，直接拒绝不落库）：
	//   1. 全员禁言：开启后仅群主/管理员可发言；
	//   2. 单人禁言：speak_muted_until 未到期不可发言；
	//   3. 频道（type=3）：仅频道主可发言，普通成员恒定只读（不依赖 MuteAll 开关）。
	if conv := convInfo(ctx, req.ConversationID); conv != nil &&
		(conv.Type == model.ConvGroup || conv.Type == model.ConvChannel) {
		// 频道（type=3）：仅频道主可发言，普通成员恒定只读（不依赖 MuteAll 开关）；
		// 例外：虚拟助手（senderID<0，uid=-1）豁免——新订阅欢迎消息由它在频道内发出
		if conv.Type == model.ConvChannel {
			if mem.Role != model.MemberOwner && senderID >= 0 {
				return nil, errs.ChannelOwnerOnly
			}
		} else if conv.MuteAll == 1 && mem.Role == model.MemberNormal {
			return nil, errs.GroupMutedAll
		}
		if mem.SpeakMutedUntil > time.Now().Unix() {
			return nil, errs.MemberMuted
		}
	}
	if req.Type == 0 {
		req.Type = model.MsgText
	}
	// client_msg_id 必填：客户端未传时服务端生成（保证幂等索引唯一性）
	if req.ClientMsgID == "" {
		req.ClientMsgID = newUUID()
	}
	// 幂等：同 sender + client_msg_id 重复提交直接返回已存在消息（重试安全）
	if req.ClientMsgID != "" {
		var exist model.Message
		err := msgColl().FindOne(ctx,
			bson.M{"sender_id": senderID, "client_msg_id": req.ClientMsgID}).Decode(&exist)
		if err == nil {
			return &exist, nil // 已发送过，返回原消息
		}
	}

	// type=7 通话信令「一通电话一条记录」模型（2026-09-16 第二轮）：
	//   - invite：落库为通话记录气泡（离线推送也依赖这条）；
	//   - hangup/cancel/reject（单聊、携带 inviteMsgId）：**不新增记录**，而是原地改写
	//     invite 消息内容（status=done/rejected/missed + duration）并广播 call_update
	//     事件，在线端实时刷新气泡、离线端之后拉历史拿到新内容；
	//   - 其余（accept/join/leave/sdp/ice 与群通话全部信令）：纯透传，不落库。
	//     旧实现 invite+hangup 各落一条（接通一次出现 5 条记录），已废弃。
	if req.Type == model.MsgCall {
		var sig struct {
			Action      string `json:"action"`
			InviteMsgID int64  `json:"inviteMsgId,string"` // 结束信令回指 invite 记录（雪花 ID 字符串防精度丢失）
			Duration    int    `json:"duration"`
			Group       bool   `json:"group"`
		}
		_ = json.Unmarshal([]byte(req.Content), &sig)
		action := sig.Action

		finalAction := action == "hangup" || action == "cancel" || action == "reject"
		if req.Silent || (action != "invite" && !(finalAction && !sig.Group && sig.InviteMsgID > 0)) {
			// 纯信令：不取 seq、不落库、不写未读、不进历史与离线推送，
			// 只经 Redis 广播给在线成员（客户端 silent 标记缺失时按 action 兜底判定）。
			pure := &model.Message{
				ConversationID: req.ConversationID,
				MsgID:          id.Next(),
				ClientMsgID:    req.ClientMsgID,
				SenderID:       senderID,
				Type:           req.Type,
				Content:        req.Content,
				Status:         model.MsgStatusNormal,
				CreatedAt:      time.Now(),
			}
			_ = PublishEvent(ctx, &Event{
				Type:   "message",
				ConvID: req.ConversationID,
				Data:   marshalJSON(pure),
			})
			return pure, nil
		}
		if action != "invite" {
			// 结束信令：把 invite 记录改写为最终状态（一通电话只留一条气泡）。
			// **同时必须把结束信令本身按纯消息广播**——对端 CallService 靠收到
			// hangup/cancel/reject 才能实时关通话页/停响铃；只发 call_update 不够，
			// 漏了这步会出现「我挂断了对方还在计时」「拒接后主叫还在响铃」。
			// 客户端气泡流已按 action!=invite 过滤，这条广播不会变成气泡。
			m, err := finalizeCallRecord(ctx, senderID, req.ConversationID, &sig, req.Content)
			if err == nil {
				_ = PublishEvent(ctx, &Event{
					Type:   "message",
					ConvID: req.ConversationID,
					Data: marshalJSON(&model.Message{
						ConversationID: req.ConversationID,
						MsgID:          id.Next(),
						ClientMsgID:    req.ClientMsgID,
						SenderID:       senderID,
						Type:           req.Type,
						Content:        req.Content,
						Status:         model.MsgStatusNormal,
						CreatedAt:      time.Now(),
					}),
				})
			}
			return m, err
		}
		// action == invite：继续走正常落库流程
	}

	// 红包(8) / 转账(9)：**落库前冻结资金**（B-19 + B-22）
	// 注意两点：
	//   1. 放在幂等判断之后：clientMsgId 重复提交会提前 return，保证重试不会重复冻结；
	//   2. 是**冻结**不是扣款 —— 钱从 balance 挪到 frozen，没人领时 24h 后原路退回，
	//      不会像以前那样「发出即扣款、没人领就凭空蒸发」。
	// msgID 必须在冻结前生成，资金包要按 msgID 记账。
	isMoney := req.Type == model.MsgRedPacket || req.Type == model.MsgTransfer
	msgID := id.Next()
	if isMoney {
		// 资金安全闸门：红包/转账在冻结前必须先校验支付密码（金额以服务端为准，客户端无法绕过）。
		// 未设置支付密码 → 4301（前端应在「点发红包/转账」时提前拦截并提示去设置）；
		// 密码错误/格式不符 → 4302。
		if err := VerifyPayPwd(ctx, senderID, req.PayPassword); err != nil {
			return nil, err
		}
		if err := SendMoneyFreeze(ctx, senderID, msgID, req.Type, req.Content); err != nil {
			return nil, err
		}
	}

	msg := &model.Message{
		ConversationID: req.ConversationID,
		MsgID:          msgID,
		ClientMsgID:    req.ClientMsgID,
		SenderID:       senderID,
		Type:           req.Type,
		Content:        req.Content,
		File:           req.File,
		Mention:        req.Mention,
		ReplyTo:        req.ReplyTo,
		Status:         model.MsgStatusNormal,
		CreatedAt:      time.Now(),
	}
	// 会话内单调递增序号：紧贴 InsertOne 取号，把「取号 → 落库」之间的并发窗口
	// 压到最小（旧实现在落库前隔了 ReplyTo 快照两次查询，并发下 seq=5 可能晚于
	// seq=6 落库广播，客户端已推进到 6 后 sync(seq>6) 永远拿不到 5 —— R-11）。
	// Redis 不可用直接拒绝发送（nextSeq 已 fail-fast，不再退化时间戳）。
	seq, err := nextSeq(ctx, req.ConversationID)
	if err != nil {
		if isMoney {
			RefundMoneyPacket(ctx, msgID)
		}
		return nil, err
	}
	msg.Seq = seq

	// 先落库，成功后确认（保证不丢消息）
	if _, err := msgColl().InsertOne(ctx, msg); err != nil {
		// 钱已冻结但消息没落库 → 立刻解冻退回，避免"钱被冻住、消息没了"。
		// MySQL(余额) 与 MongoDB(消息) 是两套存储，没法用数据库事务包住，
		// 所以用**补偿解冻**兜底（幂等，见 RefundMoneyPacket）。
		if isMoney {
			RefundMoneyPacket(ctx, msgID)
		}
		return nil, err
	}

	// 引用快照改为落库后异步回填（R-10）：原本这 2 次查询（Mongo FindOne + MySQL First）
	// 阻塞在落库之前，是发送关键路径上最贵的两跳。客户端本地已有被引用消息时可即时渲染
	// 引用条，因此不必占用发送路径；快照仅作为"本地没有时"的兜底与新设备首屏依据。
	if req.ReplyTo > 0 {
		go backfillReplySnapshot(msgID, req.ReplyTo)
	}

	// 群文件云盘：type=3 文件消息落库成功后同步写入 conv_files（供列表/预览/配额使用）。
	// 幂等：InsertConvFile 按 file_id 去重；此处在 InsertOne 成功之后、且幂等重试已提前返回，
	// 故每条新文件消息只写入一次。写入失败仅记日志，不影响消息下发。
	if req.Type == model.MsgFile && msg.File != nil {
		recordConvFile(ctx, msg)
	}

	// 接收方 = 会话成员（排除发送者自己，自己消息直接回显）
	memberIDs := convMemberIDs(ctx, req.ConversationID)
	receivers := make([]int64, 0, len(memberIDs))
	// 大群优化：未读计数用 Redis Pipeline 一次往返批量 HIncrBy，
	// 避免千人逐成员串行往返（原逻辑在 HTTP 协程内同步执行，千人群会严重拖慢发消息）。
	pipe := store.RDB.Pipeline()
	for _, uid := range memberIDs {
		if uid == senderID {
			continue
		}
		receivers = append(receivers, uid)
		pipe.HIncrBy(ctx, fmt.Sprintf("unread:%d", uid), fmt.Sprintf("%d", req.ConversationID), 1)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[send] unread pipeline exec failed conv=%d err=%v", req.ConversationID, err)
	}

	// Redis 广播（gateway 推送给本节点在线用户）。
	// 只带 convId：gateway 用本地在线集合 ∩ 会话成员集合扇出（R-01/R-21）。
	_ = PublishEvent(ctx, &Event{
		Type:   "message",
		ConvID: req.ConversationID,
		Data:   marshalJSON(msg),
	})

	// 离线推送兜底（极光，后台可配置）：异步执行不阻塞主流程，
	// 只推当前不在 WS 在线集合的接收者（在线用户已通过长连接实时收到）
	go PushMessageOffline(context.Background(), senderID, receivers, msg)
	return msg, nil
}

// finalizeCallRecord 把通话结束信令落到 **invite 那条记录**上（一通电话一条气泡）。
//
// status 语义（客户端 _CallBubble 据此渲染）：
//   - hangup  → done     「{type}通话 00:05」（duration 取结束信令携带值）
//   - reject  → rejected 「对方拒绝接听」
//   - cancel  → missed   「通话未接通」（含主叫 45s 振铃超时自动取消）
//
// 安全校验：invite 消息必须存在、type=7 且属于同一会话（发送者已在 SendMessage
// 前置校验过会话成员身份，天然允许主叫 hangup / 被叫 reject 两种角色）。
// 已带 status 的记录不再改写（先到先得，防双方并发挂断互相覆盖）。
func finalizeCallRecord(ctx context.Context, senderID, convID int64, sig *struct {
	Action      string `json:"action"`
	InviteMsgID int64  `json:"inviteMsgId,string"`
	Duration    int    `json:"duration"`
	Group       bool   `json:"group"`
}, rawContent string) (*model.Message, error) {
	var inv model.Message
	if err := msgColl().FindOne(ctx, bson.M{
		"msg_id": sig.InviteMsgID, "type": model.MsgCall, "conversation_id": convID,
	}).Decode(&inv); err != nil {
		// invite 记录找不到（旧数据/参数错）→ 按纯信令处理，不报错
		return &model.Message{
			ConversationID: convID, MsgID: id.Next(), ClientMsgID: newUUID(),
			SenderID: senderID, Type: model.MsgCall, Content: rawContent,
			Status: model.MsgStatusNormal, CreatedAt: time.Now(),
		}, nil
	}

	// 角色硬校验：cancel 只能主叫（invite 发起者）；reject 只能被叫。
	// 主叫"拒绝"自己的通话只会来自 invite 回显/同账号其他设备的占线误拒——
	// 实测「一拨就提示被拒绝、对方还在响铃」即此，服务端直接拒绝改写。
	switch sig.Action {
	case "cancel":
		if inv.SenderID != senderID {
			return nil, &errs.Err{Code: 4003, Msg: "仅主叫可取消通话"}
		}
	case "reject":
		if inv.SenderID == senderID {
			return nil, &errs.Err{Code: 4003, Msg: "不能拒绝自己的通话"}
		}
	}

	var body map[string]interface{}
	if err := json.Unmarshal([]byte(inv.Content), &body); err != nil {
		body = map[string]interface{}{}
	}
	if _, done := body["status"]; done {
		return &inv, nil // 已定型（对方已先挂断/拒绝），不再覆盖
	}
	status := "missed"
	switch sig.Action {
	case "hangup":
		status = "done"
		body["duration"] = sig.Duration
	case "reject":
		status = "rejected"
	}
	body["status"] = status
	newContent, _ := json.Marshal(body)

	if _, err := msgColl().UpdateOne(ctx,
		bson.M{"msg_id": sig.InviteMsgID},
		bson.M{"$set": bson.M{"content": string(newContent)}}); err != nil {
		return nil, err
	}
	inv.Content = string(newContent)

	// 广播改写事件：在线端把对应气泡原地刷新（离线端靠历史同步拿到新 content）
	_ = PublishEvent(ctx, &Event{
		Type:   "call_update",
		ConvID: convID,
		Data: marshalJSON(map[string]interface{}{
			"conversationId": fmt.Sprintf("%d", convID),
			"msgId":          fmt.Sprintf("%d", sig.InviteMsgID),
			"content":        string(newContent),
		}),
	})
	return &inv, nil
}

// backfillReplySnapshot 落库后异步回填引用快照。
//
// 三个必须遵守的点：
//  1. 用 context.Background() 而不是请求 ctx —— HTTP 处理函数返回后 c.Request.Context()
//     会立刻被取消，沿用请求 ctx 的话回填必然失败；
//  2. 自带超时，避免 goroutine 无限悬挂；
//  3. defer recover —— 裸 goroutine 里 panic 会直接打挂整个进程（R-19 的教训）。
//
// 失败只记日志、不重试：引用条是展示性字段，客户端有本地兜底；
// 而"消息本身"已落库并广播，绝不能因为回填失败影响发送结果。
func backfillReplySnapshot(msgID, replyTo int64) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[reply-snapshot] panic msg=%d replyTo=%d: %v", msgID, replyTo, r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var ref model.Message
	if err := msgColl().FindOne(ctx, bson.M{"msg_id": replyTo}).Decode(&ref); err != nil {
		log.Printf("[reply-snapshot] 取被引用消息失败 msg=%d replyTo=%d: %v", msgID, replyTo, err)
		return
	}
	refSender := ""
	var refUser model.User
	if err := store.DB.WithContext(ctx).First(&refUser, ref.SenderID).Error; err == nil {
		refSender = refUser.Nickname
	}
	snap := map[string]interface{}{
		"content":    ref.Content,
		"senderName": refSender,
		"senderId":   ref.SenderID,
		"type":       ref.Type,
	}
	if _, err := msgColl().UpdateOne(ctx, bson.M{"msg_id": msgID},
		bson.M{"$set": bson.M{"reply_snapshot": snap}}); err != nil {
		log.Printf("[reply-snapshot] 回填失败 msg=%d: %v", msgID, err)
	}
}

// ============ 会话 seq（单调递增水位）============
//
// 水位是「不漏消息」的地基：客户端以 (lastSeq, serverSeq] 做断点续传，
// 一旦水位回退（Redis 丢数据 / 实例重建 / AOF 丢增量 → INCR 从小值重来），
// 客户端的 lastSeq 就永远追不上服务端 → 该会话**永久收不到新消息**。
// 因此这里定三条铁律：
//   1. 取号失败必须 fail-fast（绝不退化为时间戳，1.76e18 的水位会毒死客户端）；
//   2. 水位只能单调向前（repairSeq 以 Mongo 真实 max(seq) 为基准修正）；
//   3. 读水位前先自愈，保证 serverSeq 永远 >= Mongo 真实值。

const (
	syncDefaultLimit = 200 // Sync 默认单页条数
	syncMaxLimit     = 500 // Sync 单页硬上限（大群断线积压 >200 条时补拉能一页多拿）
)

// SyncResult 增量补拉结果。
// 相比旧版裸数组，多带三个元信息，客户端才能正确分页与自愈：
//   - HasMore：本次是否还有后续（客户端据以循环翻页，修 R-07）
//   - ServerSeq：服务端当前水位（客户端据以判断水位回退，修 R-04）
//   - Reset：服务端水位确实回退了，客户端须以服务端为准重置断点
type SyncResult struct {
	List      []model.Message `json:"list"`
	HasMore   bool            `json:"hasMore"`
	MaxSeq    int64           `json:"maxSeq"`
	ServerSeq int64           `json:"serverSeq"`
	Reset     bool            `json:"reset"`
}

// nextSeq 会话内单调递增序号（Redis INCR；并发安全）。
//
// 旧实现在 Redis 不可用时退化为 `time.Now().UnixNano()`（≈1.76e18）——
// 这一条把整个会话的水位顶到天上，客户端之后 `sync(afterSeq=1.76e18)` 永远
// 查不到任何消息，且**不可逆**。宁可让这一次发送失败（用户看得到、可重试），
// 也不能产出一个错误水位（用户看不到、永久丢消息）。
func nextSeq(ctx context.Context, convID int64) (int64, error) {
	if store.RDB == nil {
		return 0, errs.SeqUnavailable
	}
	seq, err := store.RDB.Incr(ctx, convSeqKey(convID)).Result()
	if err != nil {
		return 0, errs.SeqUnavailable
	}
	// 取号成功即登记为「活跃会话」，供水位自愈定时任务扫描（见 repairSeqWorker）
	markConvActive(convID)
	return seq, nil
}

func convSeqKey(convID int64) string { return fmt.Sprintf("conv:seq:%d", convID) }

// Sync 增量补拉：重连补偿/上线拉取 seq > afterSeq 的消息（按 seq 升序）。
//
// 返回 SyncResult（含 hasMore / serverSeq / reset），客户端据此：
//   - hasMore=true → 继续翻页（不再像旧版那样只拉一页就停，漏掉第 201 条之后）
//   - reset=true   → 清空本地缓存并按 serverSeq 重建断点
func Sync(ctx context.Context, userID, convID, afterSeq int64, limit int64) (*SyncResult, error) {
	if !isMember(ctx, convID, userID) {
		return nil, errs.ConvNotFound
	}
	if limit <= 0 {
		limit = syncDefaultLimit
	}
	if limit > syncMaxLimit {
		limit = syncMaxLimit
	}
	filter := bson.M{
		"conversation_id": convID,
		"seq":             bson.M{"$gt": afterSeq},
		"blocked":         bson.M{"$ne": true}, // 后台屏蔽的消息不下发
	}
	// 「删除聊天记录」seq 位点（2026-09-18）：请求者断点落在被删窗口内时，
	// 补拉会把已删消息 + 新消息一起拉回（客户端重复显示），用位点兜底过滤。
	var selfMember model.ConversationMember
	if err := store.DB.Select("cleared_seq").
		Where("conversation_id = ? AND user_id = ?", convID, userID).
		First(&selfMember).Error; err == nil && selfMember.ClearedSeq > afterSeq {
		seqFilter := filter["seq"].(bson.M)
		seqFilter["$gt"] = selfMember.ClearedSeq
	}
	// 多取 1 条用于判断 hasMore（不返回给客户端，避免多拉一条造成客户端重复分页）
	opts := options.Find().SetSort(bson.D{{Key: "seq", Value: 1}}).SetLimit(limit + 1)
	cur, err := msgColl().Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var msgs []model.Message
	if err := cur.All(ctx, &msgs); err != nil {
		return nil, err
	}
	res := &SyncResult{}
	if int64(len(msgs)) > limit {
		res.HasMore = true
		msgs = msgs[:limit]
	}
	res.List = msgs
	if res.List == nil {
		res.List = []model.Message{}
	}
	for i := range res.List {
		if res.List[i].Seq > res.MaxSeq {
			res.MaxSeq = res.List[i].Seq
		}
	}
	// 服务端水位（读之前先自愈，保证 >= Mongo 真实 max seq）
	if serverSeq, err := LastSeq(ctx, convID); err == nil {
		res.ServerSeq = serverSeq
		// 客户端断点比服务端水位还大 → 只能是服务端水位回退过（Redis 丢数据/重建）。
		// 此时客户端若继续按自己的断点拉，将永远拉不到东西，必须重置。
		res.Reset = afterSeq > serverSeq
	} else {
		// 读不到水位（Redis 故障）时**不**宣称 reset：
		// 一次瞬时故障不该让客户端清掉本地缓存（那才是真丢内容）。
		res.ServerSeq = res.MaxSeq
	}
	return res, nil
}

// LastSeq 会话当前最新 seq（客户端保存断点）。
//
// 与旧版的区别：读之前先做一次**水位自愈**（repairSeq），
// 以 Mongo 真实 max(seq) 单调向前修正 Redis，保证返回值不会落后于 Mongo。
func LastSeq(ctx context.Context, convID int64) (int64, error) {
	seq, err := repairSeq(ctx, convID)
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// LastSeqForUser 给客户端用的水位查询（带成员鉴权 + 水位自愈）。
// 客户端进会话时调一次，把本地 lastSeq 与服务端对齐，
// 避免「本地水位落后/超前却毫无察觉」导致的漏消息。
func LastSeqForUser(ctx context.Context, userID, convID int64) (int64, error) {
	if !isMember(ctx, convID, userID) {
		return 0, errs.ConvNotFound
	}
	return LastSeq(ctx, convID)
}

// mongoMaxSeq 取 Mongo 中该会话的真实最大 seq（走 (conversation_id, seq) 索引，O(log n)）
func mongoMaxSeq(ctx context.Context, convID int64) (int64, error) {
	var m struct {
		Seq int64 `bson:"seq"`
	}
	err := msgColl().FindOne(ctx,
		bson.M{"conversation_id": convID},
		options.FindOne().SetSort(bson.D{{Key: "seq", Value: -1}}).
			SetProjection(bson.M{"seq": 1})).Decode(&m)
	if err != nil {
		return 0, err
	}
	return m.Seq, nil
}

// ---- 自愈结果短缓存 ----
// Sync 每次都要读一次水位（客户端翻页时一秒内可能好几次），
// 若每次都回源 Mongo 会平白多出一倍查询。水位是个「慢变量」，5s 内复用即可。
// 手工/定时修复走 force 路径，绕过缓存。

type seqRepairEntry struct {
	seq    int64
	expire time.Time
}

var (
	seqRepairMu    sync.Mutex
	seqRepairCache = map[int64]seqRepairEntry{}
	seqRepairTTL   = 5 * time.Second
)

func seqRepairCacheGet(convID int64) (int64, bool) {
	seqRepairMu.Lock()
	defer seqRepairMu.Unlock()
	e, ok := seqRepairCache[convID]
	if !ok || time.Now().After(e.expire) {
		return 0, false
	}
	return e.seq, true
}

func seqRepairCacheSet(convID, seq int64) {
	seqRepairMu.Lock()
	defer seqRepairMu.Unlock()
	seqRepairCache[convID] = seqRepairEntry{seq: seq, expire: time.Now().Add(seqRepairTTL)}
	// 长期运行时裁剪，避免会话多导致 map 无限增长
	if len(seqRepairCache) > 20000 {
		now := time.Now()
		for k, e := range seqRepairCache {
			if now.After(e.expire) {
				delete(seqRepairCache, k)
			}
		}
	}
}

// repairSeq 水位自愈（带 5s 结果缓存）。
//
// 只调大不调小（Redis 水位高于 Mongo 是正常的：最后几条可能刚取号还没落库，
// 或者是被后台屏蔽掉的消息）。返回修正后的水位。
func repairSeq(ctx context.Context, convID int64) (int64, error) {
	return repairSeqDo(ctx, convID, false)
}

// repairSeqForce 绕过缓存强制回源自愈（运维接口 / 定时任务用）
func repairSeqForce(ctx context.Context, convID int64) (int64, error) {
	return repairSeqDo(ctx, convID, true)
}

func repairSeqDo(ctx context.Context, convID int64, force bool) (int64, error) {
	if !force {
		if seq, ok := seqRepairCacheGet(convID); ok {
			return seq, nil
		}
	}
	seq, err := repairSeqSource(ctx, convID)
	if err == nil {
		seqRepairCacheSet(convID, seq)
	}
	return seq, err
}

func repairSeqSource(ctx context.Context, convID int64) (int64, error) {
	if store.RDB == nil {
		return 0, errs.SeqUnavailable
	}
	key := convSeqKey(convID)
	cur, err := store.RDB.Get(ctx, key).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	real, err := mongoMaxSeq(ctx, convID)
	if err != nil {
		// 会话还没有任何消息（mongo.ErrNoDocuments）→ Redis 值即权威
		if errors.Is(err, mongo.ErrNoDocuments) {
			return cur, nil
		}
		return cur, err
	}
	if real > cur {
		if err := store.RDB.Set(ctx, key, real, 0).Err(); err != nil {
			return cur, err
		}
		log.Printf("[seq] repaired conv=%d %d -> %d (source=mongo)", convID, cur, real)
		return real, nil
	}
	return cur, nil
}

// ---- 活跃会话登记（水位自愈定时任务的扫描范围）----

var (
	activeConvMu sync.RWMutex
	activeConvs  = map[int64]time.Time{} // convID -> 最近活跃时间
	activeMaxAge = 24 * time.Hour        // 超过此时长未活跃即移出扫描集合
)

func markConvActive(convID int64) {
	if convID <= 0 {
		return
	}
	now := time.Now()
	activeConvMu.Lock()
	if t, ok := activeConvs[convID]; !ok || now.Sub(t) > time.Minute {
		activeConvs[convID] = now
	}
	activeConvMu.Unlock()
}

func snapshotActiveConvs() []int64 {
	activeConvMu.RLock()
	defer activeConvMu.RUnlock()
	ids := make([]int64, 0, len(activeConvs))
	for id := range activeConvs {
		ids = append(ids, id)
	}
	return ids
}

// ---- 运维接口：手工触发水位修复 ----

// RepairSeqResult 一次水位修复的结果（运维接口返回体）
type RepairSeqResult struct {
	ConvID int64 `json:"convId"`
	Before int64 `json:"before"`
	After  int64 `json:"after"`
	Fixed  bool  `json:"fixed"`
}

// RepairSeqAdmin 手工修复水位。convID<=0 时扫描全部会话。
// 返回修复条目 + 扫描数量，供运维确认「确实修好了」而不是猜。
func RepairSeqAdmin(ctx context.Context, convID int64) (fixed []RepairSeqResult, scanned int, err error) {
	ids := []int64{convID}
	if convID <= 0 {
		ids = nil
		if err := store.DB.Model(&model.Conversation{}).
			Order("id desc").Limit(20000).Pluck("id", &ids).Error; err != nil {
			return nil, 0, err
		}
	}
	for _, id := range ids {
		before, _ := store.RDB.Get(ctx, convSeqKey(id)).Int64()
		after, e := repairSeqForce(ctx, id)
		if e != nil {
			continue
		}
		scanned++
		if after > before {
			fixed = append(fixed, RepairSeqResult{ConvID: id, Before: before, After: after, Fixed: true})
		}
	}
	if fixed == nil {
		fixed = []RepairSeqResult{}
	}
	return fixed, scanned, nil
}

// StartSeqRepairWorker 启动水位自愈定时任务。
//   - 启动时：全量扫描一次（覆盖「进程重启期间 Redis 被重建」这种场景）
//   - 之后：每 5 分钟扫描活跃会话
//
// 单个会话的修复成本是 1 次 Mongo FindOne（走索引）+ 1 次 Redis GET，
// 全量扫描限制在最近 20000 个会话内，启动一次可接受。
func StartSeqRepairWorker(ctx context.Context) {
	repairAll := func() {
		ids := snapshotActiveConvs()
		if len(ids) == 0 {
			return
		}
		n := 0
		for _, id := range ids {
			before, _ := store.RDB.Get(ctx, convSeqKey(id)).Int64()
			after, err := repairSeqForce(ctx, id)
			if err != nil {
				continue
			}
			if after > before {
				n++
				log.Printf("[seq] periodic repair conv=%d %d -> %d", id, before, after)
			}
		}
		if n > 0 {
			log.Printf("[seq] periodic repair done, fixed=%d scanned=%d", n, len(ids))
		}
		// 顺带裁剪长期不活跃的会话，避免集合无限增长
		cutoff := time.Now().Add(-activeMaxAge)
		activeConvMu.Lock()
		for id, t := range activeConvs {
			if t.Before(cutoff) {
				delete(activeConvs, id)
			}
		}
		activeConvMu.Unlock()
	}
	// 启动全量扫描：直接从 MySQL 取会话 ID（此时内存活跃集合还是空的）
	go func() {
		var ids []int64
		if err := store.DB.Model(&model.Conversation{}).
			Order("id desc").Limit(20000).Pluck("id", &ids).Error; err != nil {
			log.Printf("[seq] startup full scan load conv ids failed: %v", err)
		}
		n := 0
		for _, id := range ids {
			before, _ := store.RDB.Get(ctx, convSeqKey(id)).Int64()
			after, err := repairSeqForce(ctx, id)
			if err != nil {
				continue
			}
			if after > before {
				n++
				log.Printf("[seq] startup repair conv=%d %d -> %d", id, before, after)
			}
		}
		log.Printf("[seq] startup full scan done, scanned=%d fixed=%d", len(ids), n)
	}()
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				repairAll()
			}
		}
	}()
	log.Println("[seq] repair worker started (startup full scan + every 5m)")
}

// ============ 历史消息 ============

// History 分页拉取：beforeMsgId 之前 limit 条
func History(ctx context.Context, userID, convID, beforeMsgID int64, limit int64) ([]model.Message, error) {
	if !isMember(ctx, convID, userID) {
		return nil, errs.ConvNotFound
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	filter := bson.M{"conversation_id": convID, "blocked": bson.M{"$ne": true}}
	// 「删除双方聊天记录」软删位点：请求者成员行的 cleared_msg_id 之后的消息才可见
	// （消息本体不删，后台可查原文）。单聊任一方触发删除时双方位点同时推进。
	var selfMember model.ConversationMember
	if err := store.DB.Select("cleared_msg_id").
		Where("conversation_id = ? AND user_id = ?", convID, userID).
		First(&selfMember).Error; err == nil && selfMember.ClearedMsgID > 0 {
		msgFilter, _ := filter["msg_id"].(bson.M)
		if msgFilter == nil {
			msgFilter = bson.M{}
		}
		msgFilter["$gt"] = selfMember.ClearedMsgID
		filter["msg_id"] = msgFilter
	}
	if beforeMsgID > 0 {
		msgFilter, _ := filter["msg_id"].(bson.M)
		if msgFilter == nil {
			msgFilter = bson.M{}
		}
		msgFilter["$lt"] = beforeMsgID
		filter["msg_id"] = msgFilter
	}
	opts := options.Find().SetSort(bson.D{{Key: "msg_id", Value: -1}}).SetLimit(limit)
	cur, err := msgColl().Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var msgs []model.Message
	if err := cur.All(ctx, &msgs); err != nil {
		return nil, err
	}
	// 倒序返回（时间正序）
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	// 需求4：单聊我发的消息，对方已读 → delivery_state=read（fill Delivery 字段）。
	// 对方关闭「发送已读回执」时不下发 read/sent 推导（Delivery 留空），双向语义：
	// 我关了回执 → 任何人（含历史拉取）都看不到我的已读位点；last_read_msg_id 本身照写，
	// 未读红点/补拉位点不受影响。
	var conv model.Conversation
	if err := store.DB.First(&conv, convID).Error; err == nil && conv.Type == model.ConvDirect {
		otherID := directOtherID(ctx, convID, userID)
		if otherID > 0 && UserFlagCached(ctx, otherID, "read_receipt_enabled") == 1 {
			var other model.ConversationMember
			if err := store.DB.Where("conversation_id = ? AND user_id = ?", convID, otherID).
				First(&other).Error; err == nil {
				for i := range msgs {
					if msgs[i].SenderID == userID {
						if other.LastReadMsgID >= msgs[i].MsgID {
							msgs[i].Delivery = "read"
						} else {
							msgs[i].Delivery = "sent"
						}
					}
				}
			}
		}
	}
	return msgs, nil
}

// ============ 删除双方聊天记录（单聊软删） ============

// ClearDirectHistory 「删除聊天记录」（单聊软删）：
//   - 仅单聊可用（群聊没有对端语义，入口不暴露）；
//   - **不删消息本体**：Mongo 里的消息原样保留（后台/取证可查原文）；
//   - scope = "self"：只把**发起者**的 cleared_msg_id 位点推进到当前最大 msg_id
//     （仅为我删除——重启/重拉网络后服务端 History/ConvList 同样过滤，不再显示）；
//     scope = "both"：把会话**全部成员**（单聊即双方）的位点一并推进（为双方删除）；
//   - 推送 history.cleared 事件：both → 推给双方（在线端实时清空聊天窗与缓存）；
//     self → 只推给发起者的其他设备（对端不受影响，无需感知）。
//     离线端下次进会话由 History 过滤 + 客户端空窗口对账兜底。
//
// 重复触发幂等：位点只前进不回退（cleared_msg_id < max 才更新）。
func ClearDirectHistory(ctx context.Context, userID, convID int64, scope string) (int64, error) {
	if !isMember(ctx, convID, userID) {
		return 0, errs.ConvNotFound
	}
	var conv model.Conversation
	if err := store.DB.First(&conv, convID).Error; err != nil || conv.Type != model.ConvDirect {
		return 0, errs.ParamError
	}
	// 当前会话最大 msg_id（消息本体不动，只推位点）
	var maxMsgID int64
	var maxRow struct {
		MsgID int64 `bson:"msg_id"`
	}
	if err := msgColl().FindOne(ctx, bson.M{"conversation_id": convID},
		options.FindOne().SetSort(bson.D{{Key: "msg_id", Value: -1}}),
		options.FindOne().SetProjection(bson.M{"msg_id": 1}),
	).Decode(&maxRow); err == nil {
		maxMsgID = maxRow.MsgID
	} // 空会话 Decode 报 no documents，maxMsgID 保持 0，直接成功返回
	if maxMsgID <= 0 {
		return 0, nil
	}
	// 位点推进（条件更新保证幂等，位点永不回退）。
	// seq 位点同步记录：Sync 按 seq 增量补拉（WS 断线重连），没有 seq 过滤的话
	// 客户端断点落在被删窗口内时会把已删消息 + 新消息一起补拉回来（重复显示）。
	var maxSeq int64
	var maxSeqRow struct {
		Seq int64 `bson:"seq"`
	}
	if err := msgColl().FindOne(ctx, bson.M{"conversation_id": convID},
		options.FindOne().SetSort(bson.D{{Key: "seq", Value: -1}}),
		options.FindOne().SetProjection(bson.M{"seq": 1}),
	).Decode(&maxSeqRow); err == nil {
		maxSeq = maxSeqRow.Seq
	}
	upd := store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND cleared_msg_id < ?", convID, maxMsgID)
	if scope == "self" {
		upd = upd.Where("user_id = ?", userID)
	}
	if res := upd.Updates(map[string]interface{}{
		"cleared_msg_id": maxMsgID,
		"cleared_seq":    maxSeq,
	}); res.Error != nil {
		return 0, errs.Internal
	}
	// 事件通知：both → 双方；self → 仅发起者（他自己的其他设备同步清缓存）。
	// **scope=self 绝不能带 ConvID**：dispatchEvent 对 ConvID>0 的事件按会话全体
	// 成员扇出，带上会对端也收到清空指令（对方位点未动 → 清空后重进又全量显示）。
	var uids []int64
	if scope == "self" {
		uids = []int64{userID}
	} else {
		store.DB.Model(&model.ConversationMember{}).
			Where("conversation_id = ?", convID).Pluck("user_id", &uids)
	}
	ev := &Event{
		Type:   "history.cleared",
		ToUIDs: uids,
		Data: marshalJSON(map[string]interface{}{
			"conversationId": fmt.Sprintf("%d", convID),
			"clearedMsgId":   fmt.Sprintf("%d", maxMsgID),
			"clearedSeq":     maxSeq,
			"byUserId":       fmt.Sprintf("%d", userID),
			"scope":          scope,
		}),
	}
	if scope != "self" {
		ev.ConvID = convID // both：成员即双方，ConvID 扇出等价于 ToUIDs
	}
	_ = PublishEvent(ctx, ev)
	return maxMsgID, nil
}

// ============ 撤回 ============

// RecallMessage 撤回：本人 2 分钟内；群主/管理员不限时撤群成员
func RecallMessage(ctx context.Context, userID int64, msgID int64) error {
	var msg model.Message
	err := msgColl().FindOne(ctx, bson.M{"msg_id": msgID}).Decode(&msg)
	if err != nil {
		return &errs.Err{Code: 4003, Msg: "消息不存在"}
	}
	// 权限
	if msg.SenderID != userID {
		role := memberRole(ctx, msg.ConversationID, userID)
		if role != model.MemberOwner && role != model.MemberAdmin {
			return errs.RecallDenied
		}
	} else if time.Since(msg.CreatedAt) > 2*time.Minute {
		return errs.RecallDenied
	}

	update := bson.M{
		"$set": bson.M{
			"recalled": true, "recalled_by": userID, "status": model.MsgStatusRecalled,
		},
	}
	if _, err := msgColl().UpdateOne(ctx, bson.M{"msg_id": msgID}, update); err != nil {
		return err
	}
	// 撤回红包/转账：立刻把还没被领走的冻结款退回发送者，不等 24h 到期任务。
	// 否则发送者会看到「消息已撤回、钱却还冻结在账上」的中间态。
	// refundPacket 幂等（非 Open 直接 return nil），且撤回与到期扫描并发时只退一次；
	// 退款失败只记日志，不能让已经成功的撤回对外报错。
	if msg.Type == model.MsgRedPacket || msg.Type == model.MsgTransfer {
		if p := loadPacket(msgID); p != nil && p.Status == model.MoneyPacketOpen {
			// 文案统一为「退回红包 / 退回转账」：用户账单只认这三种
			// （发红包 / 领取红包 / 退回红包），"撤回"这类内部触发原因不对外。
			// 需要区分来源时看 type + remark（见 refundPacket 的 remark 参数）。
			title := "退回红包"
			if p.Kind == model.MsgTransfer {
				title = "退回转账"
			}
			if err := refundPacket(ctx, p, title); err != nil {
				log.Printf("[recall] 撤回退回失败 msgID=%d err=%v", msgID, err)
			}
		}
	}
	// 广播撤回通知
	_ = PublishEvent(ctx, &Event{
		Type:   "recall",
		ConvID: msg.ConversationID,
		Data:   marshalJSON(map[string]interface{}{"conversationId": fmt.Sprintf("%d", msg.ConversationID), "msgId": fmt.Sprintf("%d", msgID), "recalledBy": fmt.Sprintf("%d", userID)}),
	})
	return nil
}

// ============ 已读上报 ============

// MarkRead 上报已读：写回执 + 清未读 + 广播 read 事件
//
// 隐私开关（read_receipt_enabled）只作用于「让别人看到我的已读」：
// 关闭时跳过 Mongo 回执写入与 read 广播（群聊本来就不广播），
// 但 last_read_msg_id 与 unread 清理**照常执行**——未读红点、hasUnread、
// 补拉位点都依赖它们，绝不能因隐私开关而停写。
func MarkRead(ctx context.Context, userID, convID, msgID int64) error {
	if !isMember(ctx, convID, userID) {
		return errs.ConvNotFound
	}
	// 更新成员已读位点
	store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, userID).
		Update("last_read_msg_id", msgID)
	// 清未读
	store.RDB.HDel(ctx, fmt.Sprintf("unread:%d", userID), fmt.Sprintf("%d", convID))

	// 我是否愿意向别人展示已读（10s 缓存，见 UserFlagCached）
	receiptOn := UserFlagCached(ctx, userID, "read_receipt_enabled") == 1

	// 群聊按人展示：写入回执
	if msgID > 0 && receiptOn {
		_, _ = receiptColl().UpdateOne(ctx,
			bson.M{"conversation_id": convID, "msg_id": msgID, "user_id": userID},
			bson.M{"$setOnInsert": bson.M{
				"conversation_id": convID, "msg_id": msgID, "user_id": userID, "read_at": time.Now(),
			}},
			options.Update().SetUpsert(true))
	}
	// 【群聊不广播 read —— 扇出收敛】
	//
	// 旧实现无条件向整个会话广播 read：2000 人群里每读一条消息都要向全群在线成员
	// 推一次 read 事件（即便 T03 之后扇出已降到 O(本节点在线成员)，规模仍没变）：
	// 10 msg/s 的阅读节奏 ≈ 每秒两万次 push，是纯粹的扇出放大。
	//
	// 更关键的是：**群聊的 read 广播目前没有任何 UI 消费点** ——
	//   - conversation.go 里 Delivery="read" 只在 `conv.Type == model.ConvDirect` 分支设置；
	//   - History 里给自己的消息填充 Delivery 同样只在 ConvDirect 分支。
	// 也就是说推给全群的 read，前端根本没人用。
	//
	// 已读状态本身一点没丢：
	//   1. last_read_msg_id 已落 MySQL（会话列表红点/未读数就是这么算的）；
	//   2. 回执已写 Mongo（群按人展示用）；
	//   3. 客户端需要时走 Receipts 接口按需拉取。
	//
	// 单聊必须保留广播：「对方已读」要实时翻转，是核心体验，不能砍。
	//
	// convInfo 走 10s 进程内缓存，绝大多数命中，不额外增加 DB 往返；
	// 万一取不到会话（异常），按单聊兜底，保持与旧行为一致，不做行为回退。
	convType := model.ConvDirect
	if c := convInfo(ctx, convID); c != nil {
		convType = c.Type
	}
	// 仅单聊广播 read：群与频道（type=3）一律不广播。
	// 频道此前落入「非群才广播」分支（type=3 != ConvGroup），大频道里每个关注者的
	// 已读都会全频道扇出，且 PC/移动端对该帧均无消费点（Delivery=read 只在 ConvDirect
	// 分支推导），与上面群聊的扇出收敛同理。已读状态本身不丢：
	// last_read_msg_id 落 MySQL（红点/补拉位点）、回执写 Mongo（按需 Receipts 拉取）。
	if convType == model.ConvDirect {
		if receiptOn {
			// ID 用字符串：避免 H5 JS 大整数精度丢失导致前端匹配失败（勿改回数字）
			_ = PublishEvent(ctx, &Event{
				Type:   "read",
				ConvID: convID,
				Data:   marshalJSON(map[string]interface{}{"conversationId": fmt.Sprintf("%d", convID), "userId": fmt.Sprintf("%d", userID), "msgId": fmt.Sprintf("%d", msgID)}),
			})
		}
	}
	return nil
}

// ReceiptSummary 群消息已读详情（L1：N 已读 / 总数 / 名单）。
//
// 设计要点（与「已读回执收敛」一致，不引入广播）：
//   - Receipts：按人展示的「谁读了」名单，来自 Mongo MessageReceipt，按隐私开关过滤
//     （关闭 read_receipt_enabled 的成员不出现）。
//   - ReadCount：已读人数，聚合自 MySQL ConversationMember.last_read_msg_id >= msgID。
//     用 MySQL 位点而非 Mongo 回执统计 —— 这样**关闭回执的成员也被计入分母**，
//     「N 已读」数字准确；名单仍只展示开启者（隐私不泄露）。
//   - MemberCount：群成员总数，用于「N / 总数」展示。
// 三者都是**按需拉取**（用户打开详情才查），无扇出、无广播。
// ReceiptItem 已读名单条目：服务端补齐昵称/头像下发。
// 客户端本地群成员缓存不全（大群只拉部分成员），靠前端解析必然回落雪花 ID；
// 统一服务端补齐：群昵称（ConversationMember.Nickname）优先，回落用户昵称。
type ReceiptItem struct {
	UserID   int64     `json:"userId,string"`
	ReadAt   time.Time `json:"readAt"`
	Nickname string    `json:"nickname"`
	Avatar   string    `json:"avatar"`
}

type ReceiptSummary struct {
	Receipts    []ReceiptItem `json:"receipts"`
	ReadCount   int64         `json:"readCount"`
	MemberCount int64         `json:"memberCount"`
}

// Receipts 已读成员列表（群按人展示）。
// 双向隐私语义：关闭「发送已读回执」的成员不出现在回执列表里（即使历史上有回执记录）。
func Receipts(ctx context.Context, userID, convID, msgID int64) (*ReceiptSummary, error) {
	if !isMember(ctx, convID, userID) {
		return nil, errs.ConvNotFound
	}
	// 名单（Mongo，按隐私开关过滤：关闭回执的成员不出现在列表）
	cur, err := receiptColl().Find(ctx, bson.M{"conversation_id": convID, "msg_id": msgID})
	if err != nil {
		return nil, err
	}
	var receipts []model.MessageReceipt
	cur.All(ctx, &receipts)
	out := make([]ReceiptItem, 0, len(receipts))
	uids := make([]int64, 0, len(receipts))
	for _, r := range receipts {
		if UserFlagCached(ctx, r.UserID, "read_receipt_enabled") == 1 {
			out = append(out, ReceiptItem{UserID: r.UserID, ReadAt: r.ReadAt})
			uids = append(uids, r.UserID)
		}
	}
	// 批量补昵称/头像（2 条 IN 查询）：群昵称优先，回落用户昵称；头像取用户表
	if len(uids) > 0 {
		var users []model.User
		store.DB.Where("id IN ?", uids).Find(&users)
		um := make(map[int64]*model.User, len(users))
		for i := range users {
			um[users[i].ID] = &users[i]
		}
		var members []model.ConversationMember
		store.DB.Where("conversation_id = ? AND user_id IN ?", convID, uids).Find(&members)
		memberNick := make(map[int64]string, len(members))
		for _, m := range members {
			if m.Nickname != "" {
				memberNick[m.UserID] = m.Nickname
			}
		}
		for i := range out {
			o := &out[i]
			if n, ok := memberNick[o.UserID]; ok {
				o.Nickname = n
			} else if u, ok := um[o.UserID]; ok {
				o.Nickname = u.Nickname
			}
			if u, ok := um[o.UserID]; ok {
				o.Avatar = u.Avatar
			}
		}
	}
	// 计数（MySQL 聚合，含关闭回执者 → 分母准）：
	// 群成员 last_read_msg_id >= msgID 即视为已读（读位点单调前进，见 MarkRead）。
	var readCount, memberCount int64
	if msgID > 0 {
		store.DB.Model(&model.ConversationMember{}).
			Where("conversation_id = ? AND last_read_msg_id >= ?", convID, msgID).
			Count(&readCount)
	}
	store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ?", convID).
		Count(&memberCount)
	return &ReceiptSummary{Receipts: out, ReadCount: readCount, MemberCount: memberCount}, nil
}

// ============ 消息搜索 ============

// SearchMessages 关键词搜索（仅搜索自己参与的会话；convID 为空则全局）
func SearchMessages(ctx context.Context, userID int64, kw string, convID int64, page, size int) ([]model.Message, int64, error) {
	if kw == "" {
		return nil, 0, &errs.Err{Code: 1001, Msg: "请输入搜索关键词"}
	}
	// 用户参与的所有会话
	var myConvs []int64
	store.DB.Model(&model.ConversationMember{}).
		Where("user_id = ?", userID).Pluck("conversation_id", &myConvs)
	if len(myConvs) == 0 {
		return []model.Message{}, 0, nil
	}

	filter := bson.M{
		"conversation_id": bson.M{"$in": myConvs},
		"content":         bson.M{"$regex": regexp.QuoteMeta(kw), "$options": "i"},
		"blocked":         bson.M{"$ne": true}, // 后台屏蔽的消息不出现在搜索结果
	}
	if convID > 0 {
		if !isMember(ctx, convID, userID) {
			return nil, 0, errs.ConvNotFound
		}
		filter["conversation_id"] = convID
	}
	if size <= 0 || size > 100 {
		size = 20
	}
	if page <= 0 {
		page = 1
	}
	total, _ := msgColl().CountDocuments(ctx, filter)
	cur, err := msgColl().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "msg_id", Value: -1}}).
			SetSkip(int64((page-1)*size)).SetLimit(int64(size)))
	if err != nil {
		return nil, 0, err
	}
	var msgs []model.Message
	cur.All(ctx, &msgs)
	return msgs, total, nil
}

// ============ 收藏 ============

func FavoriteAdd(ctx context.Context, userID, convID, msgID int64) error {
	_, err := store.Mongo.Collection("message_favorite").InsertOne(ctx, &model.MessageFavorite{
		UserID: userID, ConversationID: convID, MsgID: msgID, CreatedAt: time.Now(),
	})
	return err
}

func FavoriteList(ctx context.Context, userID int64, limit int64) ([]model.Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	cur, err := store.Mongo.Collection("message_favorite").
		Find(ctx, bson.M{"user_id": userID}, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	var favs []model.MessageFavorite
	if err := cur.All(ctx, &favs); err != nil {
		return nil, err
	}
	var msgs []model.Message
	for _, f := range favs {
		var m model.Message
		if err := msgColl().FindOne(ctx, bson.M{"msg_id": f.MsgID}).Decode(&m); err == nil {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

// ============ 内部辅助 ============

func marshalJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func isMember(ctx context.Context, convID, userID int64) bool {
	var cnt int64
	store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ? AND user_id = ?", convID, userID).Count(&cnt)
	return cnt > 0
}

func memberRole(ctx context.Context, convID, userID int64) int {
	var m model.ConversationMember
	if err := store.DB.Where("conversation_id = ? AND user_id = ?", convID, userID).First(&m).Error; err != nil {
		return 0
	}
	return m.Role
}

// memberCache 会话成员列表短 TTL 缓存：发消息/广播每次都会查一次全量成员，
// 缓存 10s 减少 DB 查询压力。
//
// 同时把成员列表**写透到 Redis 集合** `conv:members:{id}`：gateway 扇出时不再
// 依赖事件体里的全量 UserIDs，而是用「本节点在线 uid ∩ 会话成员」定位接收者
// （事件体从 ~10KB 降到几百 B，见 R-21）。这里的写透就是那份数据的来源。
var memberCache sync.Map // convID(int64) -> memberCacheEntry

type memberCacheEntry struct {
	ids    []int64
	expire time.Time
}

// convMembersKey gateway 侧读取的会话成员集合
func convMembersKey(convID int64) string { return fmt.Sprintf("conv:members:%d", convID) }

func convMemberIDs(ctx context.Context, convID int64) []int64 {
	if v, ok := memberCache.Load(convID); ok {
		if e, ok := v.(memberCacheEntry); ok && time.Now().Before(e.expire) {
			return e.ids
		}
	}
	var ids []int64
	store.DB.Model(&model.ConversationMember{}).
		Where("conversation_id = ?", convID).Pluck("user_id", &ids)
	if ids == nil {
		ids = []int64{}
	}
	// 写透到 Redis（不设 TTL：它是 gateway 扇出的兜底数据源，宁可短暂陈旧
	// 也不能缺失）。成员变更时由 invalidateConvMembers 主动删除重建。
	if store.RDB != nil && len(ids) > 0 {
		members := make([]interface{}, 0, len(ids))
		for _, u := range ids {
			members = append(members, u)
		}
		if err := store.RDB.SAdd(ctx, convMembersKey(convID), members...).Err(); err != nil {
			log.Printf("[member] sync to redis failed conv=%d err=%v", convID, err)
		}
	}
	memberCache.Store(convID, memberCacheEntry{
		ids:    ids,
		expire: time.Now().Add(10 * time.Second),
	})
	return ids
}

// invalidateConvMembers 成员变更（加人/踢人/退群/解散）时失效缓存。
// 修 R-17：旧代码全仓无失效点，新成员最长 10s 收不到消息、被踢的人最长 10s 还能收。
func invalidateConvMembers(ctx context.Context, convID int64) {
	memberCache.Delete(convID)
	if store.RDB != nil {
		if err := store.RDB.Del(ctx, convMembersKey(convID)).Err(); err != nil {
			log.Printf("[member] invalidate redis failed conv=%d err=%v", convID, err)
		}
	}
}

// convInfoCache 会话行（类型/全员禁言）短 TTL 缓存，落库前少一次 MySQL 往返。
var convInfoCache sync.Map // convID(int64) -> convInfoEntry

type convInfoEntry struct {
	conv   *model.Conversation
	expire time.Time
}

func convInfo(ctx context.Context, convID int64) *model.Conversation {
	if v, ok := convInfoCache.Load(convID); ok {
		if e, ok := v.(convInfoEntry); ok && time.Now().Before(e.expire) {
			return e.conv
		}
	}
	var conv model.Conversation
	if err := store.DB.First(&conv, convID).Error; err != nil {
		// 查不到就缓存一个 nil，避免同一条不存在的会话反复打库
		convInfoCache.Store(convID, convInfoEntry{conv: nil, expire: time.Now().Add(10 * time.Second)})
		return nil
	}
	convInfoCache.Store(convID, convInfoEntry{conv: &conv, expire: time.Now().Add(10 * time.Second)})
	return &conv
}

// invalidateConvInfo 会话自身变更（全员禁言/改名/解散）时失效
func invalidateConvInfo(convID int64) { convInfoCache.Delete(convID) }

// sendGroupSystemMsg 群事件系统消息（type=6）：落 Mongo + 实时广播，不写未读数。
// content 为 JSON：{"kind":"invite|join|quit|kick|mute|unmute|muteAllOn|muteAllOff",
// "actor":"操作者昵称","target":"当事成员昵称","minutes":N}
// 客户端按 kind 用本语言词条渲染灰色居中提示条（对齐微信群事件提示）。
func sendGroupSystemMsg(ctx context.Context, convID int64, content string) {
	seq, err := nextSeq(ctx, convID)
	if err != nil {
		// 取号失败（Redis 不可用）→ 宁可丢一条群提示，也不写一条 seq=0 的坏数据
		log.Printf("[group_sys] nextSeq failed conv=%d err=%v", convID, err)
		return
	}
	msg := &model.Message{
		ConversationID: convID,
		MsgID:          id.Next(),
		ClientMsgID:    newUUID(),
		Seq:            seq,
		SenderID:       0, // 系统消息无发送者
		Type:           model.MsgSystem,
		Content:        content,
		Status:         model.MsgStatusNormal,
		CreatedAt:      time.Now(),
	}
	if _, err := msgColl().InsertOne(ctx, msg); err != nil {
		return
	}
	// 事件只带 convId（不再带全量成员，2000 人群从 ~10KB 降到几百 B，见 R-21）
	_ = PublishEvent(ctx, &Event{
		Type:   "message",
		ConvID: convID,
		Data:   marshalJSON(msg),
	})
}

// userName 取用户昵称（群系统消息文案用），查不到返回空串
func userName(userID int64) string {
	var u model.User
	if err := store.DB.First(&u, userID).Error; err != nil {
		return ""
	}
	return u.Nickname
}

// ============ 群文件云盘（功能 A）辅助 ============

// IsConvMember 导出会话成员判定（handler 鉴权复用）
func IsConvMember(ctx context.Context, convID, userID int64) bool {
	return isMember(ctx, convID, userID)
}

// UserNameMap 批量取用户昵称（上传者名展示），返回 id→nickname 映射。
func UserNameMap(ctx context.Context, ids []int64) map[int64]string {
	m := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return m
	}
	var users []model.User
	store.DB.Where("id IN ?", ids).Find(&users)
	for _, u := range users {
		m[u.ID] = u.Nickname
	}
	return m
}

// recordConvFile 由已落库的文件消息构造并写入 ConvFile。
func recordConvFile(ctx context.Context, msg *model.Message) {
	cf, ok := buildConvFile(ctx, msg)
	if !ok {
		return
	}
	if err := repo.InsertConvFile(ctx, cf); err != nil {
		log.Printf("[conv_file] insert failed convID=%d msgID=%d err=%v", msg.ConversationID, msg.MsgID, err)
		return
	}
	// 把 fileId 回写到消息体的 file 元数据里：客户端点聊天里的文件气泡时要拿它
	// 调 GET /api/v1/files/:fileId/preview 做应用内预览。
	// 两处都要写，缺一不可：
	//   1) 内存里的 msg —— 本条消息马上要下发给接收方，带上 fileId 对方才能直接预览；
	//   2) MongoDB 文档 —— 换设备/拉历史时也能拿到 fileId。
	// 老消息（本次改动之前发的）没有 fileId，客户端需降级为外部浏览器打开。
	if msg.File != nil {
		msg.File["fileId"] = cf.FileID
		if _, err := msgColl().UpdateOne(ctx,
			bson.M{"msg_id": msg.MsgID},
			bson.M{"$set": bson.M{"file.fileId": cf.FileID}},
		); err != nil {
			log.Printf("[conv_file] writeback fileId failed msgID=%d err=%v", msg.MsgID, err)
		}
	}
}

// buildConvFile 从消息的 file 元数据构造 ConvFile。
// file 字段键：object(对象名/minio_key)、name、size、mimeType、url（与 /upload 响应一致）。
func buildConvFile(ctx context.Context, msg *model.Message) (*model.ConvFile, bool) {
	f := msg.File
	if f == nil {
		return nil, false
	}
	object, _ := f["object"].(string)
	if object == "" {
		return nil, false
	}
	name, _ := f["name"].(string)
	sizeF, _ := f["size"].(float64)
	size := int64(sizeF)
	mime, _ := f["mimeType"].(string)
	if mime == "" {
		mime, _ = f["mime"].(string)
	}
	bucket := SysConfigString(ctx, "minio_bucket", "")
	if bucket == "" {
		if u, ok := f["url"].(string); ok {
			bucket = parseBucketFromURL(u)
		}
	}
	return &model.ConvFile{
		ConvID:      msg.ConversationID,
		MsgID:       msg.MsgID,
		FileID:      strconv.FormatInt(id.Next(), 10),
		Name:        name,
		Size:        size,
		Mime:        mime,
		Category:    model.FileCategory(mime),
		UploaderID:  msg.SenderID,
		MinioBucket: bucket,
		MinioKey:    object,
	}, true
}

// parseBucketFromURL 从 MinIO 公开 URL（base/bucket/object）解析 bucket。
func parseBucketFromURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(p.Path, "/"), "/")
	if len(parts) >= 2 {
		return parts[0]
	}
	return ""
}

// ============ 消息类型筛选（会话相册/文件/链接页） ============

// FilteredMsg 轻量消息卡（筛选列表用，不携带完整消息体）
type FilteredMsg struct {
	// 同 PinnedMsgBrief.MsgID：string 字段禁用 ,string（会双重引号下发）
	ID        string    `json:"id"` // msgId（雪花字符串）
	Seq       int64     `json:"seq"`
	Type      int       `json:"type"`
	Digest    string    `json:"digest"`        // 内容摘要：文件名/链接标题/文本截断
	URL       string    `json:"url,omitempty"` // 媒体 url（file.url 或 link content.url）
	CreatedAt time.Time `json:"createdAt"`
}

// filterMsgTypes type 参数 → 消息 type 码集合（model/message.go 常量）。
// link 对应 MsgLinkCard=11（url 在 content JSON 里）；普通文本消息即含 URL 也不算 link 卡。
var filterMsgTypes = map[string][]int{
	"image": {model.MsgImage},
	"video": {model.MsgVideo},
	"voice": {model.MsgVoice},
	"file":  {model.MsgFile},
	"link":  {model.MsgLinkCard},
}

// FilterMessages 按会话 + 消息类型筛选历史消息，seq 倒序游标分页
// （与 History 同风格：beforeSeq 之前 limit 条，内部翻转为时间正序返回）。
func FilterMessages(ctx context.Context, userID, convID int64, kind string, beforeSeq, limit int64) ([]FilteredMsg, error) {
	if !isMember(ctx, convID, userID) {
		return nil, errs.ConvNotFound
	}
	types, ok := filterMsgTypes[kind]
	if !ok {
		return nil, errs.ParamError
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	typeIn := bson.A{}
	for _, t := range types {
		typeIn = append(typeIn, t)
	}
	filter := bson.M{"conversation_id": convID, "type": bson.M{"$in": typeIn}, "blocked": bson.M{"$ne": true}}
	if beforeSeq > 0 {
		filter["seq"] = bson.M{"$lt": beforeSeq}
	}
	opts := options.Find().SetSort(bson.D{{Key: "seq", Value: -1}}).SetLimit(limit)
	cur, err := msgColl().Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var msgs []model.Message
	if err := cur.All(ctx, &msgs); err != nil {
		return nil, err
	}
	out := make([]FilteredMsg, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- { // 倒序查 → 时间正序输出
		m := msgs[i]
		card := FilteredMsg{
			ID:        strconv.FormatInt(m.MsgID, 10),
			Seq:       m.Seq,
			Type:      m.Type,
			CreatedAt: m.CreatedAt,
		}
		switch m.Type {
		case model.MsgLinkCard:
			// link 卡：url/标题在 content JSON（{"url","title","description",...}）
			var lc struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			}
			if json.Unmarshal([]byte(m.Content), &lc) == nil {
				card.URL = lc.URL
				card.Digest = lc.Title
			}
		case model.MsgVoice:
			// 语音消息（type=4）：content 是 JSON（{"url","duration","waveform","size"}），
			// 资料页"语音消息"相册只要 url；digest 给个标签，避免回退到原始 JSON 文本。
			var vc struct {
				URL string `json:"url"`
			}
			if json.Unmarshal([]byte(m.Content), &vc) == nil && vc.URL != "" {
				card.URL = vc.URL
			}
			if card.Digest == "" {
				card.Digest = "[语音]"
			}
		case model.MsgE2Text:
			// 端到端加密文本（type=13）：服务端不可解密（§36 定稿），
			// 相册/摘要只给占位标签，内容不透出。
			card.Digest = "[加密消息]"
		default:
			if m.File != nil {
				if u, ok := m.File["url"].(string); ok {
					card.URL = u
				}
				if n, ok := m.File["name"].(string); ok {
					card.Digest = n
				}
			} else if u := strings.TrimSpace(m.Content); u != "" {
				// 图片/视频/语音消息不走 File 字段，content 就是媒体 URL
				//（多图用 | 连接，App 端 sendImage 组装见 chat_page；PC 同契约）。
				// 原实现只认 File → filter 接口对图片消息永远不下发 url，
				// 资料页媒体列表渲染不出图片、digest 还被 truncateDigest
				// 填成原始地址文本（2026-09-15 用户实测截图实锤）。
				card.URL = u
			}
		}
		// content 已作为 url 下发时，不再把它截断成 digest（避免标题显示原始地址）
		if card.Digest == "" && card.URL == "" {
			card.Digest = truncateDigest(m.Content, 50)
		}
		out = append(out, card)
	}
	return out, nil
}

// FilterMessageCount 统计会话内某类媒体消息数（资料页「照片和视频/文件/共享链接/
// 语音消息」角标）。数据源与 FilterMessages 同链路（消息表按 type 计数）——
// 不能用群文件接口的 total：conv_file 只在文件消息落库时写入（repo/conv_file_repo.go
// InsertConvFile 仅 type=3 调用），图片/视频消息从不进集合，计数恒 0。
func FilterMessageCount(ctx context.Context, userID, convID int64, kind string) (int64, error) {
	if !isMember(ctx, convID, userID) {
		return 0, errs.ConvNotFound
	}
	types, ok := filterMsgTypes[kind]
	if !ok {
		return 0, errs.ParamError
	}
	typeIn := bson.A{}
	for _, t := range types {
		typeIn = append(typeIn, t)
	}
	filter := bson.M{"conversation_id": convID, "type": bson.M{"$in": typeIn}, "blocked": bson.M{"$ne": true}}
	return msgColl().CountDocuments(ctx, filter)
}

// truncateDigest 摘要截断（按 rune，避免切断多字节字符）
func truncateDigest(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
