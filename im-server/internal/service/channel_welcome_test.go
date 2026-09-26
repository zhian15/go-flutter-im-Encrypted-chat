package service

import "testing"

// 频道欢迎语占位符渲染纯函数单测——不依赖 DB/Redis。
// 背景：后台「聊天设置→默认关注频道→招呼内容」支持四个占位符：
// {nickname}/{订阅人昵称}（订阅用户昵称）、{channelName}/{频道名}（频道名），
// 英文为原始写法、中文为后台文案别名，全量替换、可出现多次；
// 模板含昵称占位符但昵称查不到/为空时整体降级默认文案（ok=false，不硬发半渲染内容）。
func TestRenderChannelWelcome(t *testing.T) {
	cases := []struct {
		name        string
		tpl         string
		nickname    string
		channelName string
		want        string
		wantOK      bool
	}{
		{"英文昵称占位符", "欢迎 {nickname} 关注", "小明", "官方频道", "欢迎 小明 关注", true},
		{"中文昵称别名-订阅人昵称", "欢迎 {订阅人昵称} 关注", "小明", "官方频道", "欢迎 小明 关注", true},
		{"英文频道名占位符", "欢迎关注 {channelName}", "小明", "官方频道", "欢迎关注 官方频道", true},
		{"中文频道名别名", "欢迎关注 {频道名}", "小明", "官方频道", "欢迎关注 官方频道", true},
		{"四占位符混用且重复出现", "{nickname} 你好，欢迎关注 {channelName}，{订阅人昵称} 请多指教 {频道名}", "小明", "官方频道", "小明 你好，欢迎关注 官方频道，小明 请多指教 官方频道", true},
		{"无占位符-模板原样输出", "欢迎关注本频道", "小明", "官方频道", "欢迎关注本频道", true},
		{"仅频道名占位符-昵称为空不降级", "欢迎关注 {channelName}", "", "官方频道", "欢迎关注 官方频道", true},
		{"昵称占位符-昵称为空-降级", "欢迎 {nickname} 关注", "", "官方频道", "", false},
		{"昵称中文别名-昵称为空-降级", "欢迎 {订阅人昵称} 关注", "", "官方频道", "", false},
		{"昵称占位符-纯空白昵称视为空-降级", "欢迎 {nickname} 关注", "   ", "官方频道", "", false},
		{"昵称占位符-无占位符原文-查询失败不影响", "欢迎关注本频道", "", "官方频道", "欢迎关注本频道", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := renderChannelWelcome(c.tpl, c.nickname, c.channelName)
			if ok != c.wantOK {
				t.Errorf("renderChannelWelcome(%q, %q, %q) ok = %v, want %v", c.tpl, c.nickname, c.channelName, ok, c.wantOK)
			}
			if got != c.want {
				t.Errorf("renderChannelWelcome(%q, %q, %q) = %q, want %q", c.tpl, c.nickname, c.channelName, got, c.want)
			}
		})
	}
}
