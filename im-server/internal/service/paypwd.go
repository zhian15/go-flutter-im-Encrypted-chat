package service

import (
	"context"
	"regexp"
	"strings"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/store"

	"golang.org/x/crypto/bcrypt"
)

// payPwdRe 支付密码格式：6 位数字（与主流支付产品一致；纯数字便于手机端 PIN 键盘输入）
var payPwdRe = regexp.MustCompile(`^\d{6}$`)

// HasPayPwd 是否已设置支付密码（供 profile / 前端判断是否提示去设置）
func HasPayPwd(ctx context.Context, userID int64) (bool, error) {
	var u model.User
	if err := store.DB.Select("pay_pwd_hash").First(&u, userID).Error; err != nil {
		return false, errs.Unauthorized
	}
	return strings.TrimSpace(u.PayPwdHash) != "", nil
}

// SetPayPwd 首次设置支付密码（无需校验旧密码）
func SetPayPwd(ctx context.Context, userID int64, newPwd string) error {
	if !payPwdRe.MatchString(newPwd) {
		return errs.PayPwdFormat
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPwd), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := store.DB.Model(&model.User{}).Where("id = ?", userID).
		Update("pay_pwd_hash", string(hash)).Error; err != nil {
		return err
	}
	return nil
}

// ChangePayPwd 修改支付密码（需校验原密码）
func ChangePayPwd(ctx context.Context, userID int64, oldPwd, newPwd string) error {
	var u model.User
	if err := store.DB.Select("pay_pwd_hash").First(&u, userID).Error; err != nil {
		return errs.Unauthorized
	}
	if strings.TrimSpace(u.PayPwdHash) == "" {
		return errs.PayPwdNotSet
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PayPwdHash), []byte(oldPwd)) != nil {
		return errs.PayPwdOldWrong
	}
	if !payPwdRe.MatchString(newPwd) {
		return errs.PayPwdFormat
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPwd), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := store.DB.Model(&model.User{}).Where("id = ?", userID).
		Update("pay_pwd_hash", string(hash)).Error; err != nil {
		return err
	}
	return nil
}

// VerifyPayPwd 校验支付密码（发红包/转账冻结前调用）。
// 未设置 → 4301（提示去设置）；错误 / 格式不符 → 4302。
// 校验点唯一且强制：客户端无法绕过（金额以服务端为准）。
func VerifyPayPwd(ctx context.Context, userID int64, pwd string) error {
	var u model.User
	if err := store.DB.Select("pay_pwd_hash").First(&u, userID).Error; err != nil {
		return errs.Unauthorized
	}
	if strings.TrimSpace(u.PayPwdHash) == "" {
		return errs.PayPwdNotSet
	}
	if !payPwdRe.MatchString(pwd) {
		return errs.PayPwdWrong
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PayPwdHash), []byte(pwd)) != nil {
		return errs.PayPwdWrong
	}
	return nil
}
