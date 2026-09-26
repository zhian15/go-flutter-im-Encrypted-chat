package service

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/pkg/id"
	"github.com/yourcompany/im-server/internal/store"

	"gorm.io/gorm"
)

// ============ 自定义邀请码（后台创建，一码关联多好友，注册自动加好友） ============

// InviteFriendCodeOut 邀请码输出（附带好友昵称，后台列表展示用）
type InviteFriendCodeOut struct {
	model.InviteFriendCode
	FriendNames []string `json:"friendNames"` // 关联好友的昵称列表
}

// InviteFriendList 后台邀请码列表（按创建时间倒序，附带好友昵称）
func InviteFriendList(ctx context.Context) ([]InviteFriendCodeOut, error) {
	var list []model.InviteFriendCode
	if err := store.DB.Order("id desc").Limit(500).Find(&list).Error; err != nil {
		return nil, err
	}
	out := make([]InviteFriendCodeOut, 0, len(list))
	for _, ic := range list {
		item := InviteFriendCodeOut{InviteFriendCode: ic, FriendNames: []string{}}
		for _, fid := range parseFriendIDs(ic.FriendIDs) {
			var u model.User
			if err := store.DB.Select("nickname").First(&u, fid).Error; err == nil {
				item.FriendNames = append(item.FriendNames, u.Nickname)
			} else {
				item.FriendNames = append(item.FriendNames, "#"+strconv.FormatInt(fid, 10))
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// InviteFriendCreate 后台创建邀请码（自定义码 + 关联好友 ID 列表，好友 ID 为字符串形式的 int64）
func InviteFriendCreate(ctx context.Context, code string, friendIDStrs []string, remark string) (*model.InviteFriendCode, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, &errs.Err{Code: 1001, Msg: "邀请码不能为空"}
	}
	if len(code) > 32 {
		return nil, &errs.Err{Code: 1001, Msg: "邀请码最长 32 位"}
	}
	fids, err := parseFriendIDStrs(friendIDStrs)
	if err != nil {
		return nil, err
	}
	if len(fids) == 0 {
		return nil, &errs.Err{Code: 1001, Msg: "至少关联一位好友"}
	}
	var cnt int64
	store.DB.Model(&model.InviteFriendCode{}).Where("code = ?", code).Count(&cnt)
	if cnt > 0 {
		return nil, &errs.Err{Code: 1001, Msg: "邀请码已存在"}
	}
	ic := model.InviteFriendCode{
		ID:        id.Next(),
		Code:      code,
		FriendIDs: marshalFriendIDs(fids),
		Remark:    remark,
		Enabled:   1,
	}
	if err := store.DB.Create(&ic).Error; err != nil {
		return nil, err
	}
	return &ic, nil
}

// InviteFriendUpdate 后台更新邀请码（code/friendIDs/remark/enabled，nil 表示不改）
func InviteFriendUpdate(ctx context.Context, cid int64, code *string, friendIDStrs []string, remark *string, enabled *int) error {
	var ic model.InviteFriendCode
	if err := store.DB.First(&ic, cid).Error; err != nil {
		return &errs.Err{Code: 1001, Msg: "邀请码不存在"}
	}
	updates := map[string]interface{}{}
	if code != nil && strings.TrimSpace(*code) != "" {
		updates["code"] = strings.TrimSpace(*code)
	}
	if friendIDStrs != nil {
		fids, err := parseFriendIDStrs(friendIDStrs)
		if err != nil {
			return err
		}
		if len(fids) == 0 {
			return &errs.Err{Code: 1001, Msg: "至少关联一位好友"}
		}
		updates["friend_ids"] = marshalFriendIDs(fids)
	}
	if remark != nil {
		updates["remark"] = *remark
	}
	if enabled != nil {
		if *enabled == 1 {
			updates["enabled"] = 1
		} else {
			updates["enabled"] = 0
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return store.DB.Model(&ic).Updates(updates).Error
}

// InviteFriendDelete 后台删除邀请码
func InviteFriendDelete(ctx context.Context, cid int64) error {
	return store.DB.Delete(&model.InviteFriendCode{}, cid).Error
}

// InviteFriendCodeValid 该码是否为有效的自定义好友邀请码（启用中）。
// 用于「邀请码注册」开关开启时：一次性邀请码不匹配 → 回退校验自定义邀请码（多用不限次）。
func InviteFriendCodeValid(ctx context.Context, code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	var cnt int64
	store.DB.Model(&model.InviteFriendCode{}).Where("code = ? AND enabled = 1", strings.TrimSpace(code)).Count(&cnt)
	return cnt > 0
}

// InviteFriendBindForRegister 注册成功后按邀请码自动添加关联好友（双向关系，失败仅返回错误由调用方 log，不阻断注册）
func InviteFriendBindForRegister(ctx context.Context, code string, userID int64) error {
	code = strings.TrimSpace(code)
	if code == "" || userID <= 0 {
		return nil
	}
	var ic model.InviteFriendCode
	if err := store.DB.Where("code = ? AND enabled = 1", code).First(&ic).Error; err != nil {
		return nil // 不是自定义邀请码（可能是一次性邀请码），静默跳过
	}
	fids := parseFriendIDs(ic.FriendIDs)
	bound := 0
	for _, fid := range fids {
		if err := kefuBind(ctx, userID, fid); err != nil {
			log.Printf("[invite] bind user %d <-> friend %d failed: %v", userID, fid, err)
			continue
		}
		bound++
	}
	if bound > 0 {
		store.DB.Model(&ic).UpdateColumn("used_count", ic.UsedCount+1)
	}
	return nil
}

// resolveInviterByCode 按邀请码反查上级用户 ID：
// 先按 user.my_invite_code 精确匹配，再退一步忽略大小写匹配；找不到返回 0。
// excludeUID > 0 时排除该用户自己（防止填自己的邀请码导致自邀请）。
//
// 说明：查库失败只记日志并返回 0，不阻断注册/绑定流程——此时 invited_code 仍会照常记录，
// 后台按码能搜到该用户，只是显示不出上级。
func resolveInviterByCode(ctx context.Context, code string, excludeUID int64) int64 {
	code = strings.TrimSpace(code)
	if code == "" {
		return 0
	}
	// 每次都从 store.DB 重新起链，避免复用 *gorm.DB 导致条件累积
	newQuery := func() *gorm.DB {
		q := store.DB.WithContext(ctx).Model(&model.User{})
		if excludeUID > 0 {
			q = q.Where("id <> ?", excludeUID)
		}
		return q
	}
	// 1) 精确匹配（取 id 最小的一条，保证同一码被多人误用时结果稳定）
	var inviter model.User
	err := newQuery().Where("my_invite_code = ?", code).Order("id asc").Select("id").First(&inviter).Error
	if err == nil && inviter.ID > 0 {
		return inviter.ID
	}
	// 2) 退一步：忽略大小写匹配
	var lower model.User
	err = newQuery().Where("LOWER(my_invite_code) = LOWER(?)", code).Order("id asc").Select("id").First(&lower).Error
	if err == nil && lower.ID > 0 {
		return lower.ID
	}
	return 0
}

// parseFriendIDs 解析 friend_ids JSON 数组字符串 → []int64（容错：坏数据返回空）
func parseFriendIDs(raw string) []int64 {
	var arr []int64
	if raw == "" {
		return arr
	}
	// UseNumber：大整数（雪花级用户 ID）以字符串形式保留，经 float64 转换会丢精度
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var tmp []interface{}
	if err := dec.Decode(&tmp); err != nil {
		return arr
	}
	for _, v := range tmp {
		switch n := v.(type) {
		case json.Number:
			if f, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
				arr = append(arr, f)
			}
		case string:
			if f, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
				arr = append(arr, f)
			}
		}
	}
	return arr
}

// marshalFriendIDs []int64 → JSON 数组字符串
func marshalFriendIDs(ids []int64) string {
	strs := make([]string, 0, len(ids))
	for _, v := range ids {
		strs = append(strs, strconv.FormatInt(v, 10))
	}
	b, _ := json.Marshal(strs)
	return string(b)
}

// parseFriendIDStrs 字符串 ID 列表 → []int64（雪花 ID 走 JSON 字符串传输，避免 JS 精度丢失）
func parseFriendIDStrs(strs []string) ([]int64, error) {
	out := make([]int64, 0, len(strs))
	seen := map[int64]bool{}
	for _, s := range strs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, &errs.Err{Code: 1001, Msg: "好友 ID 格式错误: " + s}
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}
