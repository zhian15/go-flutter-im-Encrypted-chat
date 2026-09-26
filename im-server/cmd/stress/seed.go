package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// runSeed 造出压测靶场：N 个账号 + 一个装着他们的群。
//
// 顺序不能颠倒：账号必须先全部存在，才能建群。否则 memberIds 里混进不存在的用户
// 时，服务端插入成员行不会报错，但那个"成员"永远收不到消息 —— 压测会把这笔账
// 算到消息链路头上，得出"丢了消息"的错误结论。
//
// 靶场落盘复用：注册有 IP 级限流，2000 个账号不可能每次压测都重建。
func runSeed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	base := fs.String("base", "http://127.0.0.1:8080/api/v1", "API 基址")
	ws := fs.String("ws", "ws://127.0.0.1:8080/ws", "WebSocket 基址（写入会话文件供 wsload 用）")
	users := fs.Int("users", 2000, "账号数（= 群成员数）")
	password := fs.String("password", "Stress@12345", "账号密码（6-20 位）")
	groupName := fs.String("group-name", "压测群", "群名称")
	sessionPath := fs.String("session", "stress-session.json", "会话文件路径（含 token，写入权限 0600）")
	reuse := fs.Bool("reuse", true, "会话文件已存在且账号数足够时直接复用，不重复注册")
	chunk := fs.Int("chunk", 200, "邀请成员的分批大小")
	conc := fs.Int("conc", 16, "注册并发度（注册有 IP 级限流，别开太大）")
	timeout := fs.Duration("timeout", 20*time.Second, "单请求超时")
	runID := fs.String("run-id", "", "批次标识，默认取当前时间戳")
	fs.Parse(args)

	if *chunk <= 0 {
		return errors.New("-chunk 必须为正数")
	}
	if *conc <= 0 {
		return errors.New("-conc 必须为正数")
	}

	cli := newAPIClient(*base, *timeout)

	// 复用已有靶场：账号数够就直接返回，避免重复注册（也避免撞限流）
	if *reuse {
		if s, err := loadSession(*sessionPath); err == nil {
			if len(s.Users) >= *users {
				fmt.Printf("复用已有靶场：%s（账号 %d，群 %s）\n", *sessionPath, len(s.Users), s.ConvID)
				return nil
			}
			fmt.Printf("已有靶场账号数 %d 少于目标 %d，重新创建\n", len(s.Users), *users)
		}
	}

	rid := *runID
	if rid == "" {
		rid = strconv.FormatInt(time.Now().Unix(), 36)
	}
	fmt.Printf("== seed：目标 %d 个账号（批次 %s）==\n", *users, rid)

	collected, failCount, firstErr := registerBatch(cli, rid, *users, *password, *conc)
	if len(collected) == 0 {
		return fmt.Errorf("一个账号都没建成，请检查服务端 AUTH_MODE / 验证码 / guest_register_enabled 配置；首个错误：%v", firstErr)
	}
	if failCount > 0 {
		fmt.Printf("  警告：%d 个账号注册失败（首个错误：%v）\n", failCount, firstErr)
	}

	convID, err := buildGroup(cli, collected, *groupName, *chunk, *users)
	if err != nil {
		return err
	}

	sess := &sessionFile{Base: *base, WS: *ws, ConvID: convID, Users: collected}
	if err := saveSession(*sessionPath, sess); err != nil {
		return fmt.Errorf("写会话文件失败：%w", err)
	}
	abs, aerr := filepath.Abs(*sessionPath)
	if aerr != nil {
		abs = *sessionPath
	}
	fmt.Printf("\n完成：%d 个账号，group=%s\n会话文件：%s\n", len(collected), convID, abs)
	fmt.Printf("下一步：go run ./cmd/stress wsload -session %s -clients %d -senders 20 -rate 50 -duration 60s\n",
		abs, len(collected))
	return nil
}

// registerBatch 并发注册账号。返回成功收集到的账号、失败数、首个错误。
func registerBatch(cli *apiClient, rid string, need int, password string, conc int) ([]sessionUser, int64, error) {
	jobs := make(chan int)
	out := make(chan regOutcome, 64)

	var wg sync.WaitGroup
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				acct := fmt.Sprintf("stress_%s_%d", rid, idx)
				u, err := registerOne(cli, acct, password)
				out <- regOutcome{u: u, err: err}
			}
		}()
	}
	go func() {
		for i := 0; i < need; i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(out)
	}()

	var (
		collected []sessionUser
		mu        sync.Mutex
		okCount   int64
		failCount int64
		firstErr  error
		errOnce   sync.Once
		done      int
	)
	for r := range out {
		done++
		if r.err != nil {
			atomic.AddInt64(&failCount, 1)
			errOnce.Do(func() { firstErr = r.err })
		} else {
			atomic.AddInt64(&okCount, 1)
			mu.Lock()
			collected = append(collected, r.u)
			mu.Unlock()
		}
		if done%200 == 0 {
			fmt.Printf("  已处理 %d/%d（成功 %d，失败 %d）\n", done, need, atomic.LoadInt64(&okCount), atomic.LoadInt64(&failCount))
		}
	}
	return collected, atomic.LoadInt64(&failCount), firstErr
}

type regOutcome struct {
	u   sessionUser
	err error
}

// registerOne 注册一个账号。
//
// 注册接口在 AUTH_MODE=sms/email 时会要求验证码/图形码，脚本拿不到；因此失败时
// 自动回落到**游客注册**。游客注册按 deviceId 幂等（同一 deviceId 重复调用复用
// 同一账号），正好适合压测靶场 —— 我们只需要"一个真实存在的 uid + 有效 token"。
func registerOne(c *apiClient, account, password string) (sessionUser, error) {
	nick := "压测"
	if len(account) > 4 {
		nick += account[len(account)-4:]
	}
	data, err := c.call(http.MethodPost, "/auth/register", "", map[string]interface{}{
		"account":  account,
		"password": password,
		"nickname": nick,
	}, nil)
	if err == nil {
		return decodeAuth(data, account)
	}
	firstErr := err

	gdata, gerr := c.call(http.MethodPost, "/auth/guest", "", map[string]interface{}{
		"deviceId":   "stress-" + account,
		"deviceType": 3,
	}, nil)
	if gerr != nil {
		return sessionUser{}, fmt.Errorf("注册 %s 失败：%v；游客注册回落也失败：%v", account, firstErr, gerr)
	}
	return decodeAuth(gdata, account)
}

// decodeAuth 解析登录/注册响应里的 user.id 与 accessToken。
// 注意 user.id 的 json tag 是 `,string`（雪花 ID 超过 2^53，用 JSON number
// 传给 JS 会精度丢失），所以这里按**字符串**取。
func decodeAuth(data json.RawMessage, account string) (sessionUser, error) {
	var d struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return sessionUser{}, fmt.Errorf("解析登录响应失败：%w", err)
	}
	if d.AccessToken == "" {
		return sessionUser{}, errors.New("登录响应缺少 accessToken")
	}
	if d.User.ID == "" || d.User.ID == "0" {
		return sessionUser{}, errors.New("登录响应缺少 user.id")
	}
	return sessionUser{ID: d.User.ID, Account: account, Token: d.AccessToken}, nil
}

// buildGroup 建群并分批补齐成员，返回会话 ID。
//
// 分两步而不是一次性提交上千个 memberIds：
//   - 一次性请求体太大，且一旦撞到群上限就是**整单失败**，看不出上限是多少；
//   - 分批能精确定位"第几个人开始装不下"，从而直接告诉用户 group_max_members 该调多大。
func buildGroup(cli *apiClient, users []sessionUser, groupName string, chunk, target int) (string, error) {
	if len(users) == 0 {
		return "", errors.New("没有可用账号，无法建群")
	}
	owner := users[0]
	first := chunk
	if first > len(users)-1 {
		first = len(users) - 1
	}
	initial := make([]string, 0, first)
	for _, u := range users[1 : 1+first] {
		initial = append(initial, u.ID)
	}

	data, err := cli.call(http.MethodPost, "/conversation/group", owner.Token, map[string]interface{}{
		"nameZh":    groupName,
		"nameEn":    groupName,
		"memberIds": initial,
	}, nil)
	if err != nil {
		if ae, ok := asAPIErr(err); ok && ae.Code == codeGroupFull {
			return "", fmt.Errorf("建群失败：%v —— 服务端 group_max_members 限制了群规模，"+
				"请把它调到 ≥ %d（0 表示不限）后重跑", err, target)
		}
		return "", fmt.Errorf("建群失败：%w", err)
	}
	convID, err := decodeID(data)
	if err != nil {
		return "", fmt.Errorf("建群响应解析失败：%w", err)
	}
	fmt.Printf("  群已创建：convId=%s（初始 %d 人）\n", convID, len(initial)+1)

	rest := users[1+first:]
	for start := 0; start < len(rest); start += chunk {
		end := start + chunk
		if end > len(rest) {
			end = len(rest)
		}
		ids := make([]string, 0, end-start)
		for _, u := range rest[start:end] {
			ids = append(ids, u.ID)
		}
		if _, err := cli.call(http.MethodPost, "/conversation/"+convID+"/invite", owner.Token, map[string]interface{}{
			"memberIds": ids,
		}, nil); err != nil {
			if ae, ok := asAPIErr(err); ok && ae.Code == codeGroupFull {
				return "", fmt.Errorf("邀请第 %d～%d 人失败：%v —— 群规模被 group_max_members 卡住，"+
					"当前只能装约 %d 人；请调大该配置后重跑", start+1, end, err, first+start+1)
			}
			return "", fmt.Errorf("邀请第 %d～%d 人失败：%w", start+1, end, err)
		}
		fmt.Printf("  已邀请 %d/%d\n", first+end+1, len(users))
	}
	return convID, nil
}

// decodeID 从对象响应里取 id。服务端 ID 统一是 json `,string`，这里兼容数字写法。
func decodeID(data json.RawMessage) (string, error) {
	var v struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(data, &v); err != nil || len(v.ID) == 0 {
		return "", fmt.Errorf("响应里没有 id 字段：%.200s", string(data))
	}
	return trimJSONString(v.ID), nil
}

// trimJSONString 去掉 JSON 字符串两端的引号；非字符串原样返回
func trimJSONString(raw json.RawMessage) string {
	s := string(raw)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var out string
		if err := json.Unmarshal(raw, &out); err == nil {
			return out
		}
		return s[1 : len(s)-1]
	}
	return s
}
