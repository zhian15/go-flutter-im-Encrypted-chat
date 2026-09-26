package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ====================== 线上报文结构 ======================

// wsFrame 服务端推下来的事件帧（对应 service.Event）
type wsFrame struct {
	Type   string          `json:"type"`
	ConvID int64           `json:"convId"`
	Data   json.RawMessage `json:"data"`
}

// wsMessage 事件 data 里的消息体（对应 model.Message）。
//
// 注意：ID 类字段服务端都序列化成**字符串**（雪花 ID 超过 2^53，用 JSON number
// 给 JS 会精度丢失，所以 tag 是 `,string`），只有 seq 是数字。
// 这个细节搞错会让工具"一条消息都匹配不上"，从而误报 100% 丢包。
type wsMessage struct {
	MsgID    string `json:"msgId"`
	Seq      int64  `json:"seq"`
	SenderID string `json:"senderId"`
	Content  string `json:"content"`
}

// wsURL 拼接带鉴权的 WS 地址。服务端走 query 鉴权：?token=<JWT>&deviceType=N
// （deviceType 3 = Web，与 PC 端一致）
func wsURL(base, token string) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "token=" + url.QueryEscape(token) + "&deviceType=3"
}

// ====================== 一次压测 ======================

// recStats 单个接收端的统计。由它自己的 goroutine 独占写，所以不用加锁。
type recStats struct {
	got     int64
	dupes   int64
	ooo     int64
	lastSeq int64
	readErr string
	bitmap  []uint64
}

type loadOpts struct {
	cli         *apiClient
	sess        *sessionFile
	clients     int // 正常接收端数量
	slowClients int // 故意不读 socket 的连接数
	senders     int
	rate        float64 // 聚合目标 msg/s（senders 模式）
	eachSends   float64 // >0 时启用「每客户端自发」模式：每个已连接客户端自己以该速率发送
	duration    time.Duration
	rampup      time.Duration
	grace       time.Duration
	maxLatMS    int
}

type loadReport struct {
	connOK, connFail, slowOK int
	sentOK                   int64
	attempted                int64
	sendCodes                map[int]int
	sendLat                  *latHistogram
	e2eLat                   *latHistogram
	expected                 int64
	sentBits                 []uint64
	recs                     []*recStats
	slotCap                  int // 全局槽位位图容量（位）
	disconnected             int
	missed                   int64
	dupes                    int64
	ooo                      int64
	unexpected               int64
	starvedMin               int64
	starvedMax               int64
	duration                 time.Duration

	// 多用户并发发送相关
	eachMode          bool    // true = 每客户端自发模式（each-sends）
	numSenders        int     // 参与发送的用户数（each 模式 = clients，senders 模式 = senders）
	eachSends         float64 // each 模式下每客户端的发送速率
	offerRate         float64 // 报价速率 msg/s
	achievedRate      float64 // 实际达成速率 msg/s
	distinctSendersOK int64   // 真正成功发出 ≥1 条消息的不同用户数
	droppedSamples    int64   // 因槽位容量不足而未被计入统计的样本数（必须上报，绝不静默丢弃）
	runStamp          int64   // 本次运行的唯一标识，用于把"上一次跑的残留消息"排除在统计之外
}

func executeLoad(o loadOpts, logf func(string, ...interface{})) (*loadReport, error) {
	// ---- 模式判定 ----
	// each-sends > 0：每个已连接的 WS 客户端自己也在发消息（一个客户端 = 一个真人用户，
	// 既收也发），并忽略 -senders 发送者池。
	eachMode := o.eachSends > 0
	var numSenders int
	var offerRate float64
	if eachMode {
		if o.clients <= 0 {
			return nil, errors.New("-each-sends 模式需要 -clients 为正数（每个客户端即一个发送者）")
		}
		numSenders = o.clients
		offerRate = float64(o.clients) * o.eachSends
	} else {
		if o.senders <= 0 {
			return nil, errors.New("-senders 必须为正数")
		}
		if o.rate <= 0 {
			return nil, errors.New("-rate 必须为正数")
		}
		numSenders = o.senders
		offerRate = o.rate
	}

	// 位图容量：按预期消息总量 * 1.2 + 10000 预分配。
	//
	// 关键修复：不再用「perSenderCap × senders」——2000 个发送者时那会按发送者数
	// 成倍放大，分配出几百 MB × 2000 个接收端，工具自己先把内存打爆。改用全局原子
	// 槽位后，位图大小只与"总消息量"相关，与发送者数量**解耦**。
	expectedTotal := int64(math.Ceil(offerRate * o.duration.Seconds()))
	if expectedTotal < 1 {
		expectedTotal = 1
	}
	slotCap := int(float64(expectedTotal)*1.2) + 10000

	rep := &loadReport{
		sendCodes:  map[int]int{},
		sendLat:    newLatHistogram(o.maxLatMS),
		e2eLat:     newLatHistogram(o.maxLatMS),
		duration:   o.duration,
		eachMode:   eachMode,
		numSenders: numSenders,
		eachSends:  o.eachSends,
		offerRate:  offerRate,
		slotCap:    slotCap,
	}

	totalClients := o.clients + o.slowClients
	if totalClients > len(o.sess.Users) {
		if eachMode {
			return nil, fmt.Errorf("-each-sends 需要 %d 个账号（每个客户端一个用户），靶场只有 %d 个；请先 seed 更多，或调小 -clients",
				totalClients, len(o.sess.Users))
		}
		return nil, fmt.Errorf("需要 %d 个账号，靶场只有 %d 个（先 seed 更多，或调小 -clients/-slow）",
			totalClients, len(o.sess.Users))
	}
	if totalClients <= 0 {
		return nil, errors.New("-clients + -slow 至少要有一个")
	}

	// ---- 拨号 ----
	logf("建立连接：正常 %d + 慢客户端 %d，拨号间隔 %s …", o.clients, o.slowClients, o.rampup)
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 15 * time.Second

	conns := make([]*websocket.Conn, totalClients)
	for i := 0; i < totalClients; i++ {
		conn, _, err := dialer.Dial(wsURL(o.sess.WS, o.sess.Users[i].Token), nil)
		if err != nil {
			rep.connFail++
		} else {
			conns[i] = conn
			if i < o.clients {
				rep.connOK++
			} else {
				rep.slowOK++
			}
		}
		if o.rampup > 0 {
			time.Sleep(o.rampup)
		}
	}
	if rep.connOK == 0 {
		return nil, errors.New("一个 WebSocket 都没连上：检查 -ws 地址、token 是否有效、gateway 进程是否在跑")
	}
	logf("连接完成：正常 %d，慢 %d，失败 %d", rep.connOK, rep.slowOK, rep.connFail)

	// ---- 并发共享状态 ----
	done := make(chan struct{})
	stopCh := make(chan struct{})
	sentBits := make([]uint64, (slotCap+63)/64)
	senderBits := make([]uint64, (numSenders+63)/64)
	var bitsMu sync.Mutex   // 保护 sentBits（"发送成功"集合）
	var senderMu sync.Mutex // 保护 senderBits（"哪些用户成功发过"集合）
	var sentOK, droppedSamples int64
	var slotSeq atomic.Int64 // 全局原子槽位分配器，取代 (senderIndex, counter) 复合下标
	codes := newCodeCounter()
	runStamp := time.Now().UnixNano()

	// ---- 接收端 ----
	//
	// 慢客户端**故意不读 socket**：TCP 接收缓冲很快填满 → 服务端写不进去。
	// 这是唯一能真实触达"服务端写不动时会怎样"这个分支的办法。
	var rwg sync.WaitGroup
	rep.recs = make([]*recStats, 0, o.clients)
	for i := 0; i < o.clients; i++ {
		if conns[i] == nil {
			continue
		}
		st := &recStats{bitmap: make([]uint64, (slotCap+63)/64)}
		rep.recs = append(rep.recs, st)
		rwg.Add(1)
		go func(conn *websocket.Conn, st *recStats) {
			defer rwg.Done()
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					// 我们自己关的连接是正常收尾，不算异常断开
					select {
					case <-done:
					default:
						st.readErr = err.Error()
					}
					return
				}
				var f wsFrame
				if err := json.Unmarshal(raw, &f); err != nil {
					continue
				}
				if f.Type != "message" {
					continue // read / typing / recall 等事件与本次统计无关
				}
				var m wsMessage
				if err := json.Unmarshal(f.Data, &m); err != nil {
					continue
				}
				p, ok := decodePayload(m.Content)
				if !ok || p.R != runStamp || p.S < 0 || p.S >= numSenders {
					continue // 不是本次压测的消息（含上一次跑的残留、群系统消息）
				}
				slot := int(p.I)
				if slot < 0 || slot >= slotCap {
					// 槽位越界：**绝不静默丢弃**，累加计数并在报告里打印，
					// 否则会让"丢失率"失真（静默丢样本会直接污染"不丢消息"的结论）。
					atomic.AddInt64(&droppedSamples, 1)
					continue
				}
				if bitmapHas(st.bitmap, slot) {
					st.dupes++
					continue
				}
				bitmapSet(st.bitmap, slot)
				st.got++
				// 同一会话的 seq 由服务端单调分配，接收序列里出现回退即乱序
				if m.Seq > 0 {
					if st.lastSeq > 0 && m.Seq < st.lastSeq {
						st.ooo++
					}
					st.lastSeq = m.Seq
				}
				if p.T > 0 {
					rep.e2eLat.add(float64(time.Now().UnixMilli() - p.T))
				}
			}
		}(conns[i], st)
	}

	// ---- 发送端 ----
	//
	// sendLoop 是两种模式共用的发送循环：抢一个全局唯一槽位 → 编码进 payload → 发送。
	// 槽位由 atomic.Int64 分配，全局唯一、0 起递增，因此位图寻址与发送者数量无关。
	sendLoop := func(senderIdx int, token string, perSenderRate float64) {
		interval := time.Duration(float64(time.Second) / perSenderRate)
		if interval <= 0 {
			interval = time.Millisecond
		}
		tk := time.NewTicker(interval)
		defer tk.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-tk.C:
			}
			slot := slotSeq.Add(1) - 1
			if slot >= int64(slotCap) {
				// 槽位超出容量：必须上报，绝不静默丢弃（丢样本会污染"不丢消息"的判定）
				atomic.AddInt64(&droppedSamples, 1)
				continue
			}
			start := time.Now()
			_, err := o.cli.call(http.MethodPost, "/message/send", token, map[string]interface{}{
				"conversationId": o.sess.ConvID,
				"clientMsgId":    fmt.Sprintf("stress-%d-%d-%d", runStamp, senderIdx, slot),
				"type":           1,
				"content":        encodePayload(senderIdx, slot, runStamp),
			}, nil)
			rep.sendLat.add(float64(time.Since(start).Microseconds()) / 1000.0)
			if err != nil {
				if ae, ok := asAPIErr(err); ok {
					codes.add(ae.Code)
				} else {
					codes.add(-1) // 传输层错误：超时 / 连接被断
				}
				continue
			}
			bitsMu.Lock()
			bitmapSet(sentBits, int(slot))
			bitsMu.Unlock()
			senderMu.Lock()
			bitmapSet(senderBits, senderIdx) // 记录"这个用户确实发出去了至少一条"
			senderMu.Unlock()
			atomic.AddInt64(&sentOK, 1)
		}
	}

	var swg sync.WaitGroup
	if eachMode {
		logf("模式：每客户端自发（each-sends）—— %d 个已连接客户端各自以 %.4f msg/s 发送，"+
			"总报价速率约 %.1f msg/s（-senders 发送者池与 -rate 已被忽略）", o.clients, o.eachSends, offerRate)
		logf("身份：第 i 个客户端用 sess.Users[i] 的 token 发送，与它自己那条 WS 连接是同一个用户（模拟真人）")
		for i := 0; i < o.clients; i++ {
			if conns[i] == nil {
				continue // 没连上的客户端无法"自己发消息"
			}
			swg.Add(1)
			go func(ci int) {
				defer swg.Done()
				sendLoop(ci, o.sess.Users[ci].Token, o.eachSends)
			}(i)
		}
	} else {
		logf("模式：发送者池（senders）—— %d 个发送者，聚合目标 %.1f msg/s，持续 %s",
			o.senders, offerRate, o.duration)
		perSenderRate := offerRate / float64(o.senders)
		for s := 0; s < o.senders; s++ {
			swg.Add(1)
			go func(si int) {
				defer swg.Done()
				sendLoop(si, o.sess.Users[si%len(o.sess.Users)].Token, perSenderRate)
			}(s)
		}
	}

	time.Sleep(o.duration)
	close(stopCh)
	swg.Wait()

	if o.grace > 0 {
		logf("停发，等待 %s 让在途消息落地…", o.grace)
		time.Sleep(o.grace)
	}
	for _, c := range conns {
		if c != nil {
			_ = c.Close()
		}
	}
	close(done)
	rwg.Wait()

	rep.sentOK = atomic.LoadInt64(&sentOK)
	rep.sendCodes = codes.snapshot()
	rep.expected = bitmapPopcount(sentBits)
	rep.sentBits = sentBits
	rep.distinctSendersOK = bitmapPopcount(senderBits)
	rep.droppedSamples = atomic.LoadInt64(&droppedSamples)
	rep.runStamp = runStamp
	rep.achievedRate = float64(rep.sentOK) / o.duration.Seconds()

	// ---- 汇总 ----
	var sum int64
	for _, v := range rep.sendCodes {
		sum += int64(v)
	}
	rep.attempted = rep.sentOK + sum

	first := true
	for _, st := range rep.recs {
		if st.readErr != "" {
			rep.disconnected++
		}
		// 交集 = 真正被这个客户端收到的消息数
		inter := bitmapAndPopcount(sentBits, st.bitmap)
		rep.missed += rep.expected - inter
		// 收到了但不在"发送成功"集合里 = 服务端其实已落库、但 HTTP 返回给了我们失败
		// （典型是超时）。这类必须单独报，否则会被误算成丢弃。
		rep.unexpected += st.got - inter
		rep.dupes += st.dupes
		rep.ooo += st.ooo
		if first || st.got < rep.starvedMin {
			rep.starvedMin = st.got
			first = false
		}
		if st.got > rep.starvedMax {
			rep.starvedMax = st.got
		}
	}
	return rep, nil
}

// ====================== 结果打印 ======================

func printLoadReport(r *loadReport) {
	totalRecvSlots := int64(len(r.recs)) * r.expected
	var lossPct float64
	if totalRecvSlots > 0 {
		lossPct = float64(r.missed) / float64(totalRecvSlots) * 100
	}

	fmt.Println("\n================ 压测结果 ================")
	fmt.Printf("持续时间            %s\n", r.duration)
	fmt.Printf("连接                正常 %d / 慢 %d / 失败 %d（异常断开 %d）\n",
		r.connOK, r.slowOK, r.connFail, r.disconnected)
	if r.eachMode {
		fmt.Printf("发送模式            每客户端自发（each-sends：每个已连接客户端自己也在发消息）\n")
		fmt.Printf("目标用户数          %d 个（每个客户端一个用户；实际连上 %d 个）\n", r.numSenders, r.connOK)
	} else {
		fmt.Printf("发送模式            发送者池（senders：少数发送者代发）\n")
		fmt.Printf("参与发送的用户      %d 个发送者\n", r.numSenders)
	}

	fmt.Printf("\n-- 发送端 --\n")
	fmt.Printf("尝试发送            %d\n", r.attempted)
	fmt.Printf("发送成功（code=0）  %d\n", r.sentOK)
	var throttled int
	if len(r.sendCodes) == 0 {
		fmt.Printf("失败码分布          无\n")
	} else {
		for _, c := range sortedCodes(r.sendCodes) {
			label := ""
			switch c {
			case codeTooManyRequests:
				label = "  <- 发送限流（per-user 20/s、per-conv 5/s），预期行为，非故障"
				throttled += r.sendCodes[c]
			case -1:
				label = "  <- 传输层错误：HTTP 超时/连接被断"
			default:
				label = "  <- 业务错误"
			}
			fmt.Printf("  code=%-6d        %d%s\n", c, r.sendCodes[c], label)
		}
		if throttled > 0 {
			fmt.Printf("限流占比            %.1f%%  <== 显著高亮：这不是故障，是单会话限流在生效\n",
				float64(throttled)/float64(r.attempted)*100)
		}
	}
	s50, s90, s99, smax, savg, sn := r.sendLat.summary()
	fmt.Printf("发送延迟(ms)        p50 %.1f / p90 %.1f / p99 %.1f / max %.1f / avg %.1f（n=%d，各用户聚合）\n",
		s50, s90, s99, smax, savg, sn)

	// ---- 用户维度：这是用户最关心的数字 ----
	fmt.Printf("\n-- 用户维度（“群里 N 人发信息”，到底多少人发得出去）--\n")
	fmt.Printf("成功发出消息的用户  %d / %d\n", r.distinctSendersOK, r.numSenders)
	neverSent := int64(r.numSenders) - r.distinctSendersOK
	if neverSent > 0 {
		fmt.Printf("                    有 %d 个用户一条都没发出去  <- 这才是「N 人里几人真发得出去」的直接答案\n",
			neverSent)
		fmt.Printf("                    （若其中含未连上的客户端，见上方“连接”一行；限流也会导致用户发不出）\n")
	}
	fmt.Printf("报价速率 vs 实际    报价 %.1f msg/s → 实际达成 %.1f msg/s\n", r.offerRate, r.achievedRate)
	if r.droppedSamples > 0 {
		fmt.Printf("丢弃样本            %d  <- 槽位容量不足，未计入统计（已显式上报，非静默丢弃；请调大容量/时长）\n",
			r.droppedSamples)
	}

	fmt.Printf("\n-- 接收端（端到端：HTTP 发送 → 落库 → 扇出 → WS 投递）--\n")
	fmt.Printf("应送达消息总数      %d（发送成功的条数）\n", r.expected)
	fmt.Printf("接收端数量          %d\n", len(r.recs))
	fmt.Printf("丢失                %d 条次（丢失率 %.4f%%）\n", r.missed, lossPct)
	fmt.Printf("重复                %d 条次\n", r.dupes)
	fmt.Printf("乱序                %d 条次\n", r.ooo)
	if r.unexpected > 0 {
		fmt.Printf("意外收到            %d 条次  <- 服务端已落库但 HTTP 返回失败（多为超时），"+
			"需人工确认是否与客户端重发叠加成重复消息\n", r.unexpected)
	}
	e50, e90, e99, emax, eavg, en := r.e2eLat.summary()
	fmt.Printf("端到端延迟(ms)      p50 %.1f / p90 %.1f / p99 %.1f / max %.1f / avg %.1f（n=%d）\n",
		e50, e90, e99, emax, eavg, en)

	fmt.Printf("\n-- 接收分布（暴露「部分客户端被饿死」）--\n")
	fmt.Printf("单客户端收到        最少 %d / 最多 %d（应送达 %d）\n", r.starvedMin, r.starvedMax, r.expected)
	if r.expected > 0 && r.starvedMin < r.expected {
		fmt.Printf("最差客户端缺口      %d 条  <- 聚合丢失率可能很好看，但确实有客户端被饿死\n",
			r.expected-r.starvedMin)
	}

	// ---- per-conversation 限流是硬瓶颈的结论 ----
	// 只要多个用户（或较高速率）集中在**同一个会话**里发送，5 msg/s 就必然成为瓶颈，
	// 与链路吞吐能力无关。这是产品级决策点，必须在报告里点明。
	if throttled > 0 || r.numSenders > 1 || r.offerRate > 5 {
		fmt.Println()
		fmt.Println(">>> 【瓶颈结论】服务端 per-conversation 限流 = 5 msg/s（突发 20）。")
		if r.eachMode {
			fmt.Printf("    %d 个用户同处一个群里发送，报价速率 %.1f msg/s，远超单会话 5 msg/s ——\n",
				r.numSenders, r.offerRate)
		} else {
			fmt.Printf("    聚合速率 %.1f msg/s 集中在单个会话，远超单会话 5 msg/s ——\n", r.offerRate)
		}
		fmt.Println("    此时瓶颈必然是这条限流（而不是链路吞吐能力），丢/卡可能因此测不出来。")
		fmt.Println("    这是产品级决策点：要么调高 per-conv 阈值，要么把群拆小 / 按会话分区后分压。")
	}

	// 判定：四条性质的硬指标。延迟不做硬判定，只提示看 p99。
	fmt.Printf("\n-- 判定 --\n")
	pass := true
	if r.missed > 0 {
		fmt.Printf("[不丢] 未通过：丢 %d 条次\n", r.missed)
		pass = false
	} else {
		fmt.Printf("[不丢] 通过\n")
	}
	if r.dupes > 0 {
		fmt.Printf("[不重] 未通过：重复 %d 条次\n", r.dupes)
		pass = false
	} else {
		fmt.Printf("[不重] 通过\n")
	}
	if r.ooo > 0 {
		fmt.Printf("[不乱] 未通过：乱序 %d 条次\n", r.ooo)
		pass = false
	} else {
		fmt.Printf("[不乱] 通过\n")
	}
	if e99 > 1000 {
		fmt.Printf("[不卡] 关注：端到端 p99 = %.0fms 偏高\n", e99)
	} else {
		fmt.Printf("[不卡] p99 = %.0fms，正常\n", e99)
	}
	if r.droppedSamples > 0 {
		fmt.Printf("[样本完整] 未通过：有 %d 个样本因槽位越界未被计入，丢失率可能失真\n", r.droppedSamples)
		pass = false
	}
	if pass {
		fmt.Println("结论：不丢 / 不重 / 不乱 三项全部通过。")
	} else {
		fmt.Println("结论：存在不通过项，请先跑 check 子命令，区分是「推送丢」还是「链路真丢」。")
	}
}

// reportJSON 机器可读结果，便于把两次运行逐字段 diff
type reportJSON struct {
	Clients           int            `json:"clients"`
	SlowClients       int            `json:"slowClients"`
	ConnOK            int            `json:"connOk"`
	ConnFail          int            `json:"connFail"`
	Mode              string         `json:"mode"`
	EachSends         float64        `json:"eachSends"`
	NumSenders        int            `json:"numSenders"`
	Attempted         int64          `json:"attempted"`
	SentOK            int64          `json:"sentOk"`
	SendCodes         map[string]int `json:"sendCodes"`
	Expected          int64          `json:"expected"`
	Lost              int64          `json:"lost"`
	LossPct           float64        `json:"lossPct"`
	Dupes             int64          `json:"dupes"`
	OutOfOrder        int64          `json:"outOfOrder"`
	Unexpected        int64          `json:"unexpected"`
	Disconnected      int            `json:"disconnected"`
	StarvedMin        int64          `json:"starvedMin"`
	StarvedMax        int64          `json:"starvedMax"`
	DistinctSendersOK int64          `json:"distinctSendersOk"`
	DroppedSamples    int64          `json:"droppedSamples"`
	OfferedRate       float64        `json:"offeredMsgPerSec"`
	AchievedRate      float64        `json:"achievedMsgPerSec"`
	SendP50Ms         float64        `json:"sendP50Ms"`
	SendP99Ms         float64        `json:"sendP99Ms"`
	E2EP50Ms          float64        `json:"e2eP50Ms"`
	E2EP99Ms          float64        `json:"e2eP99Ms"`
	E2EMaxMs          float64        `json:"e2eMaxMs"`
}

func toReportJSON(r *loadReport, slow int) reportJSON {
	codes := map[string]int{}
	for k, v := range r.sendCodes {
		codes[fmt.Sprintf("%d", k)] = v
	}
	var lossPct float64
	if slots := int64(len(r.recs)) * r.expected; slots > 0 {
		lossPct = float64(r.missed) / float64(slots) * 100
	}
	s50, _, s99, _, _, _ := r.sendLat.summary()
	e50, _, e99, eMax, _, _ := r.e2eLat.summary()
	mode := "senders"
	if r.eachMode {
		mode = "each-sends"
	}
	return reportJSON{
		Clients: len(r.recs), SlowClients: slow,
		ConnOK: r.connOK, ConnFail: r.connFail,
		Mode: mode, EachSends: r.eachSends, NumSenders: r.numSenders,
		Attempted: r.attempted, SentOK: r.sentOK, SendCodes: codes,
		Expected: r.expected, Lost: r.missed, LossPct: lossPct,
		Dupes: r.dupes, OutOfOrder: r.ooo, Unexpected: r.unexpected,
		Disconnected: r.disconnected,
		StarvedMin:   r.starvedMin, StarvedMax: r.starvedMax,
		DistinctSendersOK: r.distinctSendersOK, DroppedSamples: r.droppedSamples,
		OfferedRate: r.offerRate, AchievedRate: r.achievedRate,
		SendP50Ms: s50, SendP99Ms: s99,
		E2EP50Ms: e50, E2EP99Ms: e99, E2EMaxMs: eMax,
	}
}

func writeJSON(path string, v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// ====================== send ======================

// runSend 纯 HTTP 发送压测，衡量"发送转圈"那一面：吞吐、延迟分位、失败码分布。
//
// 它不建立任何 WS 连接，测的是「客户端 → 服务端 → 落库」这一段。与 wsload 的
// 端到端口径互补：如果 send 很快但 wsload 端到端延迟很高，瓶颈就在扇出/推送侧，
// 而不是写入侧 —— 这个区分决定了该去优化哪一段。
func runSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	base := fs.String("base", "", "API 基址（留空用会话文件里的值）")
	sessionPath := fs.String("session", "stress-session.json", "会话文件路径")
	senders := fs.Int("senders", 20, "发送者数量")
	rate := fs.Float64("rate", 50, "聚合目标发送速率 msg/s")
	duration := fs.Duration("duration", 60*time.Second, "发送持续时间")
	timeout := fs.Duration("timeout", 15*time.Second, "单次 HTTP 超时")
	maxLat := fs.Int("max-lat", 30000, "延迟直方图上限（毫秒）")
	jsonOut := fs.String("json", "", "把结果另存为 JSON")
	fs.Parse(args)

	sess, err := loadSession(*sessionPath)
	if err != nil {
		return err
	}
	if *base != "" {
		sess.Base = *base
	}
	if *senders <= 0 {
		return errors.New("-senders 必须为正数")
	}
	if *rate <= 0 {
		return errors.New("-rate 必须为正数")
	}

	cli := newAPIClient(sess.Base, *timeout)
	lat := newLatHistogram(*maxLat)
	codes := newCodeCounter()
	var ok, attempted int64

	stop := make(chan struct{})
	var wg sync.WaitGroup
	runStamp := time.Now().UnixNano()
	for s := 0; s < *senders; s++ {
		wg.Add(1)
		go func(si int) {
			defer wg.Done()
			tok := sess.Users[si%len(sess.Users)].Token
			// ticker 平滑发压：用空转 sleep 会累积漂移，实测速率会偏离目标
			interval := time.Duration(float64(*senders) / *rate * float64(time.Second))
			if interval <= 0 {
				interval = time.Millisecond
			}
			tk := time.NewTicker(interval)
			defer tk.Stop()
			var n int64
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
				}
				start := time.Now()
				_, err := cli.call(http.MethodPost, "/message/send", tok, map[string]interface{}{
					"conversationId": sess.ConvID,
					"clientMsgId":    fmt.Sprintf("stress-send-%d-%d-%d", runStamp, si, n),
					"type":           1,
					"content":        encodePayload(si, n, runStamp),
				}, nil)
				n++
				atomic.AddInt64(&attempted, 1)
				lat.add(float64(time.Since(start).Microseconds()) / 1000.0)
				if err != nil {
					if ae, isAPI := asAPIErr(err); isAPI {
						codes.add(ae.Code)
					} else {
						codes.add(-1)
					}
					continue
				}
				atomic.AddInt64(&ok, 1)
			}
		}(s)
	}

	fmt.Printf("靶场：群 %s\n%d 个发送者，目标 %.0f msg/s，持续 %s …\n", sess.ConvID, *senders, *rate, *duration)
	time.Sleep(*duration)
	close(stop)
	wg.Wait()

	att := atomic.LoadInt64(&attempted)
	sentOK := atomic.LoadInt64(&ok)
	p50, p90, p99, pmax, pavg, pn := lat.summary()
	snap := codes.snapshot()

	fmt.Println("\n================ 发送压测结果 ================")
	fmt.Printf("尝试发送            %d\n", att)
	fmt.Printf("成功（code=0）      %d\n", sentOK)
	if len(snap) == 0 {
		fmt.Printf("失败码分布          无\n")
	} else {
		var throttled int
		for _, c := range sortedCodes(snap) {
			label := ""
			switch c {
			case codeTooManyRequests:
				throttled += snap[c]
				label = "  <- 发送限流（预期行为，非故障）"
			case -1:
				label = "  <- 传输层错误：HTTP 超时/连接被断"
			default:
				label = "  <- 业务错误"
			}
			fmt.Printf("  code=%-6d        %d%s\n", c, snap[c], label)
		}
		if throttled > 0 {
			fmt.Printf("限流占比            %.1f%%\n", float64(throttled)/float64(att)*100)
			fmt.Println("                    单会话发送上限即 per-conv 5/s（突发 20）；")
			fmt.Println("                    要继续加压请把流量分散到多个会话。")
		}
	}
	fmt.Printf("实际吞吐            %.1f msg/s（目标 %.1f）\n", float64(sentOK)/duration.Seconds(), *rate)
	fmt.Printf("发送延迟(ms)        p50 %.1f / p90 %.1f / p99 %.1f / max %.1f / avg %.1f（n=%d）\n",
		p50, p90, p99, pmax, pavg, pn)

	fmt.Println("\n-- 判定 --")
	if p99 > 1000 {
		fmt.Printf("[不卡] 关注：发送 p99 = %.0fms 偏高，客户端会明显「转圈」。\n", p99)
	} else {
		fmt.Printf("[不卡] 发送 p99 = %.0fms，正常。\n", p99)
	}
	if throttled := snap[codeTooManyRequests]; throttled > 0 {
		fmt.Printf("提示：%d 次请求被限流，这些不是故障。若在真实客户端出现，",
			throttled)
		fmt.Println("      由于 clientMsgId 幂等，退避后重发是安全的。")
	}
	if transport := snap[-1]; transport > 0 {
		fmt.Printf("警告：%d 次传输层错误（HTTP 超时等）。超时不代表消息没发出去 —— "+
			"请在 wsload 里看「意外收到」计数确认是否落库成功。\n", transport)
	}

	if *jsonOut != "" {
		out := map[string]interface{}{
			"attempted":      att,
			"sentOk":         sentOK,
			"sendCodes":      snap,
			"p50Ms":          p50,
			"p90Ms":          p90,
			"p99Ms":          p99,
			"maxMs":          pmax,
			"avgMs":          pavg,
			"achievedPerSec": float64(sentOK) / duration.Seconds(),
		}
		if err := writeJSON(*jsonOut, out); err != nil {
			return fmt.Errorf("写 JSON 失败：%w", err)
		}
		fmt.Printf("\n结果已写入 %s\n", *jsonOut)
	}
	return nil
}

// ====================== wsload ======================

func runWSLoad(args []string) error {
	fs := flag.NewFlagSet("wsload", flag.ExitOnError)
	base := fs.String("base", "", "API 基址（留空用会话文件里的值）")
	wsBase := fs.String("ws", "", "WS 基址（留空用会话文件里的值）")
	sessionPath := fs.String("session", "stress-session.json", "会话文件路径")
	clients := fs.Int("clients", 2000, "接收端连接数")
	senders := fs.Int("senders", 20, "发送者数量（发送者池模式；与 -each-sends 互斥）")
	rate := fs.Float64("rate", 50, "聚合目标发送速率 msg/s（发送者池模式；each-sends 模式下忽略）")
	eachSends := fs.Float64("each-sends", 0, "每个已连接客户端自己的发送速率 msg/s；>0 启用「每客户端自发」模式（与 -senders 互斥）")
	duration := fs.Duration("duration", 60*time.Second, "发送持续时间")
	rampup := fs.Duration("rampup", 2*time.Millisecond, "每个连接的拨号间隔（避免瞬时打爆 accept 队列）")
	grace := fs.Duration("grace", 3*time.Second, "停发后等待在途消息的时间")
	timeout := fs.Duration("timeout", 15*time.Second, "单次 HTTP 超时")
	maxLat := fs.Int("max-lat", 30000, "延迟直方图上限（毫秒）")
	jsonOut := fs.String("json", "", "把结果另存为 JSON，便于两次运行 diff")
	fs.Parse(args)

	// 用 fs.Visit 区分"用户显式传了"和"用了默认值"，才能对 -each-sends / -senders
	// 做正确的互斥判定（两者都有默认值，仅看值无法区分）。
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	if *eachSends < 0 {
		return errors.New("-each-sends 不能为负")
	}
	if *eachSends > 0 && explicit["senders"] {
		return errors.New("-each-sends 与 -senders 互斥：-each-sends 模式下每个客户端都用自己发送，" +
			"本就不需要发送者池。请二选一（去掉其中一个）")
	}

	sess, err := loadSession(*sessionPath)
	if err != nil {
		return err
	}
	if *base != "" {
		sess.Base = *base
	}
	if *wsBase != "" {
		sess.WS = *wsBase
	}

	fmt.Printf("靶场：群 %s，可用账号 %d\n", sess.ConvID, len(sess.Users))
	if *eachSends > 0 {
		fmt.Printf("发送模式：每客户端自发（each-sends=%.4f msg/s）—— 忽略发送者池\n", *eachSends)
		if *clients > len(sess.Users) {
			return fmt.Errorf("-each-sends 需要 %d 个账号（每个客户端一个用户），靶场只有 %d 个；"+
				"请先 seed 更多，或调小 -clients", *clients, len(sess.Users))
		}
	} else {
		fmt.Printf("发送模式：发送者池（senders=%d，rate=%.0f msg/s）\n", *senders, *rate)
	}
	fmt.Println("跑前运维指标：")
	probeOps(sess.Base, sess.WS, *timeout)

	rep, err := executeLoad(loadOpts{
		cli:       newAPIClient(sess.Base, *timeout),
		sess:      sess,
		clients:   *clients,
		senders:   *senders,
		rate:      *rate,
		eachSends: *eachSends,
		duration:  *duration,
		rampup:    *rampup,
		grace:     *grace,
		maxLatMS:  *maxLat,
	}, func(f string, a ...interface{}) { fmt.Printf("  "+f+"\n", a...) })
	if err != nil {
		return err
	}
	printLoadReport(rep)

	fmt.Println("\n跑后运维指标（与跑前对比，判断丢在哪一层）：")
	probeOps(sess.Base, sess.WS, *timeout)

	if *jsonOut != "" {
		if err := writeJSON(*jsonOut, toReportJSON(rep, 0)); err != nil {
			return fmt.Errorf("写 JSON 失败：%w", err)
		}
		fmt.Printf("\n结果已写入 %s\n", *jsonOut)
	}
	return nil
}

// ====================== slowclient ======================

// matchRecovered 统计"补拉回来的消息里，有多少条确实属于本次运行（runStamp）且落在
// 本轮槽位空间内"。
//
// 为什么必须按 runStamp 过滤：drainSync 用 afterSeq=0，会把同一群里**历史上所有**
// 压测消息都拉回来；而会话 ID（stress-session.json 里的 sess.ConvID）跨运行复用、
// 全局槽位 i 每次运行都从 0 重新计数。若不区分 run，上一次跑的残留消息会带着与本次
// 一模一样的槽位号"碰撞"，把本次真丢的消息"补"成覆盖率 100% —— 这正是最危险方向的
// 假全绿（工具存在的唯一意义就是回答"能不能开踢连接"）。
//
// 抽成纯函数（不依赖服务端 / 网络）是为了能在无服务端的情况下做回归测试：
// 见 recovery_test.go 里的 TestMatchRecoveredExcludesPreviousRun。
func matchRecovered(sentBits []uint64, slotCap int, runStamp int64, msgs []syncMsg) int64 {
	recovered := make([]uint64, len(sentBits))
	for _, m := range msgs {
		p, ok := decodePayload(m.Content)
		if !ok || p.R != runStamp {
			continue // 不是本次压测的消息（含上一次跑的残留）：绝不能被它"补"成 100%
		}
		slot := int(p.I)
		if slot < 0 || slot >= slotCap {
			continue
		}
		bitmapSet(recovered, slot)
	}
	return bitmapAndPopcount(sentBits, recovered)
}

// runSlowClient 是"能不能开踢连接开关"的决策实验。
//
// 踢连接（KICK_SLOW_CLIENTS）本身是安全的，**前提是客户端重连后能靠补拉把消息
// 全捞回来**。这个子命令就在同一个进程里把这件事走一遍：
//  1. 挂一个从不读 socket 的慢客户端，把它的 TCP 缓冲堵死；
//  2. 正常发送压测流量；
//  3. 关掉慢客户端 → 重新连一条 WS → 用 /message/sync 从 seq 0 把消息全量拉回；
//  4. 比对"补拉捞回多少"与"发送成功多少"，得出覆盖率。
//
// 覆盖率 100% = 可以放心开踢连接开关；不足 100% = 开了就会真丢消息。
func runSlowClient(args []string) error {
	fs := flag.NewFlagSet("slowclient", flag.ExitOnError)
	base := fs.String("base", "", "API 基址（留空用会话文件里的值）")
	wsBase := fs.String("ws", "", "WS 基址（留空用会话文件里的值）")
	sessionPath := fs.String("session", "stress-session.json", "会话文件路径")
	clients := fs.Int("clients", 20, "正常接收端数量")
	slow := fs.Int("slow", 3, "慢客户端数量（从不读 socket）")
	senders := fs.Int("senders", 4, "发送者数量")
	rate := fs.Float64("rate", 20, "聚合目标发送速率 msg/s")
	duration := fs.Duration("duration", 20*time.Second, "发送持续时间")
	timeout := fs.Duration("timeout", 20*time.Second, "单次 HTTP 超时")
	maxLat := fs.Int("max-lat", 30000, "延迟直方图上限（毫秒）")
	jsonOut := fs.String("json", "", "把结果另存为 JSON")
	fs.Parse(args)

	sess, err := loadSession(*sessionPath)
	if err != nil {
		return err
	}
	if *base != "" {
		sess.Base = *base
	}
	if *wsBase != "" {
		sess.WS = *wsBase
	}
	if *slow <= 0 {
		return errors.New("-slow 必须为正数，否则测不出慢客户端行为")
	}

	fmt.Printf("靶场：群 %s，可用账号 %d\n", sess.ConvID, len(sess.Users))
	fmt.Println("跑前运维指标（重点看 kickSlowClients 是否为 true）：")
	probeOps(sess.Base, sess.WS, *timeout)

	rep, err := executeLoad(loadOpts{
		cli:         newAPIClient(sess.Base, *timeout),
		sess:        sess,
		clients:     *clients,
		slowClients: *slow,
		senders:     *senders,
		rate:        *rate,
		duration:    *duration,
		rampup:      time.Millisecond,
		grace:       3 * time.Second,
		maxLatMS:    *maxLat,
	}, func(f string, a ...interface{}) { fmt.Printf("  "+f+"\n", a...) })
	if err != nil {
		return err
	}
	printLoadReport(rep)

	fmt.Println("\n跑后运维指标：")
	probeOps(sess.Base, sess.WS, *timeout)

	// ---- 关键实验：慢客户端断线重连后，靠补拉能否零丢失 ----
	// 用第 clients+slow-1 个账号（就是那个慢客户端）重连，再全量 drain sync。
	fmt.Println("\n---- 恢复验证：慢客户端重连 + 补拉 ----")
	slowUser := sess.Users[*clients+*slow-1]
	cli := newAPIClient(sess.Base, *timeout)

	// 先重连一条 WS，证明"断线后能恢复长连接"
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 15 * time.Second
	conn, _, derr := dialer.Dial(wsURL(sess.WS, slowUser.Token), nil)
	if derr != nil {
		fmt.Printf("  重连失败：%v\n", derr)
	} else {
		fmt.Println("  重连成功（慢客户端断线后可以恢复长连接）")
		_ = conn.Close()
	}

	msgs, err := drainSync(cli, slowUser.Token, sess.ConvID, 0, 500)
	if err != nil {
		return fmt.Errorf("补拉失败：%w", err)
	}
	// 把补拉回来的压测消息标进位图，再与"发送成功"求交集。
	//
	// 关键安全线：**必须按 runStamp 过滤**（见 matchRecovered 的说明）。
	matched := matchRecovered(rep.sentBits, rep.slotCap, rep.runStamp, msgs)

	fmt.Printf("  sync 全量拉回消息          %d 条\n", len(msgs))
	fmt.Printf("  其中属于本次压测的         %d 条\n", matched)
	fmt.Printf("  发送成功                   %d 条\n", rep.expected)
	fmt.Printf("  本次 runStamp              %d（仅统计带该标识的消息）\n", rep.runStamp)
	cover := 100.0
	if rep.expected > 0 {
		cover = float64(matched) / float64(rep.expected) * 100
	}
	fmt.Printf("  补拉覆盖率                 %.2f%%\n", cover)

	fmt.Println("\n-- 判定 --")
	switch {
	case rep.expected == 0:
		// expected=0 说明本轮发送成功 0 条（2000 人同群时极易发生：全被 per-conv 限流）。
		// 此时 cover 默认 100，若照常打印"安全开启"就是**空跑假绿**，必须判为不可结论。
		fmt.Println("本轮发送成功 0 条（极可能被 per-conv 限流全部拦截），补拉实验**不可结论**：")
		fmt.Println("无法据此判断 KICK_SLOW_CLIENTS 是否安全。请降低速率 / 减少发送者后重跑。")
	case cover >= 99.999:
		fmt.Printf("补拉覆盖率 100%%（本次 runStamp %d，发送成功 %d 条，补回 %d 条）：\n",
			rep.runStamp, rep.expected, matched)
		fmt.Println("踢掉慢客户端后消息可通过重连+补拉完整恢复，")
		fmt.Println("开启 KICK_SLOW_CLIENTS 是安全的（前提：客户端确实实现了补拉）。")
	default:
		fmt.Printf("补拉覆盖率不足 100%%（本次 runStamp %d，缺 %d 条）：此时开启 KICK_SLOW_CLIENTS 会真丢消息，先别开。\n",
			rep.runStamp, rep.expected-matched)
	}
	fmt.Printf("本次观察到的服务端行为：慢客户端 %d 个，其中异常断开 %d 个；"+
		"若 kickSlowClients=false 且异常断开为 0，说明服务端选择了「丢帧保连接」而不是踢人。\n",
		rep.slowOK, rep.disconnected)

	if *jsonOut != "" {
		out := map[string]interface{}{
			"load":           toReportJSON(rep, *slow),
			"syncDrained":    len(msgs),
			"syncMatched":    matched,
			"recoveryCoverP": cover,
		}
		if err := writeJSON(*jsonOut, out); err != nil {
			return fmt.Errorf("写 JSON 失败：%w", err)
		}
		fmt.Printf("\n结果已写入 %s\n", *jsonOut)
	}
	return nil
}
