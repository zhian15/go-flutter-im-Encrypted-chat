// Package main 是 IM 服务端的压力测试工具。
//
// 存在意义：消息链路的四条性质 —— 不丢 / 不重 / 不乱 / 不卡 —— 不能靠"改代码的人
// 自己说"来证明，必须有一个外部工具能在真实负载下把它们测出来。而且必须覆盖
// 2000 人群这种扇出量级：单聊跑通不代表大群不丢消息，两者的瓶颈完全不同。
//
// 一条硬约束：**不复用被测代码的业务逻辑**（唯一例外是 internal/pkg/jwt，只为离线
// 签发令牌、免去上千次登录），否则就是拿被测实现去验证它自己。所有观测都走公开
// HTTP API + WebSocket，与真实客户端走的是同一条路径。
//
// 用法见 usage()，详细说明见同目录 README.md。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ====================== 响应信封 ======================

// envelope 服务端统一响应信封。
//
// 关键：**业务错误也返回 HTTP 200**，失败信息在 code 里（限流 429、非成员 4001、
// 群满 4002 …）。所以本工具所有判断都只看 code —— 只看 HTTP 状态码会把失败当成功，
// App 端历史上正是踩过这个坑（见 im-app 的 _unwrapSend）。
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// apiErr 业务错误（code != 0）
type apiErr struct {
	Code int
	Msg  string
}

func (e *apiErr) Error() string { return fmt.Sprintf("code=%d %s", e.Code, e.Msg) }

// 已知错误码，与服务端 internal/pkg/errs 对齐
const (
	codeTooManyRequests = 429  // 发送限流（per-user 20/s 突发50、per-conv 5/s 突发20）
	codeConvNotFound    = 4001 // 会话不存在或非成员
	codeGroupFull       = 4002 // 群人数已达上限
)

func asAPIErr(err error) (*apiErr, bool) {
	var e *apiErr
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// ====================== HTTP ======================

type apiClient struct {
	base string
	hc   *http.Client
}

func newAPIClient(base string, timeout time.Duration) *apiClient {
	return &apiClient{base: strings.TrimRight(base, "/"), hc: &http.Client{Timeout: timeout}}
}

// call 发一次请求并解开信封；code != 0 时返回 *apiErr。
// body 为 nil 时发无体请求；query 为 nil 时无查询串。
func (c *apiClient) call(method, path, token string, body interface{}, query map[string]string) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	u := c.base + path
	if len(query) > 0 {
		vs := url.Values{}
		for k, v := range query {
			vs.Set(k, v)
		}
		u += "?" + vs.Encode()
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("响应不是合法 JSON（HTTP %d）：%.200s", resp.StatusCode, string(raw))
	}
	if env.Code != 0 {
		return nil, &apiErr{Code: env.Code, Msg: env.Message}
	}
	return env.Data, nil
}

// ====================== 会话文件（压测靶场）======================

// sessionUser 一个压测账号；token 是后续所有操作的凭据。
type sessionUser struct {
	ID      string `json:"id"`
	Account string `json:"account"`
	Token   string `json:"token"`
}

// sessionFile 是 seed 的产物：一座可反复复用的压测靶场。
//
// 之所以要落盘复用：注册接口有 IP 级限流，2000 个账号不可能每次压测都重建；
// 而且 token 也免去了每次跑压测都登录一遍的开销。
type sessionFile struct {
	Base   string        `json:"base"`
	WS     string        `json:"ws"`
	ConvID string        `json:"convId"`
	Users  []sessionUser `json:"users"`
}

func loadSession(path string) (*sessionFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取会话文件失败（先执行 seed）：%w", err)
	}
	var s sessionFile
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("解析会话文件失败：%w", err)
	}
	if len(s.Users) == 0 {
		return nil, errors.New("会话文件里没有任何账号，请重新执行 seed")
	}
	if s.ConvID == "" {
		return nil, errors.New("会话文件缺少 convId，请重新执行 seed")
	}
	return &s, nil
}

// saveSession 写会话文件。权限 0600：里面有可直接冒充用户的 access token。
func saveSession(path string, s *sessionFile) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ====================== 压测消息体 ======================

// loadPayload 把发送时刻与「全局唯一槽位」嵌进消息 content。
//
// 这是本工具能算出"真正端到端延迟"的关键：接收端拿到 content 里的 t 与自己收到
// 的时刻相减，就是一次完整的 HTTP 发送 → 落库 → 扇出 → WS 投递的耗时，
// **完全不需要改服务端埋点**，也就不存在"指标本身被改坏"的可能。
//
// i 是「全局唯一槽位」：发送前由 atomic.Int64 分配，0 起全局递增。用全局槽位而不是
// (发送者下标, 自增计数) 复合下标，是为了让位图大小只与"总消息量"相关 ——
// 否则 2000 个发送者时，复合下标方案会按 senders × perSenderCap 预分配，
// 一下子就吃掉几百 MB，工具自己先把内存打爆。
//
// r 是「runStamp」：本次压测的唯一标识。**这是安全线**：槽位 i 每次运行都从 0 重新计数，
// 而会话 ID 在多次运行间复用，若不区分 run，上一次跑留在同一群里的消息会带着一模一样的
// 槽位号与本轮"碰撞"，把本轮真丢的消息"补"成覆盖率 100%（假的全绿）。带上 run 后，
// 旧消息的 R==0（缺字段）永远不会等于真实 runStamp，可被稳定排除，无需迁移。
type loadPayload struct {
	T int64 `json:"t"` // 发送时刻（Unix 毫秒）
	S int   `json:"s"` // 发送者下标（用于定位是哪个发送者/用户发的）
	I int64 `json:"i"` // 全局唯一槽位（0 起，由原子分配器分配；用于判丢/判重）
	R int64 `json:"r"` // runStamp：本次压测的唯一标识，用于排除"上一次跑的残留消息"
}

func encodePayload(sender int, slot int64, run int64) string {
	b, _ := json.Marshal(loadPayload{T: time.Now().UnixMilli(), S: sender, I: slot, R: run})
	return string(b)
}

func decodePayload(content string) (loadPayload, bool) {
	var p loadPayload
	if err := json.Unmarshal([]byte(content), &p); err != nil {
		return p, false
	}
	return p, p.T > 0
}

// ====================== 统计 ======================

// latHistogram 毫秒级延迟直方图。
//
// 为什么不是"把每个样本存下来再排序"：2000 个客户端 × 几千条消息 = 上千万个样本，
// float64 切片要几百 MB —— 压测工具自己先把内存打爆就失去意义了。
// 1ms 分辨率的直方图对延迟分布完全够用，且内存恒定。
//
// 分位数的意义：消息体验由"最慢的那部分"决定。平均 80ms 但 p99 3s 的系统，
// 用户感受就是"老是卡"，而平均值会把尾部完全抹平。
type latHistogram struct {
	mu      sync.Mutex
	buckets []uint64 // 下标 = 毫秒
	total   uint64
	sumMS   float64
	maxMS   float64
}

func newLatHistogram(maxMS int) *latHistogram {
	if maxMS < 100 {
		maxMS = 100
	}
	return &latHistogram{buckets: make([]uint64, maxMS+1)}
}

func (h *latHistogram) add(ms float64) {
	if ms < 0 {
		ms = 0
	}
	idx := int(ms + 0.5)
	h.mu.Lock()
	if idx > len(h.buckets)-1 {
		idx = len(h.buckets) - 1 // 溢出归入最后一桶
	}
	h.buckets[idx]++
	h.total++
	h.sumMS += ms
	if ms > h.maxMS {
		h.maxMS = ms
	}
	h.mu.Unlock()
}

func (h *latHistogram) summary() (p50, p90, p99, max, avg float64, n uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total == 0 {
		return
	}
	n = h.total
	max = h.maxMS
	avg = h.sumMS / float64(h.total)
	p50 = h.quantileLocked(50)
	p90 = h.quantileLocked(90)
	p99 = h.quantileLocked(99)
	return
}

func (h *latHistogram) quantileLocked(p float64) float64 {
	target := uint64(float64(h.total) * p / 100)
	if target == 0 {
		target = 1
	}
	var cum uint64
	for i, c := range h.buckets {
		cum += c
		if cum >= target {
			return float64(i)
		}
	}
	return float64(len(h.buckets) - 1)
}

// codeCounter 按错误码分组计数（并发安全）
type codeCounter struct {
	mu sync.Mutex
	m  map[int]int
}

func newCodeCounter() *codeCounter { return &codeCounter{m: map[int]int{}} }

func (c *codeCounter) add(code int) {
	c.mu.Lock()
	c.m[code]++
	c.mu.Unlock()
}

func (c *codeCounter) snapshot() map[int]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[int]int, len(c.m))
	for k, v := range c.m {
		out[k] = v
	}
	return out
}

// sortedCodes 错误码升序输出，保证多次运行结果可逐行 diff
func sortedCodes(m map[int]int) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// ====================== 位图（判丢/判重）======================

// 位图用整数下标寻址，避免 map[string]bool：2000 个接收端 × 几千条消息，
// 用字符串 map 会直接吃掉几百 MB，位图每个客户端只要几百字节。

func bitmapSet(bm []uint64, idx int) {
	if idx < 0 || idx/64 >= len(bm) {
		return
	}
	bm[idx/64] |= 1 << uint(idx%64)
}

func bitmapHas(bm []uint64, idx int) bool {
	if idx < 0 || idx/64 >= len(bm) {
		return false
	}
	return bm[idx/64]&(1<<uint(idx%64)) != 0
}

func bitmapPopcount(bm []uint64) int64 {
	var n int64
	for _, w := range bm {
		n += int64(bits.OnesCount64(w))
	}
	return n
}

// bitmapAndPopcount 两个位图都为 1 的位数（= 真正被收到的消息数）
func bitmapAndPopcount(a, b []uint64) int64 {
	var n int64
	for i := range a {
		if i >= len(b) {
			break
		}
		n += int64(bits.OnesCount64(a[i] & b[i]))
	}
	return n
}

// ====================== 运维探测 ======================

// originOf 从 api / ws 地址还原出主机源。
// api 默认 http://127.0.0.1:8080/api/v1，ws 默认 ws://127.0.0.1:8080/ws，
// 而 /health 与 /ws/stats 都注册在根路径上，所以必须单独拼。
func originOf(u string) string {
	s := u
	switch {
	case strings.HasPrefix(s, "ws://"):
		s = "http://" + s[len("ws://"):]
	case strings.HasPrefix(s, "wss://"):
		s = "https://" + s[len("wss://"):]
	}
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return s[:i+3] + rest[:j]
		}
	}
	return s
}

// probeOps 打印 /health 与 /ws/stats 的当前值。
//
// /ws/stats 是判断"消息到底丢在哪一层"的关键：droppedFrames 涨说明是 WS 写缓冲
// 塞不下被主动丢帧，droppedEvents 涨说明是事件消费侧被丢，两者都不涨却仍丢消息，
// 那问题就在客户端补拉 —— 三种根因的处理方式完全不同。
func probeOps(apiBase, wsBase string, timeout time.Duration) {
	hc := &http.Client{Timeout: timeout}
	origin := originOf(apiBase)
	for _, p := range []string{"/health", "/ws/stats"} {
		resp, err := hc.Get(origin + p)
		if err != nil {
			fmt.Printf("  %-12s 探测失败：%v\n", p, err)
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("  %-12s %s\n", p, strings.TrimSpace(string(raw)))
	}
}

// ====================== 入口 ======================

func usage() {
	fmt.Fprint(os.Stderr, `IM 消息链路压力测试工具

用法：go run ./cmd/stress <子命令> [参数]

子命令：
  seed        创建压测靶场（N 个账号 + 一个 N 人群），落盘会话文件供反复复用
  send        纯 HTTP 发送压测：吞吐 + 延迟分位 + 失败码分布
  wsload      【核心】端到端压测：N 个 WS 客户端收流，测不丢/不重/不乱/不卡
  slowclient  慢客户端回归：验证「被踢或丢帧之后，重连补拉能否零丢失」
  check       独立一致性校验：drain sync 查 seq 空洞 / 重复 / 乱序
  stats       打印 /health 与 /ws/stats（跑前跑后各取一次，看差值）

典型流程：
  go run ./cmd/stress seed      -users 2000 -session stress.json
  go run ./cmd/stress wsload    -session stress.json -clients 2000 -senders 20 -rate 50 -duration 60s
  go run ./cmd/stress wsload    -session stress.json -clients 2000 -each-sends 0.1 -duration 60s   # 2000 人自己发
  go run ./cmd/stress check     -session stress.json

重要前提：发送接口有 per-conversation 5/s（突发 20）限流。当 N 个用户同处一个群里
发送时，瓶颈必然是这条限流（而不是链路吞吐能力），返回错误码 429。限流是**预期行为**，
工具会把它与真正的失败分开统计，不要把它读成"丢消息"。要做超高吞吐，必须把流量分散到
多个会话（多建几个群）。

各子命令的参数见 go run ./cmd/stress <子命令> -h
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "seed":
		err = runSeed(os.Args[2:])
	case "send":
		err = runSend(os.Args[2:])
	case "wsload":
		err = runWSLoad(os.Args[2:])
	case "slowclient":
		err = runSlowClient(os.Args[2:])
	case "check":
		err = runCheck(os.Args[2:])
	case "stats":
		err = runStats(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "未知子命令：%s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n[失败] %v\n", err)
		os.Exit(1)
	}
}

// 供各子命令共用的小工具

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
