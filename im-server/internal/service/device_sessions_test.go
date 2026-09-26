package service

import "testing"

// 设备在线判定（两级联动）纯函数单测——不依赖 DB/Redis。
// 背景用户实测（App 设备页）：当前设备恒显示「离线」；本测试锁定
// resolveDeviceOnline 的两级判定语义：设备号粒度命中 → 在线；
// 设备号未登记但平台级集合有该平台 → 在线（兜底）；都没有 → 离线。
func TestResolveDeviceOnline(t *testing.T) {
	deviceOnline := map[string]struct{}{"dev-abc": {}}                 // onlinedev:{uid} 字段集
	platformOnline := map[string]struct{}{"android": {}}               // online:{uid} 成员集（小写，见 ws.go deviceNameOf）
	otherPlatform := map[string]struct{}{"windows": {}}                // 平台级有别的平台
	cases := []struct {
		name           string
		deviceID       string
		platform       string
		deviceOnline   map[string]struct{}
		platformOnline map[string]struct{}
		want           bool
	}{
		{"设备级命中-在线", "dev-abc", "Android", deviceOnline, map[string]struct{}{}, true},
		{"设备级未登记-平台级兜底-在线", "dev-xyz", "Android", map[string]struct{}{}, platformOnline, true},
		// online:{uid} 成员由 ws.go deviceNameOf 写入、恒为小写；设备行 platform 来自
		// deviceName（首字母大写）——resolveDeviceOnline 负责把入参转小写后比对
		{"设备行大写平台名-集合小写-兜底在线", "dev-xyz", "iOS", map[string]struct{}{}, map[string]struct{}{"ios": {}}, true},
		{"两级都没有-离线", "dev-xyz", "iOS", map[string]struct{}{}, otherPlatform, false},
		{"平台级只有其它平台-离线", "dev-xyz", "Android", map[string]struct{}{}, otherPlatform, false},
		{"空设备号-平台级兜底-在线", "", "Android", map[string]struct{}{}, platformOnline, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveDeviceOnline(c.deviceID, c.platform, c.deviceOnline, c.platformOnline); got != c.want {
				t.Errorf("resolveDeviceOnline(%q,%q) = %v, want %v", c.deviceID, c.platform, got, c.want)
			}
		})
	}
}

// 本机标记：请求头匹配 / 请求头缺失兜底最近活跃行 / 请求头不命中不错标。
func TestApplyCurrent(t *testing.T) {
	t.Run("请求头命中-精确标记", func(t *testing.T) {
		list := []DeviceSession{{DeviceID: "dev-a"}, {DeviceID: "dev-b"}}
		applyCurrent(list, "dev-b")
		if list[0].IsCurrent || list[1].IsCurrent != true || list[1].Current != true {
			t.Errorf("dev-b 应被标记本机: %+v", list)
		}
		// Current 与 IsCurrent 必须同值（旧字段兼容）
		if list[1].Current != list[1].IsCurrent {
			t.Errorf("current/isCurrent 应同值: %+v", list[1])
		}
	})
	t.Run("请求头缺失-兜底标记最近活跃第一行", func(t *testing.T) {
		list := []DeviceSession{{DeviceID: "dev-a"}, {DeviceID: "dev-b"}}
		applyCurrent(list, "")
		if !list[0].IsCurrent || list[1].IsCurrent {
			t.Errorf("空请求头应只标记第一行（lastActiveAt 倒序的最近活跃行）: %+v", list)
		}
	})
	t.Run("请求头不命中-不错标其它行", func(t *testing.T) {
		// 请求头非空但 0 命中：请求方设备无槽位本就不在列表里，错标会误导用户
		list := []DeviceSession{{DeviceID: "dev-a"}, {DeviceID: "dev-b"}}
		applyCurrent(list, "dev-other")
		for _, d := range list {
			if d.IsCurrent {
				t.Errorf("不应错标: %+v", d)
			}
		}
	})
	t.Run("空列表不 panic", func(t *testing.T) {
		applyCurrent(nil, "")
	})
}
