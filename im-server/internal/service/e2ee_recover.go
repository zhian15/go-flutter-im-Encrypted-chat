package service

// ============ E2EE 私钥跨设备转移（「申请从旧设备恢复」，2026-09-18）============
//
// 场景：新设备登录后没有本机私钥、且登录密码备份解不开（忘记密码）时，
// 可向同账号的旧设备申请私钥转移：旧设备批准后，用自己的身份私钥解出
// 私钥、再用**新设备临时公钥**包裹回传 —— 私钥明文依旧不经过服务端。
//
// 流程：
//  1. 新设备生成一次性 X25519 密钥对 → POST /e2ee/recover/request
//     {newPub, deviceName}（服务端校验 newPub、user_keys 已建钥）
//  2. 服务端把请求放 Redis（10 分钟 TTL，单用户单待处理）并 PublishEvent
//     `e2ee.recover`（action=request）推给该用户所有在线设备
//  3. 旧设备弹确认窗 → PUT /e2ee/recover/approve {requestId, approve, payload}
//     approve=true 时 payload={ep,n,c}（ECIES 包裹的私钥，格式与消息 kw 一致）
//  4. 新设备轮询 GET /e2ee/recover/status?id=...（2s 一次），approved 时
//     取 payload（**一次性读取**，读后即删）解开私钥并校验与服务端公钥配对
//
// 安全要点：请求/批准/读取都校验 uid 归属；requestId 服务端生成；
// TTL 到期自动失效；payload 只能被读一次。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"
)

const (
	e2eeRecoverTTL    = 10 * time.Minute
	e2eeRecoverPrefix = "e2ee:recover:"

	// RecoverStatusPending / Approved / Rejected 恢复请求状态
	RecoverStatusPending  = "pending"
	RecoverStatusApproved = "approved"
	RecoverStatusRejected = "rejected"
)

// e2eeRecoverReq Redis 里的请求体
type e2eeRecoverReq struct {
	UserID     int64             `json:"userId"`
	NewPub     string            `json:"newPub"`
	DeviceName string            `json:"deviceName"`
	Status     string            `json:"status"`
	Payload    map[string]string `json:"payload,omitempty"` // approved 时：{ep,n,c}
	CreatedAt  int64             `json:"createdAt"`
}

func e2eeRecoverKey(id string) string { return e2eeRecoverPrefix + id }

// E2eeRecoverRequest 新设备发起恢复申请。
func E2eeRecoverRequest(ctx context.Context, uid int64, newPub, deviceName string) (map[string]interface{}, error) {
	if uid <= 0 {
		return nil, errs.ParamError
	}
	if !validE2eePub(newPub) {
		return nil, errs.E2eeKeyInvalid
	}
	// 必须已建钥：没有身份密钥就谈不上「恢复」
	var uk model.UserKey
	if err := store.DB.Where("user_id = ?", uid).First(&uk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.E2eeKeyInvalid
		}
		return nil, err
	}
	if store.RDB == nil {
		return nil, errs.Internal
	}
	// 单用户同时只允许一个待处理请求：有旧的 pending 直接复用其 id（覆盖）
	pendingId, _ := store.RDB.Get(ctx, e2eeRecoverPrefix+"pending:"+itoa64(uid)).Result()
	if pendingId == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		pendingId = hex.EncodeToString(b)
	}
	req := e2eeRecoverReq{
		UserID: uid, NewPub: newPub, DeviceName: deviceName,
		Status: RecoverStatusPending, CreatedAt: time.Now().UnixMilli(),
	}
	b, _ := json.Marshal(req)
	if err := store.RDB.Set(ctx, e2eeRecoverKey(pendingId), string(b), e2eeRecoverTTL).Err(); err != nil {
		return nil, err
	}
	store.RDB.Set(ctx, e2eeRecoverPrefix+"pending:"+itoa64(uid), pendingId, e2eeRecoverTTL)

	// 推给该用户所有在线设备（旧设备批准用）；离线则请求自然过期
	data, _ := json.Marshal(map[string]interface{}{
		"action": "request", "requestId": pendingId,
		"newPub": newPub, "deviceName": deviceName,
		"createdAt": req.CreatedAt,
	})
	if err := PublishEvent(ctx, &Event{Type: "e2ee.recover", ToUIDs: []int64{uid}, Data: data}); err != nil {
		log.Printf("[e2ee-recover] push request failed uid=%d err=%v", uid, err)
	}
	return map[string]interface{}{
		"requestId": pendingId,
		"expiresIn": int(e2eeRecoverTTL.Seconds()),
	}, nil
}

// E2eeRecoverStatus 新设备轮询恢复结果。approved 的 payload 一次性读取（读后即删）。
func E2eeRecoverStatus(ctx context.Context, uid int64, requestId string) (map[string]interface{}, error) {
	if uid <= 0 || requestId == "" {
		return nil, errs.ParamError
	}
	if store.RDB == nil {
		return nil, errs.Internal
	}
	raw, err := store.RDB.Get(ctx, e2eeRecoverKey(requestId)).Result()
	if err != nil {
		// 过期/不存在
		return map[string]interface{}{"status": "expired"}, nil
	}
	var req e2eeRecoverReq
	if json.Unmarshal([]byte(raw), &req) != nil || req.UserID != uid {
		return map[string]interface{}{"status": "expired"}, nil
	}
	out := map[string]interface{}{"status": req.Status}
	if req.Status == RecoverStatusApproved {
		out["payload"] = req.Payload
		// 一次性：读完即删，防止同一密文被反复读取
		store.RDB.Del(ctx, e2eeRecoverKey(requestId), e2eeRecoverPrefix+"pending:"+itoa64(uid))
	}
	return out, nil
}

// E2eeRecoverApprove 旧设备批准/拒绝。approve=true 时必须带 payload（包裹后的私钥）。
func E2eeRecoverApprove(ctx context.Context, uid int64, requestId string, approve bool, payload map[string]string) error {
	if uid <= 0 || requestId == "" {
		return errs.ParamError
	}
	if store.RDB == nil {
		return errs.Internal
	}
	raw, err := store.RDB.Get(ctx, e2eeRecoverKey(requestId)).Result()
	if err != nil {
		return errs.NotFound
	}
	var req e2eeRecoverReq
	if json.Unmarshal([]byte(raw), &req) != nil || req.UserID != uid {
		return errs.NotFound
	}
	if req.Status != RecoverStatusPending {
		return errs.ParamError // 已处理过
	}
	if approve {
		if len(payload) == 0 || payload["c"] == "" || payload["ep"] == "" || payload["n"] == "" {
			return errs.E2eeKeyInvalid
		}
		req.Payload = payload
		req.Status = RecoverStatusApproved
	} else {
		req.Status = RecoverStatusRejected
	}
	b, _ := json.Marshal(req)
	if err := store.RDB.Set(ctx, e2eeRecoverKey(requestId), string(b), e2eeRecoverTTL).Err(); err != nil {
		return err
	}
	return nil
}

// itoa64 int64 → string（本文件内小工具）
func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	buf := [20]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
