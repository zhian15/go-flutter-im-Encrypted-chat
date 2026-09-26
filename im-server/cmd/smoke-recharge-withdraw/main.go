// Smoke test for recharge / withdraw flow.
//
// Usage (after docker + api both stable, run from repo root):
//
//	PS D:\im-project\im-server> go run ./cmd/smoke-recharge-withdraw
//
// It calls: register -> userToken, login admin -> adminToken,
// pay-config get/put, 2x recharge submit (approve / reject),
// save 3 withdraw accounts, 2x withdraw submit (approve / reject),
// and prints final balance / frozen / transaction checks.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"reflect"
	"time"
)

var base = "http://127.0.0.1:8080/api/v1"

type Resp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func httpDo(method, path, token string, body any, out *Resp) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	c := &http.Client{Timeout: 12 * time.Second}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if out == nil {
		out = &Resp{}
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode %s %s: %w  body=%s", method, path, err, string(b))
	}
	return nil
}

func check(label string, r *Resp, wantCode int, wantData map[string]any) error {
	if r.Code != wantCode {
		return fmt.Errorf("FAIL %s: code=%d want=%d msg=%s", label, r.Code, wantCode, r.Message)
	}
	if wantData != nil && len(r.Data) == 0 {
		return fmt.Errorf("FAIL %s: data empty", label)
	}
	if len(wantData) > 0 {
		var got map[string]any
		if err := json.Unmarshal(r.Data, &got); err != nil {
			// maybe list type, skip deep check
			return nil
		}
		for k, want := range wantData {
			gv, ok := got[k]
			if !ok {
				return fmt.Errorf("FAIL %s: key %s missing in data", label, k)
			}
			// Number vs int tolerance
			if wn, ok2 := want.(int); ok2 {
				if gn, ok3 := gv.(float64); ok3 && int(gn) != wn {
					return fmt.Errorf("FAIL %s[%s]=%v want %v", label, k, gv, want)
				}
			} else if !reflect.DeepEqual(fmt.Sprintf("%v", gv), fmt.Sprintf("%v", want)) && fmt.Sprintf("%v", gv) != fmt.Sprintf("%v", want) {
				return fmt.Errorf("FAIL %s[%s]=%v(%T) want %v(%T)", label, k, gv, gv, want, want)
			}
		}
	}
	fmt.Printf("PASS %-60s code=%d\n", label, r.Code)
	return nil
}

var pass = 0
var fails []string

func must(err error) {
	if err != nil {
		fails = append(fails, err.Error())
		log.Printf("  -> %v", err)
	} else {
		pass++
	}
}

func jsonGet(r *Resp, key string) any {
	var m map[string]any
	if err := json.Unmarshal(r.Data, &m); err != nil {
		return nil
	}
	return m[key]
}
func asStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
func asF(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	}
	return 0
}

// Try register; if account already exists fallback to login with same password.
func ensureUser(acct, pass, nick string) (uid string, token string, err error) {
	body := map[string]any{
		"account": acct, "password": pass, "nickname": nick,
		"countryCode": "86", "deviceId": "smoke-1", "deviceType": 6,
	}
	var r Resp
	if err1 := httpDo("POST", "/auth/register", "", body, &r); err1 != nil {
		return "", "", err1
	}
	if r.Code == 0 {
		var d map[string]any
		_ = json.Unmarshal(r.Data, &d)
		u := d["user"].(map[string]any)
		return asStr(u["id"]), asStr(d["accessToken"]), nil
	}
	// account exists / duplicate; try login.
	var r2 Resp
	body2 := map[string]any{"account": acct, "password": pass, "deviceId": "smoke-1", "deviceType": 6}
	if err2 := httpDo("POST", "/auth/login", "", body2, &r2); err2 != nil {
		return "", "", fmt.Errorf("register failed [%d:%s], then login err %w", r.Code, r.Message, err2)
	}
	if r2.Code != 0 {
		return "", "", fmt.Errorf("register failed [%d:%s], login failed [%d:%s]", r.Code, r.Message, r2.Code, r2.Message)
	}
	var d map[string]any
	_ = json.Unmarshal(r2.Data, &d)
	u := d["user"].(map[string]any)
	return asStr(u["id"]), asStr(d["accessToken"]), nil
}

func wallet(userToken string) (bal, fro float64, err error) {
	var r Resp
	if err := httpDo("GET", "/wallet/me", userToken, nil, &r); err != nil {
		return 0, 0, err
	}
	if r.Code != 0 {
		return 0, 0, fmt.Errorf("/wallet/me code=%d msg=%s", r.Code, r.Message)
	}
	var d map[string]any
	if err := json.Unmarshal(r.Data, &d); err != nil {
		return 0, 0, err
	}
	return asF(d["balance"]), asF(d["frozen"]), nil
}

func main() {
	if v := os.Getenv("BASE"); v != "" {
		base = v
	}
	// 1) health / wait-up
	fmt.Println("Waiting api ...")
	up := false
	for i := 0; i < 40; i++ {
		c := &http.Client{Timeout: 2 * time.Second}
		if res, err := c.Get(base + "/wallet/me"); err == nil {
			res.Body.Close()
			up = true
			break
		}
		time.Sleep(800 * time.Millisecond)
	}
	if !up {
		log.Fatalf("api %s not reachable. Please start: cd im-server ; .\\api.exe", base)
	}
	fmt.Println("api up, starting smoke ...")

	// 2) users & admin tokens
	uid1, uTok1, err := ensureUser("smoker1@im.test", "SmokeTest123", "烟民1号")
	must(err)
	fmt.Printf("user1 id=%s\n", uid1)
	uid2, uTok2, err := ensureUser("smoker2@im.test", "SmokeTest123", "烟民2号")
	must(err)
	fmt.Printf("user2 id=%s\n", uid2)

	_, adminTok, err := ensureUser("admin", "Admin@123456", "系统管理员")
	// If admin login failed we also try auth/login with admin directly (it's the same handler path).
	if err != nil {
		// fallback: login explicit with admin
		var r Resp
		err2 := httpDo("POST", "/auth/login", "", map[string]any{
			"account": "admin", "password": "Admin@123456", "deviceId": "smoke-admin-1", "deviceType": 6,
		}, &r)
		if err2 != nil || r.Code != 0 {
			must(fmt.Errorf("admin login failed: %w code=%d msg=%s", err, r.Code, r.Message))
			adminTok = ""
		} else {
			var d map[string]any
			_ = json.Unmarshal(r.Data, &d)
			adminTok = asStr(d["accessToken"])
			err = nil
		}
	}
	must(err)

	// Helper: admin wallet adjust (to give user1 800 / user2 800 start balance so they can withdraw)
	adjust := func(userIdStr string, delta float64, tok, reason string) {
		body := map[string]any{"userId": userIdStr, "delta": delta, "reason": reason}
		var r Resp
		err := httpDo("POST", "/admin/wallet/adjust", tok, body, &r)
		must(err)
		must(check("admin wallet adjust delta="+fmt.Sprintf("%.2f", delta), &r, 0, nil))
	}

	_ = adjust

	// add seed balance
	adjust(uid1, 1000, adminTok, "smoke: seed 1000 for smoker1")
	adjust(uid2, 1000, adminTok, "smoke: seed 1000 for smoker2")

	b10, f10, err := wallet(uTok1)
	must(err)
	fmt.Printf("user1 balance=%.2f frozen=%.2f\n", b10, f10)
	b20, f20, err := wallet(uTok2)
	must(err)
	fmt.Printf("user2 balance=%.2f frozen=%.2f\n", b20, f20)

	// 3) GET pay-config
	var r Resp
	must(httpDo("GET", "/pay/config", uTok1, nil, &r))
	must(check("GET /pay/config", &r, 0, map[string]any{"enabled": true}))

	// 4) Admin PUT pay-config (ensure default, set feeRate=0.01 feeMin=1 min=10 max=50000)
	must(httpDo("PUT", "/admin/pay-config", adminTok, map[string]any{
		"enabled": true,
		"receiveWechatQrcodeUrl": "http://127.0.0.1:9000/im-files/smoke/wx.png",
		"receiveAlipayQrcodeUrl": "",
		"receiveBankQrcodeUrl":   "",
		"receiveBankInfo":        map[string]any{"bankName": "", "cardNo": "", "accountName": ""},
		"rechargeTips":           "冒烟测试：请扫码付款并上传凭证。",
		"withdrawEnabled":        true,
		"withdrawMin":            10,
		"withdrawMax":            50000,
		"withdrawFeeRate":        0.01,
		"withdrawFeeMin":         1,
	}, &r))
	must(check("PUT /admin/pay-config", &r, 0, nil))

	// 5) user1 recharge submit (will approve)
	must(httpDo("POST", "/wallet/recharge/submit", uTok1, map[string]any{
		"amount": 300.55, "payMethod": 1,
		"proofImage": "http://127.0.0.1:9000/im-files/smoke/p1.png",
		"payTxNo":    "SMOKE-WX-001",
		"remark":     "smoke approve",
	}, &r))
	must(check("user1 recharge.submit (to approve)", &r, 0, map[string]any{"status": 1}))
	oidA := asStr(jsonGet(&r, "id"))

	// 6) user1 recharge submit (will reject)
	must(httpDo("POST", "/wallet/recharge/submit", uTok1, map[string]any{
		"amount": 50, "payMethod": 2,
		"proofImage": "http://127.0.0.1:9000/im-files/smoke/p2.png",
		"remark":     "smoke reject",
	}, &r))
	must(check("user1 recharge.submit (to reject)", &r, 0, map[string]any{"status": 1}))
	oidR := asStr(jsonGet(&r, "id"))
	if oidA == oidR {
		must(errors.New("duplicate order id for different recharge orders"))
	}

	// 7) admin approve recharge A
	var ra, rb Resp
	must(httpDo("PUT", "/admin/recharge-orders/"+oidA+"/approve", adminTok, nil, &ra))
	must(check("admin approve recharge A", &ra, 0, map[string]any{"orderId": oidA, "userId": uid1}))
	balAfter := asF(jsonGet(&ra, "balance"))
	wantA := b10 + 300.55
	if fmt.Sprintf("%.2f", balAfter) != fmt.Sprintf("%.2f", wantA) {
		must(fmt.Errorf("after recharge approve balance=%.2f want %.2f", balAfter, wantA))
	} else {
		pass++
		fmt.Printf("PASS balance after approve=%.2f matches %.2f\n", balAfter, wantA)
	}

	// 8) admin reject recharge R
	must(httpDo("PUT", "/admin/recharge-orders/"+oidR+"/reject", adminTok, map[string]any{
		"reason": "smoke:截图模糊，看不到交易金额",
	}, &rb))
	must(check("admin reject recharge R", &rb, 0, nil))
	// balance unchanged
	bAft, _, err := wallet(uTok1)
	must(err)
	if fmt.Sprintf("%.2f", bAft) != fmt.Sprintf("%.2f", wantA) {
		must(fmt.Errorf("after reject balance=%.2f want %.2f (reject must not add)", bAft, wantA))
	} else {
		pass++
		fmt.Printf("PASS after reject balance unchanged=%.2f\n", bAft)
	}

	// 9) save 3 withdraw account types (3 PUTS)
	must(httpDo("PUT", "/wallet/withdraw-account", uTok1, map[string]any{
		"accountType":    1,
		"wechatName":     "张三",
		"wechatQrcodeUrl": "http://127.0.0.1:9000/im-files/smoke/wd1_wx.png",
	}, &r))
	must(check("save withdraw-account (wechat)", &r, 0, nil))
	must(httpDo("PUT", "/wallet/withdraw-account", uTok1, map[string]any{
		"accountType":    2,
		"alipayAccount":  "smoker1@qq.com",
		"alipayName":     "张四",
		"alipayQrcodeUrl": "http://127.0.0.1:9000/im-files/smoke/wd1_ali.png",
	}, &r))
	must(check("save withdraw-account (alipay)", &r, 0, nil))
	must(httpDo("PUT", "/wallet/withdraw-account", uTok1, map[string]any{
		"accountType":     3,
		"bankName":        "招商银行",
		"bankCardNo":      "6225 8888 8888 1001",
		"bankAccountName": "张五",
	}, &r))
	must(check("save withdraw-account (bank)", &r, 0, nil))

	// 10) user2 save bank + submit withdraw (will be approved via bank)
	must(httpDo("PUT", "/wallet/withdraw-account", uTok2, map[string]any{
		"accountType":     3,
		"bankName":        "工商银行",
		"bankCardNo":      "6222 0000 0000 2002",
		"bankAccountName": "烟二",
	}, &r))
	must(check("user2 save withdraw-account (bank)", &r, 0, nil))

	// Current wallet user2
	b21, f21, err := wallet(uTok2)
	must(err)
	// Apply withdraw amount=200 => fee = max(200*0.01, 1) = 2; actual=198
	must(httpDo("POST", "/wallet/withdraw/submit", uTok2, map[string]any{
		"amount": 200, "withdrawType": 3,
	}, &r))
	must(check("user2 withdraw.submit (will approve, bank 200)", &r, 0, map[string]any{"amount": 200.0, "fee": 2.0, "actualAmount": 198.0, "status": 1}))
	wdA := asStr(jsonGet(&r, "id"))

	// Check wallet immediately: balance -= 200, frozen += 200
	b22, f22, err := wallet(uTok2)
	must(err)
	wantB := b21 - 200
	wantF := f21 + 200
	if fmt.Sprintf("%.2f", b22) != fmt.Sprintf("%.2f", wantB) || fmt.Sprintf("%.2f", f22) != fmt.Sprintf("%.2f", wantF) {
		must(fmt.Errorf("after withdraw submit: bal=%.2f want %.2f / fro=%.2f want %.2f", b22, wantB, f22, wantF))
	} else {
		pass++
		fmt.Printf("PASS after withdraw submit: bal=%.2f fro=%.2f\n", b22, f22)
	}

	// 11) user2 submit 2nd withdraw (to be rejected) => amount=100 => fee=1 actual=99
	must(httpDo("POST", "/wallet/withdraw/submit", uTok2, map[string]any{
		"amount": 100, "withdrawType": 3,
	}, &r))
	must(check("user2 withdraw.submit (will reject, 100)", &r, 0, map[string]any{"amount": 100.0, "fee": 1.0, "actualAmount": 99.0, "status": 1}))
	wdR := asStr(jsonGet(&r, "id"))
	b23, f23, err := wallet(uTok2)
	must(err)
	wantB2 := wantB - 100
	wantF2 := wantF + 100
	if fmt.Sprintf("%.2f", b23) != fmt.Sprintf("%.2f", wantB2) || fmt.Sprintf("%.2f", f23) != fmt.Sprintf("%.2f", wantF2) {
		must(fmt.Errorf("after 2nd withdraw submit: bal=%.2f want %.2f / fro=%.2f want %.2f", b23, wantB2, f23, wantF2))
	} else {
		pass++
		fmt.Printf("PASS after 2nd withdraw submit: bal=%.2f fro=%.2f\n", b23, f23)
	}

	// 12) admin approve withdraw wdA
	must(httpDo("PUT", "/admin/withdraw-orders/"+wdA+"/approve", adminTok, nil, &ra))
	must(check("admin approve withdraw wdA", &ra, 0, map[string]any{"orderId": wdA, "userId": uid2, "amount": 200.0, "fee": 2.0}))
	// After approve: frozen -= 200, balance -= 2
	b24, f24, err := wallet(uTok2)
	must(err)
	wantB3 := wantB2 - 2  // fee
	wantF3 := wantF2 - 200 // unfreeze
	if fmt.Sprintf("%.2f", b24) != fmt.Sprintf("%.2f", wantB3) || fmt.Sprintf("%.2f", f24) != fmt.Sprintf("%.2f", wantF3) {
		must(fmt.Errorf("after withdraw approve: bal=%.2f want %.2f / fro=%.2f want %.2f", b24, wantB3, f24, wantF3))
	} else {
		pass++
		fmt.Printf("PASS after withdraw approve: bal=%.2f fro=%.2f\n", b24, f24)
	}

	// 13) admin reject withdraw wdR
	must(httpDo("PUT", "/admin/withdraw-orders/"+wdR+"/reject", adminTok, map[string]any{
		"reason": "smoke: 单笔低于 200 不给提",
	}, &rb))
	must(check("admin reject withdraw wdR", &rb, 0, nil))
	// After reject: frozen -= 100, balance += 100 (refund)
	b25, f25, err := wallet(uTok2)
	must(err)
	wantB4 := wantB3 + 100
	wantF4 := wantF3 - 100
	if fmt.Sprintf("%.2f", b25) != fmt.Sprintf("%.2f", wantB4) || fmt.Sprintf("%.2f", f25) != fmt.Sprintf("%.2f", wantF4) {
		must(fmt.Errorf("after withdraw reject: bal=%.2f want %.2f / fro=%.2f want %.2f", b25, wantB4, f25, wantF4))
	} else {
		pass++
		fmt.Printf("PASS after withdraw reject: bal=%.2f fro=%.2f\n", b25, f25)
	}

	// 14) admin list orders sanity check
	must(httpDo("GET", "/admin/recharge-orders?kw=烟民&size=5", adminTok, nil, &r))
	must(check("admin /admin/recharge-orders search kw=烟民", &r, 0, nil))
	must(httpDo("GET", "/admin/withdraw-orders?type=3&size=5", adminTok, nil, &r))
	must(check("admin /admin/withdraw-orders type=3", &r, 0, nil))

	fmt.Printf("\n========== SMOKE SUMMARY: PASS=%d  FAIL=%d ==========\n", pass, len(fails))
	for i, f := range fails {
		fmt.Printf("FAIL[%d] %s\n", i+1, f)
	}
	if len(fails) > 0 {
		os.Exit(1)
	}
}
