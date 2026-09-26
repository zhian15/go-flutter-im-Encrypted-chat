package service

// ============ E2EE 端到端加密（2026-09-18 §36 定稿）============
//
// 架构：X25519 身份密钥（私钥仅本机）+ 每单聊随机 AES-256-GCM 会话密钥
//（对方公钥包裹下发，消息 type=13，见 model.MsgE2Text）。
// 私钥备份：客户端用登录密码派生 KEK（PBKDF2 + HKDF "e2ee-kek-v1"）加密后
// 存本表 encrypted_private_key —— **服务端只存密文，无法解密**。
//
// 服务端职责只有四件：
//  1. 存公钥 + 备份密文（user_keys，migrations/008_e2ee.sql）；
//  2. 把密文原样发给属主 / 把公钥发给任意登录用户（发消息前要取对方公钥）；
//  3. 改密码时同事务收新备份密文（re-wrap，拒绝「密码已改、备份没换」）；
//  4. 重置密码 = 烧备份（删 user_keys 行，§36 拍板 A）。
//
// 加解密/签封/拆封全部在客户端完成，服务端永远接触不到明文与私钥。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"
)

// E2EE 模式（sys_config 键 e2ee_mode，后台「加密方式」三选，拍板②）：
//
//	off    —— 关闭（不加密）
//	server —— 服务端可解密（预留旧 e2e_enabled 的语义，本期不实现加解密）
//	e2ee   —— 端到端（服务端不可解密），用户级开关默认开（拍板③）
const (
	E2eeModeOff    = "off"
	E2eeModeServer = "server"
	E2eeModeE2EE   = "e2ee"
)

// E2eeMode 读取当前加密方式。未配置时按旧 e2e_enabled 布尔开关推导（向后兼容）。
func E2eeMode(ctx context.Context) string {
	m, _ := SysConfigGet(ctx, "e2ee_mode", "").(string)
	switch m {
	case E2eeModeOff, E2eeModeServer, E2eeModeE2EE:
		return m
	}
	// 兼容：老后台只配过 e2e_enabled 布尔值
	if boolVal(SysConfigGet(ctx, "e2e_enabled", false)) {
		return E2eeModeServer
	}
	return E2eeModeOff
}

// validE2eePub 校验 X25519 公钥：base64 标准编码解码后必须 32 字节。
func validE2eePub(pub string) bool {
	if pub == "" || len(pub) > 128 {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(pub)
	return err == nil && len(b) == 32
}

// validE2eeEncPriv 校验私钥备份密文：客户端产出的 JSON，必须带 ct（AES-GCM 密文）
// 与 pub（与私钥配对的公钥，便于客户端核对），整体 ≤ 8KB。
func validE2eeEncPriv(s string) bool {
	if s == "" || len(s) > 8*1024 {
		return false
	}
	var m map[string]interface{}
	if json.Unmarshal([]byte(s), &m) != nil {
		return false
	}
	ct, _ := m["ct"].(string)
	pk, _ := m["pub"].(string)
	return ct != "" && pk != ""
}

// E2eePubKeyOut GET /e2ee/pubkey/:uid 的返回：给发消息方取对方公钥用。
type E2eePubKeyOut struct {
	UserID     int64  `json:"userId,string"`
	PublicKey  string `json:"publicKey"`
	E2eeOn     bool   `json:"e2eeOn"`
	KeyVersion int    `json:"keyVersion"`
}

// E2eePubKey 查某用户的端到端公钥（无密钥行时 PublicKey 为空串）。
func E2eePubKey(ctx context.Context, uid int64) (*E2eePubKeyOut, error) {
	if uid <= 0 {
		return nil, errs.ParamError
	}
	out := &E2eePubKeyOut{UserID: uid, E2eeOn: false}
	var uk model.UserKey
	if err := store.DB.Where("user_id = ?", uid).First(&uk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, nil // 没建钥：客户端按「对方不支持端到端」降级普通文本
		}
		return nil, err
	}
	out.PublicKey = uk.PublicKey
	out.E2eeOn = uk.E2eeOn == 1
	out.KeyVersion = uk.KeyVersion
	return out, nil
}

// E2eeMeOut GET /e2ee/me：本人密钥状态（登录后自动建钥 / 换设备解锁都先调它）。
type E2eeMeOut struct {
	Mode       string `json:"mode"`    // off / server / e2ee（后台加密方式）
	HasKeys    bool   `json:"hasKeys"` // 是否已生成身份密钥对
	PublicKey  string `json:"publicKey"`
	E2eeOn     bool   `json:"e2eeOn"`            // 用户级开关（默认开）
	HasBackup  bool   `json:"hasBackup"`         // 是否有私钥备份密文
	EncPriv    string `json:"encPriv,omitempty"` // 备份密文原样下发（换设备解锁用，仅属主可见）
	KeyVersion int    `json:"keyVersion"`
}

// E2eeMe 本人密钥状态。
func E2eeMe(ctx context.Context, uid int64) (*E2eeMeOut, error) {
	out := &E2eeMeOut{Mode: E2eeMode(ctx), E2eeOn: true}
	var uk model.UserKey
	if err := store.DB.Where("user_id = ?", uid).First(&uk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, nil
		}
		return nil, err
	}
	out.HasKeys = uk.PublicKey != ""
	out.PublicKey = uk.PublicKey
	out.E2eeOn = uk.E2eeOn == 1
	out.HasBackup = uk.EncryptedPrivateKey != ""
	out.EncPriv = uk.EncryptedPrivateKey
	out.KeyVersion = uk.KeyVersion
	return out, nil
}

// E2eePutKeys 生成/轮换身份密钥对（PUT /e2ee/keys）。
// 客户端在「登录后自动建钥」或「解锁失败重置」时调用；行存在则整行覆盖（轮换，
// key_version +1 —— 旧公钥包裹的历史消息将无法解密，客户端 UI 需自行警示）。
func E2eePutKeys(ctx context.Context, uid int64, pub, encPriv string) error {
	if !validE2eePub(pub) || !validE2eeEncPriv(encPriv) {
		return errs.E2eeKeyInvalid
	}
	var uk model.UserKey
	err := store.DB.Where("user_id = ?", uid).First(&uk).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return store.DB.Create(&model.UserKey{
			UserID:              uid,
			PublicKey:           pub,
			EncryptedPrivateKey: encPriv,
			KeyVersion:          1,
			E2eeOn:              1,
		}).Error
	}
	if err != nil {
		return err
	}
	return store.DB.Model(&uk).Updates(map[string]interface{}{
		"public_key":            pub,
		"encrypted_private_key": encPriv,
		"key_version":           gorm.Expr("key_version + 1"),
	}).Error
}

// E2eePutBackup 只更新私钥备份密文（PUT /e2ee/backup）：改密码 re-wrap 时调用，
// 公钥不变（身份密钥对不轮换，历史消息继续可解）。
func E2eePutBackup(ctx context.Context, uid int64, encPriv string) error {
	if !validE2eeEncPriv(encPriv) {
		return errs.E2eeKeyInvalid
	}
	res := store.DB.Model(&model.UserKey{}).Where("user_id = ?", uid).
		Updates(map[string]interface{}{
			"encrypted_private_key": encPriv,
			"key_version":           gorm.Expr("key_version + 1"),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.NotFound
	}
	return nil
}

// E2eeToggle 用户级开关（PUT /e2ee/toggle，拍板③：默认开、可关）。
func E2eeToggle(ctx context.Context, uid int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	res := store.DB.Model(&model.UserKey{}).Where("user_id = ?", uid).
		Update("e2ee_on", v)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.NotFound
	}
	return nil
}

// E2eeBurnBackup 烧私钥备份（§36 拍板 A）：删整行（公钥一并作废）。
// 触发点：管理员重置密码（AdminUserResetPassword）/ 将来的忘记密码重置流程。
// 后果：老设备本地私钥仍可解旧消息；新设备需生成**新**密钥对，
//
//	联系人端提示「对方密钥已变更」，旧历史在新设备永久不可解。
func E2eeBurnBackup(ctx context.Context, uid int64) error {
	return store.DB.Where("user_id = ?", uid).Delete(&model.UserKey{}).Error
}
