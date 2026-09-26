package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/jwt"
	"github.com/yourcompany/im-server/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// ============ 连接管理 ============

// connWriter 每个连接一个独立写协程 + 缓冲通道，集中处理「消息推送」与「服务端 ping」
// 两条写路径（避免 gorilla 并发写同一 socket 未保护的坑），并对慢客户端做队头阻塞隔离：
// push 入队非阻塞，写协程按序刷出并设置写超时，慢客户端不会阻塞消费协程。
//
// 【本次改动 R-08】缓冲从 256 提到 1024，并把「缓冲满」的处理从**静默丢弃**改成显式策略：
//   - 可丢帧（typing / read / wallet / pong 等）：计数丢弃（丢一条输入状态无伤大雅）
//   - 不可丢帧（message / recall 等）：默认同样丢弃但**打点 + 告警日志**；
//     开关 [SetSlowClientKick] 打开后改为关闭连接，逼客户端重连 → 走补拉，数据不丢。
//
// 为什么踢连接默认关：T01 的补拉机制未经验证前，踢掉慢客户端只会让用户「更频繁地
// 看到连接中」却没有补偿手段。补拉验证通过后再打开（约束：必须能开关）。
type connWriter struct {
	ch       chan []byte
	done     chan struct{}
	once     sync.Once
	conn     *websocket.Conn
	deviceID string        // 连接所属设备号（WS 握手 query deviceId；空=老客户端未带，无法按设备定位）
	dropped  atomic.Uint64 // 累计丢帧数（可观测性）
	kicked   atomic.Bool   // 是否已因慢被踢（避免重复 close）
}

const (
	writeBufSize  = 1024 // 每连接写缓冲（原 256，大群突发时容易打满）
	writeTimeout  = 10 * time.Second
	serverPing    = 30 * time.Second
	eventShardNum = 64  // 本地消费分片数（同会话保序、跨会话并行）
	shardQueueLen = 512 // 每个分片 Worker 的待处理队列长度
)

// WS 握手复核（handleWS 里的 status / token_version 校验）的观测计数器。
//
// 这个复核是 **fail-open** 的（查不到就放行），因此"复核没生效"必须能被看见 ——
// 否则「禁用账号 / 已吊销令牌仍能新开 WS 并收到推送」这件事在线上是完全无声的。
// 三个计数器互斥地覆盖三种结果，看 /ws/stats 的 wsRecheck 即可判断复核是"真在拦"还是"静默跳过"：
//   - skippedNoDb 与 skippedDbErr 都保持 0 → 复核在正常生效（denied 只是历史拦截数，不必大于 0）；
//   - 任一个在增长 → 正在降级，需查网关到 MySQL 的连通性 / 启动顺序。
//
// 注意 skippedDbErr 只统计真正的**查询报错**（连接断开 / 超时 / 表不存在等）。
// gorm.ErrRecordNotFound 虽然也是 error，但语义是"查询成功但没有这一行"= 账号已被物理删除，
// 属于确定性拒绝，计入 denied 而非此处 —— 否则删号会污染这个"DB 抖动"指标，把运维引向错误方向。
var (
	wsRecheckDenied       atomic.Uint64 // 复核执行且拒绝（status/token_version 不匹配，或账号行已不存在）
	wsRecheckSkippedNoDB  atomic.Uint64 // store.DB 为 nil（网关启动时 MySQL 不可达；该进程内不会自愈）
	wsRecheckSkippedDBErr atomic.Uint64 // 查询报错（运行期 DB 抖动/掉线）→ 跳过，线上最常见
)

// kickSlowClients 慢客户端踢连接开关（默认关闭）。
// 由 main 按配置 KICK_SLOW_CLIENTS 设置，见 [SetSlowClientKick]。
var kickSlowClients atomic.Bool

// SetSlowClientKick 打开/关闭「消息帧塞不下就踢连接」策略。
// 关闭时（默认）：不可丢帧塞不下也只丢弃并计数 —— 与旧行为一致，靠补拉兜底。
// 打开时：关闭该连接，客户端重连后走补拉补齐（数据不丢，代价是弱网用户闪断）。
func SetSlowClientKick(on bool) {
	kickSlowClients.Store(on)
	log.Printf("[ws] slow client kick = %v", on)
}

func SlowClientKick() bool { return kickSlowClients.Load() }

// frameKind 帧类型：决定缓冲满时是「可丢」还是「必须送达」
type frameKind uint8

const (
	frameDroppable frameKind = iota // typing / read / wallet / pong 等丢了不影响数据完整性
	frameCritical                   // message / recall 等，丢了 = 漏消息
)

func newConnWriter(conn *websocket.Conn, deviceID string) *connWriter {
	cw := &connWriter{
		ch:       make(chan []byte, writeBufSize),
		done:     make(chan struct{}),
		conn:     conn,
		deviceID: deviceID,
	}
	go cw.run(conn)
	return cw
}

func (cw *connWriter) run(conn *websocket.Conn) {
	ticker := time.NewTicker(serverPing)
	defer ticker.Stop()
	// 注意：done 只由 cw.close()（remove 时）关闭，这里不要 defer close，
	// 否则 remove 先关闭 done 后本协程退出时再次 close 会 panic（重复关闭 channel）。
	for {
		select {
		case frame := <-cw.ch:
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
				conn.Close() // 写失败（慢/死客户端）→ 关连接，读协程随后退出并清理
				return
			}
		case <-ticker.C:
			// 服务端心跳：集中在这里发，与消息推送共用同一写协程，杜绝并发写。
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				conn.Close()
				return
			}
		case <-cw.done:
			return
		}
	}
}

// write 非阻塞写入。绝不在 HTTP/消费协程里阻塞。
//
// 缓冲满时按帧类型处理（旧实现是 `default:` 直接丢，既不计数也不告警）：
//   - 可丢帧：丢，仅计数；
//   - 不可丢帧：计数 + 日志；开关打开时关闭连接（客户端重连 → 补拉 → 数据不丢）。
func (cw *connWriter) write(frame []byte, kind frameKind) {
	select {
	case cw.ch <- frame:
		return
	case <-cw.done:
		return
	default:
	}
	// 缓冲已满
	cw.dropped.Add(1)
	if kind == frameDroppable {
		return
	}
	// 不可丢帧
	if !kickSlowClients.Load() {
		// 开关未开：与旧行为一致（丢弃），但要留痕，方便确认「到底丢了多少」
		if n := cw.dropped.Load(); n == 1 || n%100 == 0 {
			log.Printf("[ws] critical frame dropped (kick disabled) dropped=%d buf=%d", n, writeBufSize)
		}
		return
	}
	// 开关已开：踢掉慢客户端，逼它重连后走补拉（重连补拉由 T01 保证正确）
	if cw.kicked.CompareAndSwap(false, true) {
		log.Printf("[ws] slow client kicked, dropped=%d (client will reconnect and re-sync)", cw.dropped.Load())
		cw.close()
		if cw.conn != nil {
			cw.conn.Close()
		}
	}
}

func (cw *connWriter) close() {
	cw.once.Do(func() { close(cw.done) })
}

// connShard 连接表分片：按 uid 哈希分桶，把一把全局 RWMutex 拆成 64 把，
// 消除大群广播时所有 uid 抢同一把读锁造成的串行（R-01 的一部分）。
type connShard struct {
	mu    sync.RWMutex
	conns map[int64]map[*websocket.Conn]*connWriter
}

type ConnManager struct {
	shards [eventShardNum]*connShard
}

var connMgr = newConnManager()

func newConnManager() *ConnManager {
	m := &ConnManager{}
	for i := range m.shards {
		m.shards[i] = &connShard{conns: make(map[int64]map[*websocket.Conn]*connWriter)}
	}
	return m
}

func (m *ConnManager) shard(uid int64) *connShard {
	return m.shards[uint64(uid)%eventShardNum]
}

func (m *ConnManager) add(uid int64, c *websocket.Conn, deviceID string) *connWriter {
	cw := newConnWriter(c, deviceID)
	s := m.shard(uid)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns[uid] == nil {
		s.conns[uid] = make(map[*websocket.Conn]*connWriter)
	}
	s.conns[uid][c] = cw
	return cw
}

func (m *ConnManager) remove(uid int64, c *websocket.Conn) {
	s := m.shard(uid)
	s.mu.Lock()
	defer s.mu.Unlock()
	if set, ok := s.conns[uid]; ok {
		if cw, ok := set[c]; ok {
			cw.close()
			delete(set, c)
		}
		if len(set) == 0 {
			delete(s.conns, uid)
		}
	}
}

// pushDevice 推送给本节点上某用户「指定设备」的连接（注销设备前先送达 forceLogout 用）。
func (m *ConnManager) pushDevice(uid int64, deviceID string, frame []byte, kind frameKind) {
	if deviceID == "" {
		return
	}
	s := m.shard(uid)
	s.mu.RLock()
	set := s.conns[uid]
	var targets []*connWriter
	for _, cw := range set {
		if cw.deviceID == deviceID {
			targets = append(targets, cw)
		}
	}
	s.mu.RUnlock()
	for _, cw := range targets {
		cw.write(frame, kind)
	}
}

// CloseByDevice 主动断开某用户指定设备的所有连接（注销设备用）。
// 关闭写协程 + 关闭底层 socket（读协程随之退出并走 handleWS 的 defer 清理）。
// 返回关闭的连接数。deviceID 为空时不做任何事（老客户端连接无设备身份，无法定位）。
// 约束：单 gateway 节点（见 shardOf 注释），本地关闭即全量；若未来多节点，
// 此方法只在收到 device.logout 事件的节点生效，需改为广播事件。
func (m *ConnManager) CloseByDevice(uid int64, deviceID string) int {
	if deviceID == "" {
		return 0
	}
	s := m.shard(uid)
	s.mu.Lock()
	set := s.conns[uid]
	var targets []*connWriter
	for c, cw := range set {
		if cw.deviceID == deviceID {
			targets = append(targets, cw)
			delete(set, c)
		}
	}
	if set != nil && len(set) == 0 {
		delete(s.conns, uid)
	}
	s.mu.Unlock()
	for _, cw := range targets {
		cw.close()
		if cw.conn != nil {
			cw.conn.Close()
		}
	}
	return len(targets)
}

// push 推送给本节点上线的某个用户（非阻塞；慢客户端由 connWriter 按帧类型处置）。
//
// 优化：单连接是绝大多数场景（每人 1 条连接），用大小为 1 的栈上数组避免
// 每次调用都 `make` 一个 slice（大群广播时这是每秒上千次的小对象分配）。
func (m *ConnManager) push(uid int64, frame []byte, kind frameKind) {
	s := m.shard(uid)
	s.mu.RLock()
	set := s.conns[uid]
	n := len(set)
	if n == 0 {
		s.mu.RUnlock()
		return
	}
	if n == 1 {
		for _, cw := range set {
			s.mu.RUnlock()
			cw.write(frame, kind)
			return
		}
	}
	writers := make([]*connWriter, 0, n)
	for _, cw := range set {
		writers = append(writers, cw)
	}
	s.mu.RUnlock()
	for _, cw := range writers {
		cw.write(frame, kind)
	}
}

// ============ Redis 事件订阅（api 发布，gateway 推送） ============

// Event 跨服务事件（api → redis channel → gateway → 客户端）
//
// 【本次改动 R-21】不再携带全量 UserIDs：2000 人群每条事件的 userIds 约 8–10KB，
// 多节点时每个节点都要反序列化并遍历全量，只为推本地少数人。
// 改为携带「受众描述」：
//   - ConvID：群/单聊会话 → gateway 用「会话成员 ∩ 本节点在线」扇出
//   - ToUIDs：精确受众（好友申请 / 钱包 / 强制下线）
//   - UserIDs：**仅保留用于滚动发布兼容**（旧 gateway 二进制仍能消费新事件），
//     api 侧不再填充。
type Event struct {
	Type    string          `json:"type"` // message / recall / read / system / typing / call / wallet / friend.* / forceLogout / device.logout(内部)
	ConvID  int64           `json:"convId,omitempty"`
	ToUIDs  []int64         `json:"to,omitempty"`
	Exclude []int64         `json:"exclude,omitempty"`
	Data    json.RawMessage `json:"data"`
	// Deprecated: 仅为兼容滚动发布期间的旧 gateway，新代码请勿填充。
	UserIDs []int64 `json:"userIds,omitempty"`
}

const eventChannel = "im:events"

// PublishEvent 发布事件（api 侧调用，把消息推给在线用户）
func PublishEvent(ctx context.Context, ev *Event) error {
	if store.RDB == nil {
		return nil
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	// 事件发布是「加速通知」，丢了由客户端补拉兜底，但错误必须留痕（旧代码 `_ =` 吞掉）
	if err := store.RDB.Publish(ctx, eventChannel, string(b)).Err(); err != nil {
		log.Printf("[event] publish failed type=%s conv=%d err=%v", ev.Type, ev.ConvID, err)
		return err
	}
	return nil
}

// ---- 会话成员集合（gateway 扇出用）----

var (
	convIdxMu    sync.RWMutex
	convIdxCache = map[int64]convMembersEntry{}
	convIdxTTL   = 30 * time.Second
)

type convMembersEntry struct {
	uids   map[int64]struct{}
	expire time.Time
}

// convMemberSet 会话成员集合（本地缓存 30s，回源 Redis `conv:members:{id}`，
// 由 api 的 convMemberIDs 写透）。取不到时返回 nil，调用方按「无法扇出」处理。
func convMemberSet(ctx context.Context, convID int64) map[int64]struct{} {
	now := time.Now()
	convIdxMu.RLock()
	if e, ok := convIdxCache[convID]; ok && now.Before(e.expire) {
		convIdxMu.RUnlock()
		return e.uids
	}
	convIdxMu.RUnlock()

	if store.RDB == nil {
		return nil
	}
	vals, err := store.RDB.SMembers(ctx, convMembersKey(convID)).Result()
	if err != nil {
		log.Printf("[fanout] load conv members failed conv=%d err=%v", convID, err)
		return nil
	}
	set := make(map[int64]struct{}, len(vals))
	for _, v := range vals {
		uid, err := strconv.ParseInt(v, 10, 64)
		if err != nil || uid <= 0 {
			continue
		}
		set[uid] = struct{}{}
	}
	convIdxMu.Lock()
	convIdxCache[convID] = convMembersEntry{uids: set, expire: now.Add(convIdxTTL)}
	// 顺带清理过期项，避免长期运行后缓存无限增长
	if len(convIdxCache) > 10000 {
		for k, e := range convIdxCache {
			if now.After(e.expire) {
				delete(convIdxCache, k)
			}
		}
	}
	convIdxMu.Unlock()
	return set
}

// invalidateConvIdxCache 让本地会话成员缓存立即失效（成员变更事件到达时调用）
func invalidateConvIdxCache(convID int64) {
	convIdxMu.Lock()
	delete(convIdxCache, convID)
	convIdxMu.Unlock()
}

// ---- 消费分片：同会话保序、跨会话并行 ----

// shardOf 事件 → 本地消费分片。同一 convID 恒定落在同一分片（保序），
// 不同 convID 分散到 64 个分片（并行）。
// Redis 仍用单 channel：因为我们**不引入消费者组**（单 gateway 节点，约束 2），
// 分片只发生在 gateway 进程内 —— 62 条额外订阅连接换不来收益，反而增加 Redis 负担。
func shardOf(ev *Event) int {
	if ev.ConvID > 0 {
		return int(uint64(ev.ConvID) % eventShardNum)
	}
	if len(ev.ToUIDs) > 0 {
		return int(uint64(ev.ToUIDs[0]) % eventShardNum)
	}
	return 0
}

var (
	droppedEvents atomic.Uint64 // 分片队列满导致的丢弃（可观测性）
)

// StartEventConsumer 启动事件消费（gateway 进程启动时调用）。
//
// 【本次改动 R-19】整个消费循环加 recover：旧实现一次 panic（如 ev.Data 为 nil 时
// 的 marshal）就带走整个 goroutine，gateway 从此**静默停止推送**且不告警。
// 现在 panic 后打印堆栈并自动重建订阅（3s 退避）。
func StartEventConsumer(ctx context.Context) {
	go runEventConsumer(ctx)
	log.Printf("event consumer started (shards=%d, queue=%d)", eventShardNum, shardQueueLen)
}

func runEventConsumer(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[event] consumer PANIC: %v\n%s", r, debug.Stack())
			time.Sleep(3 * time.Second)
			if ctx.Err() == nil {
				go runEventConsumer(ctx) // 自愈：重建订阅
			}
		}
	}()
	consumeEvents(ctx)
}

func consumeEvents(ctx context.Context) {
	if store.RDB == nil {
		log.Printf("[event] redis unavailable, consumer stopped")
		return
	}
	sub := store.RDB.Subscribe(ctx, eventChannel)
	defer sub.Close()

	// 每个分片一个 worker：同会话事件串行（保序），跨会话并行
	queues := make([]chan *Event, eventShardNum)
	workerWG := sync.WaitGroup{}
	for i := 0; i < eventShardNum; i++ {
		queues[i] = make(chan *Event, shardQueueLen)
		q := queues[i]
		workerWG.Add(1)
		go func(idx int) {
			defer workerWG.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[event] shard-%d worker PANIC: %v\n%s", idx, r, debug.Stack())
				}
			}()
			for ev := range q {
				dispatchEvent(ctx, ev)
			}
		}(i)
	}
	// 分发完成后关闭队列，让 worker 退出（正常路径下不会走到）
	defer func() {
		for _, q := range queues {
			close(q)
		}
		workerWG.Wait()
	}()

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var ev Event
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				continue
			}
			q := queues[shardOf(&ev)]
			select {
			case q <- &ev:
			default:
				// 队列满：分片内积压过大。丢事件不会丢消息（客户端补拉兜底），
				// 但必须计数 + 留痕，作为扩容/调优的依据。
				if n := droppedEvents.Add(1); n == 1 || n%100 == 0 {
					log.Printf("[event] shard queue full, dropped=%d (type=%s conv=%d)",
						n, ev.Type, ev.ConvID)
				}
			}
		}
	}
}

// dispatchEvent 把一条事件扇出给本节点在线用户。
//
// 【本次改动 R-01】json.Marshal 提到 uid 循环**之外**：旧实现在循环体内 marshal，
// 2000 人群 = 每条消息 2000 次相同内容的 marshal（这是 gateway CPU 的最大开销）。
func dispatchEvent(ctx context.Context, ev *Event) {
	// 内部事件：成员变更 → 只让本节点失效会话成员缓存，不下发给客户端
	// （客户端通过群系统消息 + 补拉感知，见 conversation.go 的调用点）
	if ev.Type == "member.changed" {
		if ev.ConvID > 0 {
			invalidateConvIdxCache(ev.ConvID)
		}
		return
	}
	// 内部事件：注销设备 → 先给该设备推 forceLogout 帧，再主动断开其连接
	// （api 进程没有 connMgr，连接在本进程；见 service/device.go LogoutDevice）
	if ev.Type == "device.logout" {
		if len(ev.ToUIDs) == 1 && len(ev.Data) > 0 {
			var d struct {
				DeviceID string `json:"deviceId"`
			}
			if json.Unmarshal(ev.Data, &d) == nil && d.DeviceID != "" {
				frame, _ := json.Marshal(map[string]interface{}{
					"type": "forceLogout",
					"data": map[string]interface{}{"reason": "device_logged_out", "deviceId": d.DeviceID},
				})
				connMgr.pushDevice(ev.ToUIDs[0], d.DeviceID, frame, frameCritical)
				// 给写协程一点时间把 forceLogout 帧刷出再断开（否则 select 可能先收到 done）
				go func(uid int64, deviceID string) {
					time.Sleep(200 * time.Millisecond)
					if n := connMgr.CloseByDevice(uid, deviceID); n > 0 {
						log.Printf("[device] closed ws uid=%d device=%s conns=%d", uid, deviceID, n)
					}
				}(ev.ToUIDs[0], d.DeviceID)
			}
		}
		return
	}
	kind := frameKindOf(ev.Type)
	frame, err := json.Marshal(map[string]interface{}{
		"type": ev.Type,
		"data": ev.Data,
	})
	if err != nil {
		return
	}

	// 精确受众与会话受众同时存在时才需要去重；绝大多数事件只有一个来源，
	// 这里省掉每次事件都建一个 2000 项的 map（大群扇出的隐性开销）。
	needDedup := len(ev.ToUIDs) > 0 || len(ev.UserIDs) > 0

	excluded := make(map[int64]struct{}, len(ev.Exclude))
	for _, uid := range ev.Exclude {
		excluded[uid] = struct{}{}
	}
	hasExclude := len(excluded) > 0

	var seen map[int64]struct{}
	if needDedup {
		seen = make(map[int64]struct{}, 8)
	}
	// push 内部会先查分片连接表，未在线直接返回，无需在这里再判一次在线
	push := func(uid int64) {
		if uid <= 0 {
			return
		}
		if hasExclude {
			if _, ok := excluded[uid]; ok {
				return
			}
		}
		if seen != nil {
			if _, ok := seen[uid]; ok {
				return
			}
			seen[uid] = struct{}{}
		}
		connMgr.push(uid, frame, kind)
	}

	// 1) 会话维度扇出：本节点在线 ∩ 会话成员（O(本节点在线成员)，而非 O(群成员)）
	if ev.ConvID > 0 {
		if set := convMemberSet(ctx, ev.ConvID); set != nil {
			for uid := range set {
				push(uid)
			}
		} else {
			log.Printf("[fanout] conv member set unavailable conv=%d type=%s (members will fall back to sync)",
				ev.ConvID, ev.Type)
		}
	}

	// 2) 精确受众（好友/钱包/强制下线等非会话事件）
	for _, uid := range ev.ToUIDs {
		push(uid)
	}

	// 3) 兼容旧事件体（滚动发布期间 api 老版本仍带 userIds）
	for _, uid := range ev.UserIDs {
		push(uid)
	}
}

// frameKindOf 帧是否可丢：只有「丢了会导致漏消息」的才算 critical
func frameKindOf(typ string) frameKind {
	switch typ {
	case "message", "recall", "forceLogout", "friend.request", "friend.accepted", "friend.deleted":
		return frameCritical
	default:
		// typing / read / wallet / call 等丢了不影响消息完整性
		return frameDroppable
	}
}

// ============ WebSocket 处理 ============

var upgrader = websocket.Upgrader{
	// CheckOrigin 实际在 RegisterWSRoutes 里按 cfg 的 WSAllowedOrigins 覆盖；
	// 这里的默认值保持拒绝跨站，避免漏配时被无意使用。
	CheckOrigin: func(r *http.Request) bool { return false },
}

// WsOriginAllowed 校验 WebSocket 升级请求的 Origin 是否允许。
// 规则（顺序敏感）：
//  1. 没有 Origin 头 → 放行（原生 App、Go 压测工具都不带 Origin）；
//  2. Origin 命中白名单（大小写不敏感精确匹配；白名单项以 "*." 开头则按域名后缀匹配，
//     例如 *.example.com 匹配 https://a.example.com —— 仅匹配子域，裸域需单独列出）→ 放行；
//  3. Origin 的 host 为 localhost / 127.0.0.1 / [::1]（任意端口）→ 放行
//     （PC Electron 从本地静态服务加载，其 Origin 就是 http://127.0.0.1:端口）；
//  4. 其余 → 拒绝，并打日志。
func WsOriginAllowed(cfg *config.Config, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	origin = strings.TrimSpace(origin)
	lower := strings.ToLower(origin)
	for _, allow := range cfg.WSAllowedOrigins {
		a := strings.ToLower(strings.TrimSpace(allow))
		if a == "" {
			continue
		}
		if strings.HasPrefix(a, "*.") {
			// 域名后缀匹配：*.example.com 匹配 https://a.example.com（仅子域）
			suffix := a[2:]
			if host, err := originHost(origin); err == nil {
				if strings.HasSuffix(strings.ToLower(host), "."+suffix) {
					return true
				}
			}
			continue
		}
		if lower == a {
			return true
		}
	}
	// 本机来源放行（PC Electron）
	if host, err := originHost(origin); err == nil {
		h := strings.ToLower(host)
		if h == "localhost" || h == "127.0.0.1" || h == "[::1]" || h == "::1" {
			return true
		}
	}
	log.Printf("[ws] origin rejected: origin=%q remote=%s", origin, r.RemoteAddr)
	return false
}

// originHost 从 Origin 中解析 host（可含端口），例如 https://a.example.com:8080 → a.example.com
func originHost(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return "", errors.New("invalid origin")
	}
	return u.Hostname(), nil
}

// RegisterWSRoutes 注册 WebSocket 路由（网关）
func RegisterWSRoutes(r *gin.Engine, cfg *config.Config) {
	// 在注册路由前设置 CheckOrigin（upgrader 是包级变量），handleWS 里不再重复赋值
	upgrader.CheckOrigin = func(req *http.Request) bool { return WsOriginAllowed(cfg, req) }
	r.GET("/ws", func(c *gin.Context) { handleWS(c, cfg) })
	// 运维自检：当前丢帧/丢事件计数与踢连接开关状态
	r.GET("/ws/stats", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0, "message": "ok",
			"data": gin.H{
				"droppedFrames":   totalDroppedFrames(),
				"droppedEvents":   droppedEvents.Load(),
				"kickSlowClients": SlowClientKick(),
				"shards":          eventShardNum,
				// WS 握手复核的真实生效情况（fail-open，所以必须可观测）：
				//   denied       = 复核执行且拦截的次数（>0 说明复核确实在工作）
				//   skippedNoDb  = store.DB 为 nil → 跳过（网关启动时 MySQL 不可达，进程内不会自愈）
				//   skippedDbErr = 查询报错 → 跳过（运行期 DB 抖动/掉线，线上最常见）
				// 后两者任一持续增长，说明「改密码 / 被禁用即断 WS」当前不生效，需查 MySQL 连通性。
				"wsRecheck": gin.H{
					"denied":       wsRecheckDenied.Load(),
					"skippedNoDb":  wsRecheckSkippedNoDB.Load(),
					"skippedDbErr": wsRecheckSkippedDBErr.Load(),
				},
			},
		})
	})
}

// ClientFrame 客户端 → 服务端帧
type ClientFrame struct {
	Action string          `json:"action"` // ping / typing
	Data   json.RawMessage `json:"data"`
}

func handleWS(c *gin.Context, cfg *config.Config) {
	// 鉴权：?token=JWT
	token := c.Query("token")
	claims, err := jwt.Parse(cfg.JWTSecret, token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 1002, "message": "未登录或登录过期"})
		return
	}
	// 账号状态 + 令牌版本复核：禁用账号、改密码/被重置密码后已吊销的 token 无法新开 WS 连接。
	// 判定分三类。除 `store.DB == nil` 这一状态外，判定语义与 middleware.Auth 一致
	// （该状态两侧**有意不等价**：WS 侧放行，REST 侧因 api 启动即 Fatal 而走 fail-closed，
	// 详见 middleware/auth.go 中 "关于 store.DB 为 nil" 那段），因此改一处务必核对另一处：
	//   1) 查到用户且 status/token_version 不匹配 → 拒绝；
	//   2) 查询成功但无此行（gorm.ErrRecordNotFound）→ 同样拒绝。
	//      它不是 DB 故障而是"账号已被物理删除"（如后台 AdminDataClear scope=users，
	//      见 service/admin.go:447）。放行会让被删用户凭旧 token 开 WS，成为幽灵连接。
	//   3) 真正的查询报错 / store.DB 为 nil → 放行（宁可放行也不能把全体 WS 挡在门外）：
	//      - 查询报错：与 middleware.Auth 的"抖动放行"语义一致；
	//      - store.DB 为 nil：网关启动时 MySQL 不可达，InitMySQLNoMigrate 失败只告警不退出
	//        （见 cmd/gateway/main.go），且该进程内**不会**自动恢复，复核会一直跳过。
	//        这不是安全回归（改动前 WS 本就不做复核），但意味着"改密码/被禁用即断线"对 WS 信道
	//        不生效；REST 侧 middleware.Auth 仍会对每个 API 请求复核，授权判断没有全失守。
	//        该状态由 /ws/stats 的 wsRecheck.skippedNoDb / skippedDbErr 暴露（见文件顶部的计数器注释），
	//        便于运维发现；根治办法是保证网关启动时 MySQL 已就绪。
	// 这里必须短路：对 nil *gorm.DB 调 Select 会 panic，被 gin.Recovery 转成 500，
	// 后果是「所有 WS 握手失败」，比不做复核严重得多。
	if store.DB != nil {
		var u model.User
		err := store.DB.Select("status", "token_version").Where("id = ?", claims.UserID).First(&u).Error
		if errors.Is(err, gorm.ErrRecordNotFound) ||
			(err == nil && (u.Status != model.StatusNormal || u.TokenVersion != claims.Ver)) {
			wsRecheckDenied.Add(1)
			c.JSON(http.StatusUnauthorized, gin.H{"code": 1002, "message": "未登录或登录过期"})
			return
		}
		if err != nil {
			if n := wsRecheckSkippedDBErr.Add(1); n == 1 || n%100 == 0 {
				log.Printf("[ws] 令牌复核查询失败，本次放行（累计 %d 次）: uid=%d err=%v", n, claims.UserID, err)
			}
		}
	} else if n := wsRecheckSkippedNoDB.Add(1); n == 1 || n%100 == 0 {
		log.Printf("[ws] store.DB 为 nil，跳过令牌复核（累计 %d 次）", n)
	}
	uid := claims.UserID

	// 设备级令牌失效（二期落地，2026-09-24）：access token 现已携带 device_id（见 jwt.Generate）。
	// 若该设备的 refresh 槽位已被吊销（LogoutDevice / 注销单台设备 / 改密码全量登出），
	// 即使 access token 未过期也拒绝新开 WS —— 否则被注销设备的 WS 会因 access token 仍有效
	// 而无限重连、在「登录设备」里始终显示在线且收得到推送。
	// 仅当 token 带 device_id 时校验（老 token 无 did 放行，兼容存量会话）；Redis 异常 fail-open。
	if claims.DeviceID != "" && store.RDB != nil {
		if ok := DeviceHasSlot(c.Request.Context(), uid, claims.DeviceID); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 1002, "message": "设备已注销"})
			return
		}
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// 在线状态注册：按设备类型存集合（多端同时在线互不覆盖）
	// 前端 WS 连接带 deviceType 参数（1 Android/2 iOS/3 Web/4 Windows/5 macOS）
	dt := c.Query("deviceType")
	if dt == "" {
		dt = "3" // 默认 Web
	}
	// 设备身份：活跃会话列表 / 注销单台设备依赖 deviceId 定位连接。
	// 老客户端不带 deviceId（query 可选）→ 连接无设备身份，仅保留旧的平台名行为。
	deviceID := strings.TrimSpace(c.Query("deviceId"))
	deviceName := deviceNameOf(dt)
	onlineKey := "online:" + stringInt64(uid)
	markOnline(c.Request.Context(), onlineKey, deviceName, cfg.NodeID)
	// 需求8：记录客户端 IP（带设备前缀，多端各自一个 IP；格式 device:ip）
	clientIP := c.ClientIP()
	if clientIP == "" {
		clientIP = "unknown"
	}
	store.RDB.Set(c.Request.Context(), onlineKey+":ip:"+deviceName, clientIP, 90*time.Second)
	// 设备级登记：onlinedev:{uid}（deviceId→平台名，90s 心跳续期）+ device 行活跃信息落库。
	// 这是 device.last_active_at / last_ip 的真实更新点（登录/刷新 token 也会写，见 auth.issueTokens）。
	if deviceID != "" {
		store.RDB.HSet(c.Request.Context(), onlineDevKey(uid), deviceID, deviceName)
		store.RDB.Expire(c.Request.Context(), onlineDevKey(uid), 90*time.Second)
		store.DB.Model(&model.Device{}).
			Where("user_id = ? AND device_id = ?", uid, deviceID).
			Updates(map[string]interface{}{
				"status":         1,
				"last_active_at": time.Now(),
				"last_ip":        clientIP,
			})
	}
	cw := connMgr.add(uid, conn, deviceID)
	defer func() {
		connMgr.remove(uid, conn)
		clearOnline(c.Request.Context(), onlineKey, deviceName)
		if deviceID != "" {
			store.RDB.HDel(c.Request.Context(), onlineDevKey(uid), deviceID)
		}
	}()

	// 心跳
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	// 心跳 ping 统一由 connWriter 的写协程发送（见 newConnWriter），这里不再另起协程，
	// 避免与消息推送并发写同一 socket（gorilla websocket 未保护并发写）。

	log.Printf("ws connected uid=%d node=%s device=%s deviceId=%s", uid, cfg.NodeID, deviceName, deviceID)
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var frame ClientFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			continue
		}
		switch frame.Action {
		case "ping":
			// 心跳续期在线状态
			markOnline(c.Request.Context(), onlineKey, deviceName, cfg.NodeID)
			// 需求8：IP 键同样要随心跳续期——此前只在建连时写一次（90s TTL），对方连上
			// 90 秒后 IP 键过期 → 会话列表 peerOnlineIp 变空，PC 聊天标题栏
			// 「设备 · 在线 · IP」的 IP 闪一下就消失（2026-09-24 修复）。
			store.RDB.Set(c.Request.Context(), onlineKey+":ip:"+deviceName, clientIP, 90*time.Second)
			if deviceID != "" {
				store.RDB.HSet(c.Request.Context(), onlineDevKey(uid), deviceID, deviceName)
				store.RDB.Expire(c.Request.Context(), onlineDevKey(uid), 90*time.Second)
			}
			cw.write([]byte(`{"type":"pong"}`), frameDroppable)
		case "typing":
			// 转发输入状态。旧实现不带任何受众（遍历 0 个用户空转，R-25），
			// 这里把 conversationId 解析出来作为扇出维度。
			// 隐私开关：发送者关闭「显示输入状态」时直接丢弃该帧（不转发给任何人）；
			// Exclude 排除发送者自己——否则 typing 事件会按会话成员集回弹给本人。
			if UserFlagCached(c.Request.Context(), uid, "typing_enabled") == 1 {
				_ = PublishEvent(c.Request.Context(), &Event{
					Type:    "typing",
					ConvID:  convIDOfRaw(frame.Data),
					Exclude: []int64{uid},
					Data:    frame.Data,
				})
			}
		}
	}
}

// convIDOfRaw 从客户端帧 data 里取 conversationId（字符串或数字都兼容）
func convIDOfRaw(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var m struct {
		ConversationID json.RawMessage `json:"conversationId"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || len(m.ConversationID) == 0 {
		return 0
	}
	s := trimQuotes(string(m.ConversationID))
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// totalDroppedFrames 汇总所有连接的丢帧数（/ws/stats 用）。
// 只在被调用时遍历一次分片，不放进热路径。
func totalDroppedFrames() uint64 {
	var total uint64
	for i := range connMgr.shards {
		s := connMgr.shards[i]
		s.mu.RLock()
		for _, set := range s.conns {
			for _, cw := range set {
				total += cw.dropped.Load()
			}
		}
		s.mu.RUnlock()
	}
	return total
}

func deviceNameOf(dt string) string {
	switch dt {
	case "1":
		return "android"
	case "2":
		return "ios"
	case "3":
		return "web"
	case "4":
		return "windows"
	case "5":
		return "macos"
	default:
		return "web"
	}
}

// markOnline 把设备加入在线集合（Redis Set：online:{uid}）
func markOnline(ctx context.Context, key, deviceName, nodeID string) {
	store.RDB.SAdd(ctx, key, deviceName)
	store.RDB.Expire(ctx, key, 90*time.Second)
}

// clearOnline 连接断开时把设备从集合移除
func clearOnline(ctx context.Context, key, deviceName string) {
	store.RDB.SRem(ctx, key, deviceName)
}

// IsUserOnline 查询用户是否在线（任意设备），返回在线设备名列表
func IsUserOnline(ctx context.Context, uid int64) (bool, []string) {
	key := "online:" + stringInt64(uid)
	devices, err := store.RDB.SMembers(ctx, key).Result()
	if err != nil || len(devices) == 0 {
		return false, nil
	}
	return true, devices
}

// OnlineIPs 查询用户在线设备的 IP（key online:{uid}:ip:{device}，device 为设备名）
func OnlineIPs(ctx context.Context, uid int64) []string {
	key := "online:" + stringInt64(uid)
	devices, err := store.RDB.SMembers(ctx, key).Result()
	if err != nil || len(devices) == 0 {
		return nil
	}
	ips := []string{}
	for _, d := range devices {
		ip, err := store.RDB.Get(ctx, key+":ip:"+d).Result()
		if err == nil && ip != "" {
			ips = append(ips, ip)
		}
	}
	return ips
}

// OnlineDeviceZh 设备名 → 中文展示（手机在线/H5在线/电脑在线等）
func OnlineDeviceZh(devices []string) string {
	if len(devices) == 0 {
		return ""
	}
	for _, d := range devices {
		switch d {
		case "ios", "android":
			return "手机在线"
		case "web":
			return "H5在线"
		case "windows", "macos":
			return "电脑在线"
		}
	}
	return ""
}

func stringInt64(v int64) string {
	b := make([]byte, 0, 20)
	return string(appendInt64(b, v))
}

func appendInt64(b []byte, v int64) []byte {
	if v == 0 {
		return append(b, '0')
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var tmp [20]byte
	i := len(tmp)
	for v > 0 {
		i--
		tmp[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		tmp[i] = '-'
	}
	return append(b, tmp[i:]...)
}

// ============ 会话成员广播（供 conversation.go 成员变更时调用） ============

// PublishMemberChanged 成员变更后广播：让 gateway 失效本地成员缓存（修 R-17）。
// 这是一条「内部事件」，不推给客户端（客户端通过群系统消息 + 补拉感知）。
func PublishMemberChanged(ctx context.Context, convID int64) {
	invalidateConvIdxCache(convID) // 本进程（api 与 gateway 同进程时也生效）
	if store.RDB == nil {
		return
	}
	b, _ := json.Marshal(&Event{
		Type:   "member.changed",
		ConvID: convID,
		Data:   json.RawMessage(fmt.Sprintf(`{"conversationId":"%d"}`, convID)),
	})
	if err := store.RDB.Publish(ctx, eventChannel, string(b)).Err(); err != nil {
		log.Printf("[event] publish member.changed failed conv=%d err=%v", convID, err)
	}
}
