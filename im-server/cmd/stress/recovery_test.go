package main

import "testing"

// 本文件是对 slowclient「补拉覆盖率」实验的回归防护。
//
// 背景缺陷：loadPayload 早期只有 {t,s,i} 三个字段，没有 run 标识；而 drainSync 用
// afterSeq=0 会把同一群里历史上所有压测消息都拉回来（会话 ID 跨运行复用、槽位 i 每次
// 从 0 重数）。于是**上一次跑的残留消息会与本次的槽位号完全碰撞**，把本次真丢的消息
// "补"成覆盖率 100% —— 工具在"能不能开 KICK_SLOW_CLIENTS"这个唯一的判断题上给出
// 了危险方向的假全绿。
//
// 修复：payload 增加 r(runStamp) 字段，matchRecovered 按 runStamp 严格过滤。
// 这些用例不需要服务端，纯逻辑即可证明过滤生效。

const testSlotCap = 256 // 位图容量（位），>= 本文件用到的最大槽位

// mkSentBits 造出"本轮发送成功"的位图：slots 里的槽位被置 1。
func mkSentBits(slots ...int) []uint64 {
	bm := make([]uint64, (testSlotCap+63)/64)
	for _, s := range slots {
		bitmapSet(bm, s)
	}
	return bm
}

// msgsOfRun 造出一批"某次运行( run )发出去的、随后被 sync 拉回"的消息。
func msgsOfRun(run int64, slots ...int) []syncMsg {
	out := make([]syncMsg, 0, len(slots))
	for _, s := range slots {
		out = append(out, syncMsg{Content: encodePayload(0, int64(s), run)})
	}
	return out
}

// 核心回归：本轮真丢全部消息，但同一群里存在"上一次跑"的残留消息，且槽位号完全重合。
// 修复后必须判为 matched==0（覆盖率 0%），而**不是** 100%。
//
// 同时复刻"修复前"的旧逻辑作为对照组，把 before/after 的差别钉死在测试里。
func TestMatchRecoveredExcludesPreviousRun(t *testing.T) {
	const thisRun, prevRun = int64(222), int64(111)

	sentBits := mkSentBits(0, 1, 2, 3, 4, 5, 6, 7, 8, 9) // 本轮发出 10 条
	// 拉回来的全是**上一次跑**的消息，槽位与本次完全重合 —— 经典的污染场景。
	msgs := msgsOfRun(prevRun, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9)

	// ---- 对照组：复刻"修复前"的错误行为（不按 run 过滤，只看槽位越界）----
	// 旧代码会把上一次跑的残留消息算成本轮补回 → matched=10、覆盖率 100%（假全绿）。
	legacy := make([]uint64, len(sentBits))
	for _, m := range msgs {
		p, ok := decodePayload(m.Content)
		if !ok {
			continue
		}
		slot := int(p.I)
		if slot < 0 || slot >= testSlotCap {
			continue
		}
		bitmapSet(legacy, slot)
	}
	if legacyMatched := bitmapAndPopcount(sentBits, legacy); legacyMatched != 10 {
		t.Fatalf("对照组前提不成立：旧逻辑 matched=%d，期望 10（用于证明缺陷确实会假全绿）", legacyMatched)
	}

	// ---- 修复后的行为：严格按 runStamp 过滤 ----
	matched := matchRecovered(sentBits, testSlotCap, thisRun, msgs)
	if matched != 0 {
		t.Fatalf("污染未排除：matched=%d，期望 0（残留消息不得把本次丢失“补”成 100%%）", matched)
	}
}

// 本轮自己的消息必须被正确统计。
func TestMatchRecoveredCountsOwnRun(t *testing.T) {
	const thisRun = int64(222)

	sentBits := mkSentBits(0, 1, 2, 3, 4, 5, 6, 7, 8, 9)
	msgs := msgsOfRun(thisRun, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9)

	matched := matchRecovered(sentBits, testSlotCap, thisRun, msgs)
	if matched != 10 {
		t.Fatalf("本轮消息未被完整统计：matched=%d，期望 10", matched)
	}
}

// 混合场景：残留 + 本轮部分补回。只应统计本轮补回的那部分。
func TestMatchRecoveredMixedRuns(t *testing.T) {
	const thisRun, prevRun = int64(222), int64(111)

	sentBits := mkSentBits(0, 1, 2, 3, 4, 5, 6, 7, 8, 9)
	msgs := append(
		msgsOfRun(prevRun, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9), // 残留：全部槽位
		msgsOfRun(thisRun, 0, 1, 2, 3, 4)...,             // 本轮真正补回：前 5 条
	)

	matched := matchRecovered(sentBits, testSlotCap, thisRun, msgs)
	if matched != 5 {
		t.Fatalf("混合场景统计错误：matched=%d，期望 5", matched)
	}
}

// 越界槽位与"旧格式消息"（无 r 字段，解码后 R==0）都必须被忽略。
func TestMatchRecoveredIgnoresOutOfRangeAndLegacy(t *testing.T) {
	const thisRun = int64(222)

	sentBits := mkSentBits(0, 1, 2)
	msgs := []syncMsg{
		{Content: encodePayload(0, 0, thisRun)},                  // 有效
		{Content: encodePayload(0, int64(testSlotCap), thisRun)}, // 越界，应忽略
		{Content: `{"t":1,"s":0,"i":1}`},                         // 旧格式：R==0，应忽略
		{Content: "not-json"},                                    // 非法，应忽略
	}

	matched := matchRecovered(sentBits, testSlotCap, thisRun, msgs)
	if matched != 1 {
		t.Fatalf("越界/旧格式未被忽略：matched=%d，期望 1", matched)
	}
}
