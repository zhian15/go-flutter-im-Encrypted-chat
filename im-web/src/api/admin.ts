import { http } from './http'
import type { ApiResp } from './auth'

// 管理后台 API
export const adminApi = {
  // 用户管理（role: 1用户 / 2管理员 / 3客服，0或不传=全部；inviteCode=按注册/绑定邀请码精确搜索；inviterId=查某人邀请了谁）
  users: (params: { kw?: string; status?: number; dept?: number; role?: number; inviteCode?: string; inviterId?: number | string; page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/users', { params }),
  // 创建账号（2026-09-25）：账号/手机号/邮箱三选一（后端强校验）；
  // 头像空→App默认头像，shortId 空→自动分配随机靓号，密码空→123456
  userCreate: (payload: { account?: string; phone?: string; email?: string; password?: string; nickname?: string; avatar?: string; shortId?: string; departmentId?: number; role?: number }) =>
    http.post<ApiResp>('/admin/users', payload),
  // 批量生成账号（按一键注册规则：g+随机账号/随机中文昵称/自动靓号/默认头像，密码固定 123456）
  userBatchCreate: (count: number) =>
    http.post<ApiResp<{ count: number; list: Array<{ account: string; password: string; nickname: string; shortId: string }> }>>('/admin/users/batch', { count }),
  userUpdate: (id: number, payload: { nickname?: string; avatar?: string; role?: number; shortId?: string | number }) =>
    http.put<ApiResp>(`/admin/users/${id}`, payload),
  userStatus: (id: number, status: number) => http.put<ApiResp>(`/admin/users/${id}/status`, { status }),
  userResetPwd: (id: number, password: string) => http.put<ApiResp>(`/admin/users/${id}/password`, { password }),
  // 充值（B-24）：服务端原子入账，返回落库后的真实余额，前端不得自行推算
  userRecharge: (id: number, amount: number, remark?: string) =>
    http.post<ApiResp<{ balance: number }>>(`/admin/users/${id}/recharge`, { amount, remark }),
  userWallet: (id: number) => http.get<ApiResp<{ userId: number; balance: number; frozen: number }>>(`/admin/users/${id}/wallet`),
  // 用户详情（查看详情弹窗）：资料 + 累计充值/提现 + 注册 IP/设备 + 统计
  userDetail: (id: number | string) =>
    http.get<ApiResp<Record<string, any>>>(`/admin/users/${id}/detail`),
  // 用户登录设备列表（含 IP 与信任状态，账户安全验证保底展示）：返回 {devices}
  // devices 项字段见 DeviceSession：deviceId/deviceType/deviceName/lastIp/lastActiveAt/online/isCurrent/trusted
  userDevices: (id: number | string) =>
    http.get<ApiResp<{ devices: Array<Record<string, any>> }>>(`/admin/users/${id}/devices`),
  // 清除用户全部设备信任标记（强制下次重新登录验证，不会锁死账号）：返回 {cleared}
  clearTrust: (id: number | string) =>
    http.post<ApiResp<{ cleared: number }>>(`/admin/users/${id}/devices/clear-trust`, {}),

  // 部门管理
  departments: () => http.get<ApiResp<Array<Record<string, any>>>>('/admin/departments'),
  deptCreate: (payload: { nameZh: string; nameEn?: string; parentId?: number; sort?: number }) =>
    http.post<ApiResp>('/admin/departments', payload),
  deptUpdate: (id: number, payload: { nameZh?: string; nameEn?: string; sort?: number }) =>
    http.put<ApiResp>(`/admin/departments/${id}`, payload),
  deptDelete: (id: number) => http.delete<ApiResp>(`/admin/departments/${id}`),

  // 系统配置
  configGet: (key: string) => http.get<ApiResp>(`/admin/configs/${key}`),
  configSet: (key: string, value: unknown) => http.put<ApiResp>(`/admin/configs/${key}`, { value }),

  // 自定义邀请码（一码关联多好友，注册自动加好友）
  inviteCodes: () => http.get<ApiResp<Array<Record<string, any>>>>('/admin/invite-friend-codes'),
  inviteCodeCreate: (payload: { code: string; friendIds: string[]; remark?: string }) =>
    http.post<ApiResp>('/admin/invite-friend-codes', payload),
  inviteCodeUpdate: (id: string | number, payload: { code?: string; friendIds?: string[]; remark?: string; enabled?: number }) =>
    http.put<ApiResp>(`/admin/invite-friend-codes/${id}`, payload),
  inviteCodeDelete: (id: string | number) => http.delete<ApiResp>(`/admin/invite-friend-codes/${id}`),

  // 智能小助手
  assistantConfigGet: () => http.get<ApiResp<Record<string, any>>>('/admin/assistant/config'),
  assistantConfigSet: (payload: { enabled: boolean; name: string; avatar?: string; autoAdd?: boolean; welcomeText?: string }) =>
    http.post<ApiResp>('/admin/assistant/config', payload),
  assistantPush: (payload: { userIds: string[]; userId?: string; content?: string; fileUrl?: string }) =>
    http.post<ApiResp>('/admin/assistant/push', payload),
  // 返回 {list,total}：list=按最近消息倒序的前 limit 条，total=会话总数
  assistantConversations: (params?: { limit?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/assistant/conversations', { params }),
  assistantMessages: (params: { userId: string; beforeMsgId?: string | number; limit?: number }) =>
    http.get<ApiResp<Array<Record<string, any>>>>('/admin/assistant/messages', { params }),

  // 小程序管理（H5 容器）
  apps: () => http.get<ApiResp<Array<Record<string, any>>>>('/admin/app-entries'),
  appCreate: (payload: { nameZh: string; nameEn?: string; icon?: string; url: string; category?: string; sort?: number; enabled?: boolean }) =>
    http.post<ApiResp>('/admin/app-entries', payload),
  appUpdate: (id: number, payload: { nameZh?: string; nameEn?: string; icon?: string; url?: string; category?: string; sort?: number; enabled?: boolean }) =>
    http.put<ApiResp>(`/admin/app-entries/${id}`, payload),
  appDelete: (id: number) => http.delete<ApiResp>(`/admin/app-entries/${id}`),

  // 群组管理（后端已改为 {list,total} 并支持 kw 搜索 + 分页）
  groups: (params?: { kw?: string; page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/groups', { params }),
  groupDisband: (id: string) => http.delete<ApiResp>(`/admin/groups/${id}`),
  // 编辑群资料：仅传需要改的字段（name / avatar / announcement / maxMembers）
  groupUpdate: (id: string, payload: { name?: string; avatar?: string; announcement?: string; maxMembers?: number }) =>
    http.put<ApiResp>(`/admin/groups/${id}`, payload),
  // 转移群主：newOwnerId 必须是该群成员
  groupTransferOwner: (id: string, newOwnerId: number | string) =>
    http.put<ApiResp>(`/admin/groups/${id}/owner`, { newOwnerId }),
  groupMembers: (groupId: string, params?: { page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>(`/admin/groups/${groupId}/members`, { params }),
  groupMessages: (groupId: string, params?: { kw?: string; page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>(`/admin/groups/${groupId}/messages`, { params }),

  // 朋友圈管理（点赞/评论明细在 list 项的 likes/comments 字段）
  moments: (params?: { page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/moments', { params }),
  momentCreate: (payload: { content: string; images?: string[] }) =>
    http.post<ApiResp<Record<string, any>>>('/admin/moments', payload),
  momentHidden: (id: string | number, hidden: boolean) =>
    http.put<ApiResp>(`/admin/moments/${id}/hidden`, { hidden }),
  momentDelete: (id: string | number) =>
    http.delete<ApiResp>(`/admin/moments/${id}`),
  momentCommentDelete: (commentId: string | number) =>
    http.delete<ApiResp>(`/admin/moments/comments/${commentId}`),

  // 消息记录（审计）。userKw：用户 ID / 昵称 / 账号 / 手机号（后端先反查用户再过滤 sender_id）
  messages: (params: { kw?: string; convId?: string; userId?: string; userKw?: string; type?: number; from?: number; to?: number; page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/messages', { params }),
  // 屏蔽/恢复消息（blocked=false 恢复）
  messageBlock: (msgId: string, blocked = true) =>
    http.post<ApiResp>(`/admin/messages/${msgId}/block`, { blocked }),
  // 管理员下载任意文件（无需是群成员）：返回签名直链，前端直接 window.open / a[download]
  adminFileDownload: (fileId: string) =>
    http.get<ApiResp<{ url: string; name: string; size: number; mime: string }>>(`/admin/files/${fileId}/download`),

  // 数据统计
  statsOverview: () => http.get<ApiResp<Record<string, any>>>('/admin/stats/overview'),
  statsMessages: (days = 7) => http.get<ApiResp<{ days: number; series: Array<{ day: string; count: number }> }>>('/admin/stats/messages', { params: { days } }),

  // 清空数据（危险操作：scope = users/chats/groups/recharge/withdraw/all；前端必须二次确认后再调用）
  dataClear: (scope: string) =>
    http.post<ApiResp<Record<string, any>>>('/admin/data/clear', { scope }),

  // 日志
  logs: (params: { page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/logs', { params }),
  loginLogs: (params: { page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/logs/login', { params }),

  // 保留靓号
  reservedShortIds: (params: { kw?: string; status?: number; source?: number; page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/reserved-short-ids', { params }),
  reservedShortIdsBatch: (payload: {
    // 三种模式：range 范围 / list 列表 / rule 规则
    mode: 'range' | 'list' | 'rule' | 'manual' | string
    from?: number | string | bigint
    to?: number | string | bigint
    prefix?: string
    digits?: number
    count?: number
    list?: string[]
    remark?: string
    price?: number
    source?: number
    type?: number          // 1 普通 / 2 豹子号 / 3 顺子号 / 4 VIP
    ids?: string[]         // 兼容旧前端：ids 与 list 等价
  }) =>
    http.post<ApiResp<{ added: number; count: number }>>('/admin/reserved-short-ids/batch', payload),
  reservedShortIdRemark: (id: number | string, payload: { remark?: string; price?: number; type?: number }) =>
    http.put<ApiResp>(`/admin/reserved-short-ids/${id}/remark`, payload),
  reservedShortIdFrozen: (id: number | string, frozen: boolean) =>
    http.put<ApiResp>(`/admin/reserved-short-ids/${id}/frozen`, { frozen }),
  reservedShortIdDelete: (id: number | string) =>
    http.delete<ApiResp>(`/admin/reserved-short-ids/${id}`),
  // 分配给用户 / 解除分配（绑定账号列会对应更新）
  reservedShortIdAssign: (id: number | string, userId: number | string) =>
    http.put<ApiResp<{ userId?: number | string; nickname?: string; account?: string; shortId?: string }>>(
      `/admin/reserved-short-ids/${id}/assign`,
      { userId }
    ),
  reservedShortIdRelieve: (id: number | string) =>
    http.put<ApiResp>(`/admin/reserved-short-ids/${id}/relieve`, {}),

  // 系统健康检测
  healthCheck: (key: string) =>
    http.get<ApiResp<Record<string, any>>>(`/admin/health/${key}`),

  // 服务重启（systemd 托管环境）：target = api | gateway
  systemRestart: (target: 'api' | 'gateway') =>
    http.post<ApiResp<null>>('/admin/system/restart', { target }),

  /**
   * 后台文件上传：POST /api/v1/admin/upload（admin 接口，admin token 鉴权）。
   * 走 axios 而非 fetch，确保拦截器统一带 Authorization + 401 自动刷新；
   * 历史 bug：直接用 fetch('/api/v1/upload') 走用户接口，部分反向代环境下
   * FormData body 被吞，后端 FormFile("file") 拿不到 → 返回 1001「缺少文件」。
   * 改走 admin 接口 + axios 后该问题消失。返回 MinIO URL 字符串。
   *
   * 再次踩坑：显式 { 'Content-Type': 'multipart/form-data' } 会**覆盖** axios 自动加的 boundary，
   * 后端 c.Request.FormFile("file") 因缺少 boundary 解析失败 → 同样 1001。
   * 修复：删掉显式 headers，让 axios 检测到 FormData 时自动补齐 Content-Type（含 boundary）。
   */
  uploadFile: (file: File, dir = 'common/') => {
    const fd = new FormData()
    fd.append('file', file)
    fd.append('dir', dir)
    return http.post<ApiResp<{ url: string; object: string; size: number }>>(
      '/admin/upload',
      fd
    ).then(r => {
      if (r.data.code !== 0) throw new Error(r.data.message || '上传失败')
      return r.data.data.url
    })
  },

  // 财务
  financeRecords: (params: { kw?: string; type?: string; side?: string; page?: number; size?: number; from?: number; to?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/finances', { params }),

  // ===== 支付配置 / 充值订单 / 提现订单 =====
  payConfigGet: () =>
    http.get<ApiResp<{
      enabled: boolean
      receiveWechatQrcodeUrl: string
      receiveAlipayQrcodeUrl: string
      receiveBankQrcodeUrl: string
      receiveBankInfo: { bankName: string; cardNo: string; accountName: string }
      rechargeTips: string
      withdrawEnabled: boolean
      withdrawMin: number
      withdrawMax: number
      withdrawFeeRate: number
      withdrawFeeMin: number
    }>>('/admin/pay-config'),
  payConfigSet: (payload: Record<string, unknown>) => http.put<ApiResp>('/admin/pay-config', payload),

  rechargeOrders: (params: { kw?: string; status?: number; page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/recharge-orders', { params }),
  rechargeOrderApprove: (id: number | string) =>
    http.put<ApiResp<{ orderId: number | string; userId: number | string; amount: number; balance: number }>>(
      `/admin/recharge-orders/${id}/approve`,
      {}
    ),
  rechargeOrderReject: (id: number | string, reason: string) =>
    http.put<ApiResp>(`/admin/recharge-orders/${id}/reject`, { reason }),

  withdrawOrders: (params: { kw?: string; status?: number; type?: number; page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/withdraw-orders', { params }),
  withdrawOrderApprove: (id: number | string) =>
    http.put<ApiResp<{ orderId: number | string; userId: number | string; amount: number; fee: number }>>(
      `/admin/withdraw-orders/${id}/approve`,
      {}
    ),
  withdrawOrderReject: (id: number | string, reason: string) =>
    http.put<ApiResp>(`/admin/withdraw-orders/${id}/reject`, { reason }),

  // 网页白名单（功能 B 后台管理）：链接卡片命中白名单后才会走应用内 WebView 壳
  //
  // 契约注意：enabled / nativeBridge 必须传 **int 1/0**，不能传 boolean。
  // 历史 bug：前端传 true/false → 后端结构体字段是 int → JSON 绑定失败 → 直接 400「参数错误」。
  // logo / cover 为图片 URL（后台上传得到的 MinIO 地址）；传空字符串表示清空，
  // 命中白名单时后端会用它们覆盖聊天卡片的图标与封面（未配置则回落 favicon / og:image）。
  // 出参是 **{list,total}**（见 handler/admin_whitelist.go:34），不是裸数组。
  // 历史 bug：这里断言成 Array、页面直接 data.data.map(...) → 运行时
  // "xxx.map is not a function" → 列表永远空白，还会把刚成功的保存提示翻成「保存失败」。
  // 查询参数名是 keyword（不是 kw）：后端 c.Query("keyword")，传 kw 会让搜索框静默失效。
  webWhitelistList: (params?: { keyword?: string }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/web-whitelist', { params }),
  webWhitelistCreate: (payload: { domain: string; displayName: string; enabled?: number; nativeBridge?: number; logo?: string; cover?: string }) =>
    http.post<ApiResp>('/admin/web-whitelist', payload),
  webWhitelistUpdate: (id: number, payload: { domain?: string; displayName?: string; enabled?: number; nativeBridge?: number; logo?: string; cover?: string }) =>
    http.put<ApiResp>(`/admin/web-whitelist/${id}`, payload),
  webWhitelistDelete: (id: number) =>
    http.delete<ApiResp>(`/admin/web-whitelist/${id}`),

  // ===== 投诉/举报管理 =====
  // 列表：status 0 待处理 / 1 已处理（不传 = 全部）
  // 列表项字段（以 im-server/doc/API.md「投诉」节为准）：id、reporter_id、peer_id、conv_id、
  //   category、note、status、created_at、举报人/被投诉人昵称
  reports: (params: { status?: number; page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/reports', { params }),
  // 标记已处理。契约给的是 POST；若后端落成 PUT，改这一行即可。
  reportHandle: (id: number | string) => http.post<ApiResp>(`/admin/reports/${id}/handle`, {}),

  // 会话列表（按 type 过滤：1私聊 2群聊 3频道）。用于「默认关注频道」下拉拉 type=3 的频道。
  // 返回 {list,total}；频道/群 ID 均为雪花 ID，前端必须按字符串处理。
  adminConversations: (params: { type?: number; kw?: string; page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/conversations', { params })
}

export const adminLogApi = {
  logs: (params: { page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/logs', { params }),
  loginLogs: (params: { page?: number; size?: number }) =>
    http.get<ApiResp<{ list: Array<Record<string, any>>; total: number }>>('/admin/logs/login', { params })
}
