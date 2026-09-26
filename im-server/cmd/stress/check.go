package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// ====================== /message/sync ======================

// syncMsg /message/sync 返回的单条消息（只取本工具关心的字段）
type syncMsg struct {
	MsgID    string `json:"msgId"`
	Seq      int64  `json:"seq"`
	SenderID string `json:"senderId"`
	Content  string `json:"content"`
}

// syncPage 是 /message/sync 的响应体（对应服务端 service.SyncResult）
type syncPage struct {
	List      []syncMsg `json:"list"`
	HasMore   bool      `json:"hasMore"`
	MaxSeq    int64     `json:"maxSeq"`
	ServerSeq int64     `json:"serverSeq"`
	Reset     bool      `json:"reset"`
}

// drainSync 从 afterSeq 起循环翻页，把消息拉干净。
//
// 这是本工具最重要的**独立证据来源**：sync 由服务端从 MongoDB 按 seq 顺序读出，
// 与 WS 推送走的是完全不同的代码路径。两者对比能直接定位责任方：
//   - WS 少收，但 sync 里一条不缺  → 消息没丢，是「最后一公里」推送丢了；
//   - sync 里也缺                → 那才是真的写丢了/根本没落库。
//
// 这两种结论的修复方向完全不同，所以必须能把它们分开证明。
func drainSync(cli *apiClient, token, convID string, afterSeq int64, maxPages int) ([]syncMsg, error) {
	var all []syncMsg
	for page := 0; page < maxPages; page++ {
		data, err := cli.call(http.MethodGet, "/message/sync", token, nil, map[string]string{
			"convId":   convID,
			"afterSeq": fmt.Sprintf("%d", afterSeq),
			"limit":    "500",
		})
		if err != nil {
			return all, err
		}
		var p syncPage
		if err := json.Unmarshal(data, &p); err != nil {
			return all, fmt.Errorf("解析 sync 响应失败：%w", err)
		}
		if len(p.List) == 0 {
			break
		}
		all = append(all, p.List...)

		next := p.MaxSeq
		if next <= afterSeq {
			next = p.ServerSeq
		}
		if next <= afterSeq {
			break // 断点没推进，避免死循环
		}
		afterSeq = next
		if !p.HasMore {
			break
		}
	}
	return all, nil
}

// ====================== check ======================

// runCheck 是 不丢/不重/不乱 的独立一致性校验。
//
// 思路：把服务端的权威序列整段拉下来，直接检查它自身是否自洽 ——
// seq 有没有空洞（丢）、有没有重复（重）、返回顺序有没有回退（乱）。
// 它不依赖任何客户端行为，因此可以当"仲裁者"：当 wsload 报丢包时，跑一次 check
// 就能判定到底是推送丢还是链路真丢。
func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	base := fs.String("base", "", "API 基址（留空用会话文件里的值）")
	sessionPath := fs.String("session", "stress-session.json", "会话文件路径")
	convFlag := fs.String("conv", "", "会话 ID（留空用会话文件里的值）")
	fromSeq := fs.Int64("from-seq", 0, "起始 seq（默认 0 = 全量）")
	maxPages := fs.Int("max-pages", 500, "最多翻页数（500 页 × 500 条 = 25 万条）")
	timeout := fs.Duration("timeout", 30*time.Second, "单请求超时")
	jsonOut := fs.String("json", "", "把结果另存为 JSON")
	fs.Parse(args)

	sess, err := loadSession(*sessionPath)
	if err != nil {
		return err
	}
	if *base != "" {
		sess.Base = *base
	}
	conv := *convFlag
	if conv == "" {
		conv = sess.ConvID
	}
	cli := newAPIClient(sess.Base, *timeout)
	user := sess.Users[0]

	fmt.Printf("会话 %s，起始 seq %d，正在全量 drain /message/sync …\n", conv, *fromSeq)
	msgs, err := drainSync(cli, user.Token, conv, *fromSeq, *maxPages)
	if err != nil {
		return fmt.Errorf("drain 失败：%w", err)
	}
	if len(msgs) == 0 {
		fmt.Println("该会话在 sync 里没有任何消息（seq 起点可能取太高，或会话为空）。")
		return nil
	}

	// 乱序必须在**返回顺序**上判断：sync 本就该按 seq 升序返回，
	// 所以先按原始顺序查回退，再排序查重复与空洞。
	var ooo int
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Seq < msgs[i-1].Seq {
			ooo++
		}
	}

	seqs := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		seqs = append(seqs, m.Seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	var dupSeq, gapCount int
	var gapSamples []string
	for i := 1; i < len(seqs); i++ {
		if seqs[i] == seqs[i-1] {
			dupSeq++
			continue
		}
		if d := seqs[i] - seqs[i-1]; d > 1 {
			gapCount += int(d - 1)
			if len(gapSamples) < 10 {
				gapSamples = append(gapSamples, fmt.Sprintf("[%d..%d]", seqs[i-1]+1, seqs[i]-1))
			}
		}
	}
	minSeq, maxSeq := seqs[0], seqs[len(seqs)-1]
	span := maxSeq - minSeq + 1

	// 服务端当前水位，用于确认本地断点是否落后
	var serverSeq int64
	if data, err := cli.call(http.MethodGet, "/message/lastSeq", user.Token, nil, map[string]string{"convId": conv}); err == nil {
		var v struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(data, &v) == nil {
			serverSeq = v.Seq
		}
	}

	fmt.Println("\n================ 一致性校验 ================")
	fmt.Printf("拉回消息条数        %d\n", len(msgs))
	fmt.Printf("seq 范围            %d ~ %d（跨度 %d）\n", minSeq, maxSeq, span)
	fmt.Printf("服务端当前水位      %d\n", serverSeq)
	fmt.Printf("重复 seq            %d\n", dupSeq)
	fmt.Printf("seq 空洞            %d\n", gapCount)
	if len(gapSamples) > 0 {
		fmt.Printf("  空洞样例          %v\n", gapSamples)
		fmt.Println("  说明：空洞不一定等于丢消息 —— 后台被「屏蔽」的消息在 sync 里本就不下发，也会形成空洞。")
		fmt.Println("        要区分二者，需对照 Mongo 里该会话的实际文档数。")
	}
	fmt.Printf("返回顺序回退        %d\n", ooo)
	if len(msgs) != int(span) {
		fmt.Printf("条数 vs 跨度        不一致：实际 %d，跨度 %d（差 %d）\n", len(msgs), span, int(span)-len(msgs))
	} else {
		fmt.Printf("条数 vs 跨度        一致（%d）\n", len(msgs))
	}

	fmt.Println("\n-- 判定 --")
	ok := true
	if dupSeq > 0 {
		fmt.Printf("[不重] 未通过：同一 seq 出现 %d 次\n", dupSeq)
		ok = false
	} else {
		fmt.Println("[不重] 通过：seq 无重复")
	}
	if gapCount > 0 {
		fmt.Printf("[不丢] 存疑：存在 %d 个 seq 空洞（需排除「被屏蔽消息」后确认）\n", gapCount)
		ok = false
	} else {
		fmt.Println("[不丢] 通过：seq 连续无空洞")
	}
	if ooo > 0 {
		fmt.Printf("[不乱] 未通过：sync 返回顺序里 seq 回退 %d 次\n", ooo)
		ok = false
	} else {
		fmt.Println("[不乱] 通过：sync 按 seq 升序返回")
	}
	if ok {
		fmt.Println("结论：服务端序列自洽 —— 若客户端仍报丢消息，问题在推送/客户端补拉，不在存储。")
	} else {
		fmt.Println("结论：服务端序列存在异常，先查服务端，不要先去查客户端。")
	}

	if *jsonOut != "" {
		out := map[string]interface{}{
			"conversationId": conv,
			"count":          len(msgs),
			"minSeq":         minSeq,
			"maxSeq":         maxSeq,
			"serverSeq":      serverSeq,
			"duplicateSeq":   dupSeq,
			"gapCount":       gapCount,
			"gapSamples":     gapSamples,
			"outOfOrder":     ooo,
			"span":           span,
		}
		if err := writeJSON(*jsonOut, out); err != nil {
			return fmt.Errorf("写 JSON 失败：%w", err)
		}
		fmt.Printf("\n结果已写入 %s\n", *jsonOut)
	}
	return nil
}

// ====================== stats ======================

const (
	defaultAPIBase = "http://127.0.0.1:8080/api/v1"
	defaultWSBase  = "ws://127.0.0.1:8080/ws"
)

// runStats 打印 /health 与 /ws/stats。跑压测前后各执行一次，看差值即可判断
// 消息丢在"写缓冲帧"还是"事件消费"层。
func runStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	base := fs.String("base", defaultAPIBase, "API 基址")
	wsBase := fs.String("ws", defaultWSBase, "WS 基址")
	sessionPath := fs.String("session", "", "会话文件路径（可选，用于自动取地址）")
	timeout := fs.Duration("timeout", 10*time.Second, "探测超时")
	fs.Parse(args)

	apiBase, wsAddr := *base, *wsBase
	// 会话文件里的地址优先，但显式传了 flag 就听 flag 的
	if *sessionPath != "" {
		if sess, err := loadSession(*sessionPath); err == nil {
			if *base == defaultAPIBase {
				apiBase = sess.Base
			}
			if *wsBase == defaultWSBase {
				wsAddr = sess.WS
			}
		}
	}
	probeOps(apiBase, wsAddr, *timeout)
	fmt.Println("\n判读：droppedFrames 增长 = WS 写缓冲塞不下被丢帧（慢客户端/带宽问题）；")
	fmt.Println("      droppedEvents 增长 = 事件消费侧被丢（Redis PubSub 缓冲打满，扇出跟不上）。")
	fmt.Println("      两者都不涨却仍丢消息 → 问题在客户端补拉，不在服务端推送。")
	return nil
}
