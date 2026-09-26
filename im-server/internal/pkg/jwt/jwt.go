package jwt

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims JWT 载荷
type Claims struct {
	UserID int64 `json:"uid"`
	Role   int   `json:"role"`
	// Ver 令牌版本：与 user.token_version 比对，改密码/被禁用/管理员重置密码后 +1，
	// 令所有已签发的 access/refresh token 立即失效（存量令牌无该声明，解析为 0）。
	Ver int64 `json:"v,omitempty"`
	// DeviceID 签发设备号：仅 refresh token 写入，用于多设备独立槽位与精确吊销单设备。
	DeviceID string `json:"did,omitempty"`
	jwt.RegisteredClaims
}

// generate 内部统一签发实现（Generate / GenerateForDevice 共用，避免复制两份逻辑）
func generate(secret string, userID int64, role int, ttlHours int, ver int64, deviceID string) (string, error) {
	claims := Claims{
		UserID:   userID,
		Role:     role,
		Ver:      ver,
		DeviceID: deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(ttlHours) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "im-server",
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString([]byte(secret))
}

// Generate 签发 access token（短期）。
// deviceID：自 2026-09-24 起 access token 也携带设备号（此前仅 refresh token 携带），
// 用于「设备级令牌失效」——被注销/单设备登出后，middleware.Auth 与 WS 握手可据此复核
// 该设备的 refresh 槽位是否仍存在，已吊销则拒绝（见 service 的 device-slot 复核）。
// 老 token 无 did 字段（解析为 ""），各环节对 did=="" 一律放行，兼容存量会话自然过期。
func Generate(secret string, userID int64, role int, ttlHours int, ver int64, deviceID string) (string, error) {
	return generate(secret, userID, role, ttlHours, ver, deviceID)
}

// GenerateForDevice 签发 refresh token（长期，携带设备号与令牌版本）
func GenerateForDevice(secret string, userID int64, role int, ttlHours int, ver int64, deviceID string) (string, error) {
	return generate(secret, userID, role, ttlHours, ver, deviceID)
}

// Parse 解析并校验 access token
func Parse(secret, tokenStr string) (*Claims, error) {
	t, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := t.Claims.(*Claims)
	if !ok || !t.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
