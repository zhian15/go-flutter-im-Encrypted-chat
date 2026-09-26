package errs

// 统一错误码：服务端只返回 code，界面文案由客户端按语言渲染
var (
	Success      = &Err{Code: 0, Msg: "ok"}
	ParamError   = &Err{Code: 1001, Msg: "参数错误"}
	Unauthorized = &Err{Code: 1002, Msg: "未登录或登录过期"}
	Forbidden    = &Err{Code: 1003, Msg: "无权限"}
	Banned       = &Err{Code: 1004, Msg: "您当前已经被封禁"}

	AccountExists = &Err{Code: 2001, Msg: "账号已存在"}
	CodeInvalid   = &Err{Code: 2002, Msg: "验证码错误或过期"}
	InviteInvalid = &Err{Code: 2003, Msg: "邀请码无效"}
	RegisterOff   = &Err{Code: 2004, Msg: "注册已关闭"}
	LoginFailed   = &Err{Code: 2005, Msg: "账号或密码错误"}
	// IPBanned 密码错误 5 次自动拉黑客户端 IP 24h（防同 IP 换号爆破）。
	// 信息不泄露"还剩几次"，仅提示已被封锁，避免辅助攻击者。
	IPBanned = &Err{Code: 2006, Msg: "您的 IP 因多次登录失败已被临时封锁，请 24 小时后再试"}
	GuestOff      = &Err{Code: 2007, Msg: "游客注册未开启"}

	FriendReqNotFound = &Err{Code: 3001, Msg: "好友申请不存在"}

	// NotFriends 私聊好友门控：非好友不得新建单聊会话（需求「对方同意后才能聊天」）。
	// 与 3002（已是好友）/3003（申请已发送）同族，前端文案渲染「先加好友」引导。
	NotFriends = &Err{Code: 3006, Msg: "对方不是你的好友，请先发送好友申请"}

	// ShortIDTaken 频道自定义唯一 ID（conversation.short_id）已被占用：
	// 全表唯一，一个自定义 ID 只能属于一个会话（创建频道时查重返回）。
	ShortIDTaken = &Err{Code: 3008, Msg: "自定义 ID 已被使用"}

	ConvNotFound  = &Err{Code: 4001, Msg: "会话不存在或非成员"}
	GroupFull     = &Err{Code: 4002, Msg: "群人数已达上限"}
	RecallDenied  = &Err{Code: 4003, Msg: "撤回超时或无权限"}
	GroupMutedAll = &Err{Code: 4004, Msg: "群已开启全员禁言"}
	MemberMuted   = &Err{Code: 4005, Msg: "你已被禁言，无法发言"}
	MemberPrivacy = &Err{Code: 4006, Msg: "群主已开启成员隐私"}
	// ChannelOwnerOnly 频道（type=3）发言限制：只有频道主能发言，普通成员只读。
	ChannelOwnerOnly = &Err{Code: 4007, Msg: "只有频道主可以发言"}

	// ChannelDisabled 频道功能开关（sys_config channel_enabled）关闭：
	// 后台关闭后创建频道一律拒绝（App 新建频道入口同步隐藏，此处兜底旧版本客户端）。
	ChannelDisabled = &Err{Code: 4031, Msg: "频道功能已关闭"}

	// 会话文件夹（个人视图分组，PC 归档/文件夹需求）：4008 族
	FolderNotFound     = &Err{Code: 4008, Msg: "文件夹不存在"}
	FolderNameRequired = &Err{Code: 4009, Msg: "请填写文件夹名称"}

	FileTooLarge = &Err{Code: 5001, Msg: "文件过大或类型不允许"}

	// SeqUnavailable：会话序号水位不可用（Redis 写失败）。
	// 客户端收到后应提示「系统繁忙，请稍后重试」并保留气泡可重试。
	// 绝不能像旧实现那样退化成纳秒时间戳——那会产出一个 1.76e18 的水位，
	// 让客户端之后永远 sync 不到新消息（R-04）。
	SeqUnavailable = &Err{Code: 5002, Msg: "系统繁忙，请稍后重试"}

	// 群文件云盘 & 链接卡片（功能 A / B）相关错误码
	NotFound        = &Err{Code: 404, Msg: "资源不存在"}
	SSRFForbidden   = &Err{Code: 403, Msg: "该链接地址不允许访问"}
	DomainExists    = &Err{Code: 409, Msg: "域名已存在"}
	ConvertFailed   = &Err{Code: 409, Msg: "文件预览转换失败"}
	ConvertTooLarge = &Err{Code: 422, Msg: "文件过大，不支持预览转换（上限 50MB）"}
	TooManyRequests = &Err{Code: 429, Msg: "操作过于频繁，请稍后再试"}

	CallRoomNotFound = &Err{Code: 6001, Msg: "通话房间不存在"}

	RateLimited = &Err{Code: 7001, Msg: "操作过于频繁"}

	// 支付密码相关（资金安全：发红包/转账冻结前校验）
	PayPwdNotSet   = &Err{Code: 4301, Msg: "请先设置支付密码"}
	PayPwdWrong    = &Err{Code: 4302, Msg: "支付密码错误"}
	PayPwdOldWrong = &Err{Code: 4303, Msg: "原支付密码错误"}
	PayPwdFormat   = &Err{Code: 4304, Msg: "支付密码需为 6 位数字"}

	// E2EE 端到端加密（2026-09-18 §36 定稿）：4401 族
	// E2eeBackupRequired 改密码时已有私钥备份但未同步 re-wrap 新密文
	//（防「密码已改、备份没换」→ 换设备后备份永久解不开）。
	E2eeBackupRequired = &Err{Code: 4401, Msg: "请同步更新端到端加密备份"}
	E2eeKeyInvalid     = &Err{Code: 4402, Msg: "端到端密钥格式无效"}

	// E2EE 跨设备恢复（申请从旧设备恢复）：4403 族
	E2eeRecoverBusy    = &Err{Code: 4403, Msg: "已有进行中的恢复申请"}
	E2eeRecoverExpired = &Err{Code: 4404, Msg: "恢复申请已过期或不存在"}

	// 账户安全验证（登录设备批准，2026-09-23）：4101 族
	VerifyCodeWrong    = &Err{Code: 4101, Msg: "验证码错误或已失效"}
	VerifyExpired      = &Err{Code: 4102, Msg: "验证会话已过期，请重新登录"}
	VerifyTooMany      = &Err{Code: 4103, Msg: "验证尝试次数过多，请重新登录"}
	VerifyNotApprover  = &Err{Code: 4104, Msg: "非审批设备，无法操作"}
	VerifyRejected     = &Err{Code: 4105, Msg: "登录请求已被拒绝"}
	VerifyAssistantOff = &Err{Code: 4106, Msg: "小助手未开启，无法使用验证码登录"}
	VerifyNoApprover   = &Err{Code: 4107, Msg: "没有可用的审批设备"}
	VerifyCodeExpired  = &Err{Code: 4108, Msg: "验证码已失效，请重新获取"}
	VerifyResendLimit  = &Err{Code: 4109, Msg: "验证码发送次数过多，请明日再试"}

	Internal = &Err{Code: 500, Msg: "服务内部错误"}
)

type Err struct {
	Code int    `json:"code"`
	Msg  string `json:"message"`
}

func (e *Err) Error() string { return e.Msg }
