<template>
  <div class="page">
    <!-- 概览卡片 -->
    <div class="stats">
      <a-card class="stat-card" v-for="(s, i) in summary" :key="i">
        <div class="stat-icon" :style="{ background: s.bg, color: s.fg }">
          <component :is="s.icon" />
        </div>
        <div class="stat-body">
          <div class="stat-label">{{ s.label }}</div>
          <div class="stat-value" :style="{ color: s.fg }">{{ s.value }}</div>
          <div class="stat-sub muted">{{ s.sub }}</div>
        </div>
      </a-card>
    </div>

    <a-card>
      <div class="toolbar">
        <a-input-search v-model="query.kw" placeholder="搜索单号 / 账户 / 用户" style="width: 260px" allow-clear @search="loadSafe(1)" />
        <a-select v-model="query.side" placeholder="收支方向" allow-clear style="width: 140px" @change="loadSafe(1)">
          <a-option value="IN">收入</a-option>
          <a-option value="OUT">支出</a-option>
          <a-option value="FREEZE">冻结</a-option>
        </a-select>
        <a-select v-model="query.type" placeholder="交易类型" allow-clear style="width: 180px" @change="loadSafe(1)">
          <a-option v-for="(label, v) in typeMap" :key="v" :value="v">{{ label }}</a-option>
        </a-select>
        <a-date-picker v-model="dateRange" type="daterange" value-format="YYYY-MM-DD" style="width: 260px" />
        <a-button type="primary" @click="loadSafe(1)">查询</a-button>
        <a-button style="margin-left:auto" :icon="IconDownload" @click="exportCSV">导出 CSV</a-button>
      </div>

      <a-table :data="list" row-key="id" :pagination="pagination" :loading="loading" @page-change="loadSafe">
        <template #columns>
          <a-table-column title="单号" data-index="orderNo" :width="220" ellipsis>
            <template #cell="{ record }">
              <span class="mono-chip">{{ record.orderNo || '-' }}</span>
            </template>
          </a-table-column>
          <a-table-column title="时间" :width="170">
            <template #cell="{ record }">{{ fmt(record.createdAt) }}</template>
          </a-table-column>
          <a-table-column title="收支" :width="90">
            <template #cell="{ record }">
              <a-tag :color="sideColor(record.side)">{{ sideText(record.side) }}</a-tag>
            </template>
          </a-table-column>
          <a-table-column title="类型" :width="150">
            <template #cell="{ record }">
              <span class="type-chip">
                <component :is="typeIcon(record.type)" />
                {{ typeMap[record.type] || record.type }}
              </span>
            </template>
          </a-table-column>
          <a-table-column title="用户" :width="210">
            <template #cell="{ record }">
              <div class="user-cell">
                <span class="avatar" :style="{ background: avatarColor(record.userId ?? 0) }">
                  <img v-if="record.userAvatar" :src="record.userAvatar" alt="" />
                  <template v-else>{{ (record.userNickname || 'U').slice(0, 1).toUpperCase() }}</template>
                </span>
                <div class="user-info">
                  <span class="nickname">{{ record.userNickname || '-' }}</span>
                  <span class="account muted">{{ record.userAccount || record.userId }}</span>
                </div>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="金额" :width="140" align="right">
            <template #cell="{ record }">
              <span class="amount" :class="record.side">
                {{ record.side === 'OUT' ? '-' : record.side === 'FREEZE' ? '±' : '+' }} ¥{{ fmtMoney(record.amount) }}
              </span>
            </template>
          </a-table-column>
          <a-table-column title="余额后" :width="140" align="right">
            <template #cell="{ record }">
              <span class="mono muted">¥{{ fmtMoney(record.balanceAfter ?? record.balance) }}</span>
            </template>
          </a-table-column>
          <a-table-column title="备注" ellipsis>
            <template #cell="{ record }">
              <span :title="record.remark || ''">{{ record.remark || '-' }}</span>
            </template>
          </a-table-column>
          <a-table-column title="状态" :width="100">
            <template #cell="{ record }">
              <a-tag v-if="record.status === 1" color="green">成功</a-tag>
              <a-tag v-else-if="record.status === 2" color="orange">处理中</a-tag>
              <a-tag v-else-if="record.status === 3" color="red">失败</a-tag>
              <a-tag v-else color="gray">{{ record.status ?? '成功' }}</a-tag>
            </template>
          </a-table-column>
        </template>
      </a-table>
    </a-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, watch, computed, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import { Message } from '@arco-design/web-vue'
import {
  IconFile, IconGift, IconSend, IconUser, IconSwap, IconSafe, IconDownload
} from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'

const route = useRoute()
const _mounting = ref(true)
const _loadingOnce = ref(false)
const _lastDateRangeKey = ref('')

const list = ref<Array<Record<string, any>>>([])
const loading = ref(false)
const dateRange = ref<Array<string | number> | undefined>(undefined)
const query = reactive({ kw: '', side: undefined as string | undefined, type: undefined as string | undefined })
const pagination = reactive({ current: 1, pageSize: 15, total: 0, showTotal: true })

const typeMap: Record<string, string> = {
  RECHARGE: '充值',
  WITHDRAW: '提现',
  TRANSFER: '转账',
  REDPACKET: '红包',
  VIP_PAY: '会员购买',
  MERCHANT: '商户结算',
  REFUND: '退款',
  FEE: '手续费',
  FREEZE: '冻结/解冻',
  OTHER: '其他'
}

const AVATAR_COLORS = ['#4E8CFF', '#7B61FF', '#FF7D00', '#00B42A', '#F53F3F', '#14C9C9', '#B37FEC']
function avatarColor(id: number) {
  return { backgroundColor: AVATAR_COLORS[Math.abs(Number(id) || 0) % AVATAR_COLORS.length] }
}
function fmt(v: string | number | Date) {
  if (!v) return '-'
  const d = new Date(v)
  if (isNaN(+d)) return String(v)
  const p = (n: number) => n.toString().padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function fmtMoney(v: any) {
  const n = Number(v) || 0
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}
function sideText(v: string) {
  if (v === 'IN') return '收入'
  if (v === 'OUT') return '支出'
  if (v === 'FREEZE') return '冻结'
  return v || '-'
}
function sideColor(v: string) {
  if (v === 'IN') return 'green'
  if (v === 'OUT') return 'red'
  if (v === 'FREEZE') return 'orange'
  return 'gray'
}
function typeIcon(t: string) {
  switch (t) {
    case 'RECHARGE': return IconGift
    case 'WITHDRAW': return IconSend
    case 'TRANSFER': return IconSwap
    case 'REDPACKET': return IconGift
    case 'VIP_PAY': return IconUser
    case 'MERCHANT': return IconFile
    case 'REFUND': return IconSwap
    case 'FEE': return IconFile
    case 'FREEZE': return IconSafe
    default: return IconFile
  }
}

// 汇总（mock 累计）
const summary = computed(() => {
  const all = list.value
  const inAmt = all.filter(r => r.side === 'IN').reduce((a, b) => a + (Number(b.amount) || 0), 0)
  const outAmt = all.filter(r => r.side === 'OUT').reduce((a, b) => a + (Number(b.amount) || 0), 0)
  return [
    { label: '今日收入', value: `¥ ${fmtMoney(inAmt)}`, sub: `本页共 ${all.filter(r => r.side === 'IN').length} 笔`, icon: IconGift, bg: '#e8ffea', fg: '#00b42a' },
    { label: '今日支出', value: `¥ ${fmtMoney(outAmt)}`, sub: `本页共 ${all.filter(r => r.side === 'OUT').length} 笔`, icon: IconSend, bg: '#ffece8', fg: '#f53f3f' },
    { label: '净增加', value: `¥ ${fmtMoney(inAmt - outAmt)}`, sub: `合计笔数 ${all.length}`, icon: IconSwap, bg: '#eef0ff', fg: '#4b3cff' },
    { label: '冻结金额', value: `¥ ${fmtMoney(all.filter(r => r.side === 'FREEZE').reduce((a, b) => a + (Number(b.amount) || 0), 0))}`, sub: '本页冻结记录', icon: IconSafe, bg: '#fff7e6', fg: '#ad6800' }
  ]
})

// ===== 以下 4 个函数已废弃（B-24），请勿再调用 =====
// 它们服务于「接口 404 → 用随机 mock 数据顶上」的旧逻辑，
// 那套逻辑让财务页长期显示跟数据库无关的假账。现在财务页只读服务端
// /admin/finances（wallet_transaction）。保留仅为不破坏构建，后续可安全删除。
const FINANCE_META_KEY = 'admin_finance_meta'
async function loadFinanceMeta(): Promise<Array<Record<string, any>>> {
  try {
    const { data } = await adminApi.configGet(FINANCE_META_KEY)
    if (data && data.code === 0 && data.data) {
      try {
        const arr = JSON.parse(String(data.data))
        return Array.isArray(arr) ? arr : []
      } catch { return [] }
    }
  } catch { /* ignore */ }
  return []
}
async function saveFinanceMeta(arr: Array<Record<string, any>>) {
  try { await adminApi.configSet(FINANCE_META_KEY, JSON.stringify(arr || [])) } catch { /* ignore */ }
}
function fmtMoneyLocal(v: any) {
  const n = Number(v) || 0
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

// ===== Mock 财务记录（已废弃，见上方说明）=====
function buildMockFinanceRecords(): Array<Record<string, any>> {
  const users = [
    { id: 100000, nickname: '林墨白', account: 'user1001', shortId: 10000 },
    { id: 100001, nickname: '苏晚晴', account: 'user1002', shortId: 10001 },
    { id: 100002, nickname: '陈星河', account: 'user1003', shortId: 10002 },
    { id: 100003, nickname: '沈逸舟', account: 'user1004', shortId: 10003 },
    { id: 100004, nickname: '顾长卿', account: 'user1005', shortId: 10004 },
    { id: 100005, nickname: '江雪柠', account: 'user1006', shortId: 10005 },
    { id: 100006, nickname: '周慕白', account: 'user1007', shortId: 10006 },
    { id: 100007, nickname: '徐知夏', account: 'user1008', shortId: 10007 }
  ]
  const types = [
    { type: 'RECHARGE', side: 'IN', weight: 10, min: 10, max: 2000, remark: '充值', status: 1 },
    { type: 'WITHDRAW', side: 'OUT', weight: 6, min: 50, max: 5000, remark: '提现', status: 1 },
    { type: 'TRANSFER', side: 'OUT', weight: 8, min: 1, max: 500, remark: '转账给好友', status: 1 },
    { type: 'REDPACKET', side: 'OUT', weight: 14, min: 1, max: 200, remark: '发红包', status: 1 },
    { type: 'REDPACKET', side: 'IN', weight: 12, min: 1, max: 200, remark: '收红包', status: 1 },
    { type: 'VIP_PAY', side: 'OUT', weight: 5, min: 30, max: 388, remark: '会员购买', status: 1 },
    { type: 'REFUND', side: 'IN', weight: 2, min: 20, max: 500, remark: '退款', status: 1 },
    { type: 'FEE', side: 'OUT', weight: 8, min: 1, max: 20, remark: '手续费', status: 1 },
    { type: 'FREEZE', side: 'FREEZE', weight: 2, min: 50, max: 1000, remark: '违规冻结', status: 1 },
    { type: 'MERCHANT', side: 'IN', weight: 3, min: 100, max: 5000, remark: '商户结算', status: 1 },
    { type: 'OTHER', side: 'IN', weight: 3, min: 5, max: 100, remark: '其他收入', status: 1 },
    { type: 'OTHER', side: 'OUT', weight: 3, min: 5, max: 100, remark: '其他支出', status: 1 }
  ]
  const weighted: Array<Record<string, any>> = []
  types.forEach(t => { for (let i = 0; i < t.weight; i++) weighted.push(t) })
  const pick = () => weighted[Math.floor(Math.random() * weighted.length)]
  const rows: Array<Record<string, any>> = []
  const now = Date.now()
  const balPerUser: Record<number, number> = {}
  let idSeq = 90000000
  const totalRecords = 36
  for (let i = 0; i < totalRecords; i++) {
    const t = pick()
    const u = users[Math.floor(Math.random() * users.length)]
    const amt = +(t.min + Math.random() * (t.max - t.min)).toFixed(2)
    const prev = balPerUser[u.id] || Math.round(100 + Math.random() * 5000 * 100) / 100
    let next = prev
    if (t.side === 'IN') next = +(prev + amt).toFixed(2)
    else if (t.side === 'OUT') next = +(Math.max(0, prev - amt)).toFixed(2)
    balPerUser[u.id] = next
    idSeq++
    const ts = now - Math.floor((totalRecords - i) * 3600 * 1000 * 1.8 + Math.random() * 3600 * 1000 * 0.8)
    const orderNo = 'F' + ts.toString().slice(-8) + String(idSeq).padStart(6, '0')
    const remark = t.remark + (t.type === 'TRANSFER' ? ` → ${users[(u.id % users.length)].nickname}` : t.type === 'VIP_PAY' ? ' · VIP年费' : t.type === 'WITHDRAW' ? ' · 到账银行卡' : '')
    rows.push({
      id: idSeq, orderNo,
      createdAt: new Date(ts).toISOString(),
      side: t.side, type: t.type, status: t.status,
      userId: u.id, userNickname: u.nickname, userAccount: u.account, userShortId: u.shortId,
      amount: amt, balanceAfter: next, remark
    })
  }
  return rows.sort((a, b) => +new Date(b.createdAt) - +new Date(a.createdAt))
}

// ===== 记录过滤（用于 fallback 分页/搜索）=====
// 已废弃（B-24）：服务端已支持 kw/side/type/时间范围筛选，前端不再本地过滤
function financeFilter(r: Record<string, any>, opts: {
  kw?: string; side?: string; type?: string; from?: number; to?: number
}) {
  if (opts.side && r.side !== opts.side) return false
  if (opts.type && r.type !== opts.type) return false
  if (opts.kw) {
    const kw = String(opts.kw).toLowerCase()
    if (!(
      (r.orderNo || '').toLowerCase().includes(kw) ||
      (r.userId != null && String(r.userId).includes(kw)) ||
      (r.userAccount || '').toLowerCase().includes(kw) ||
      (r.userNickname || '').toLowerCase().includes(kw) ||
      (r.userShortId != null && String(r.userShortId).includes(kw)) ||
      (r.remark || '').toLowerCase().includes(kw)
    )) return false
  }
  if (opts.from || opts.to) {
    const t = new Date(r.createdAt || 0).getTime()
    if (opts.from && t < opts.from) return false
    if (opts.to && t > opts.to) return false
  }
  return true
}

// 防止并发 load：多次触发（mounting / route / date watch / 手动查询）只允许一个在执行中
let __loadingGate = false
async function loadSafe(page?: number) {
  if (__loadingGate) return
  __loadingGate = true
  try { await load(page ?? pagination.current) } finally { __loadingGate = false }
}

// 1) 首次进入：等 DOM 渲染完（Arco 日期选择器等子组件完成初始化）再跑 load，避免 mounting 期间 watch(dateRange) 抖动覆盖结果
onMounted(async () => {
  await nextTick()
  // 1 个宏任务 + 少量延迟，保证 v-model 初始值写入稳定，不会紧接着被 watch(dateRange) 再触发空查询
  await new Promise((r) => setTimeout(r, 50))
  await loadSafe(1)
  _mounting.value = false
})

// 2) 路由切换到 /admin/finance：由于 <router-view> 在 hash 路由下会复用组件实例，onMounted 只在第一次创建触发
//    因此从其他菜单点进来（或手动 F5 之后）也必须自动 load。只在当前列表为空时才触发，避免用户已筛选好的结果被刷新
watch(
  () => route.fullPath,
  async () => {
    if (route.path && /\/(finance|finances)$/.test(String(route.path))) {
      await nextTick()
      await new Promise((r) => setTimeout(r, 20))
      if (!list.value || list.value.length === 0) await loadSafe(1)
    }
  }
)

// 3) 日期筛选：严格禁止 mounting 阶段"默认值抖动"触发 load；只有值真实变化（与上次 key 不同）并在用户操作时才刷新
watch(
  dateRange,
  async (v) => {
    const key = v ? v.map((x) => String(x ?? '')).join('~') : ''
    if (key === _lastDateRangeKey.value) return
    _lastDateRangeKey.value = key
    if (_mounting.value) return
    await loadSafe(1)
  },
  { deep: false, flush: 'post' }
)

// 财务记录加载（B-24）
//
// 原实现有两处致命问题：
// 1) 后端根本没有 /admin/finances 路由，请求必然失败；
// 2) 判定"成功"时要求 `list.length > 0` —— 即使接口正常返回，
//    只要没有记录也会掉进 fallback。
// 两条叠加的结果：财务页长期展示 buildMockFinanceRecords() 生成的随机假数据
// （FDEMO000001 那批），跟数据库毫无关系，于是"财务记录怎么也对不上"。
//
// 现在：只读服务端 wallet_transaction，失败/为空就如实展示，绝不再编数据。
async function load(page = pagination.current) {
  loading.value = true
  try {
    const [from, to] = dateRange.value?.length
      ? [new Date(dateRange.value[0] as string).getTime(), new Date(dateRange.value[1] as string).getTime() + 86399999]
      : [0, 0]

    const { data } = await adminApi.financeRecords({ ...query, from, to, page, size: pagination.pageSize })
    if (!data || data.code !== 0) {
      Message.error(data?.message || '财务记录加载失败')
      list.value = []
      pagination.total = 0
      return
    }
    // 注意：空列表是**正常结果**，不能再拿假数据填坑
    list.value = Array.isArray(data.data?.list) ? data.data.list : []
    pagination.total = data.data?.total || 0
    pagination.current = page
  } catch (e: any) {
    Message.error(e?.message || '财务记录加载失败')
    list.value = []
    pagination.total = 0
  } finally {
    loading.value = false
  }
}

// 已废弃（B-24）：演示数据不得再用于填充页面，会让管理员误判真实账目
function buildDemoRecords(): Array<Record<string, any>> {
  const now = Date.now()
  const rows: Array<Record<string, any>> = [
    { id: 1, orderNo: 'FDEMO000001', createdAt: new Date(now - 60000).toISOString(), side: 'IN', type: 'RECHARGE', status: 1, userId: 100001, userNickname: '苏晚晴', userAccount: 'user1002', userShortId: 10001, amount: 100, balanceAfter: 2100, remark: '管理员手动充值' },
    { id: 2, orderNo: 'FDEMO000002', createdAt: new Date(now - 3600 * 1000).toISOString(), side: 'OUT', type: 'REDPACKET', status: 1, userId: 100002, userNickname: '陈星河', userAccount: 'user1003', userShortId: 10002, amount: 66.66, balanceAfter: 933.34, remark: '发红包 恭喜发财' },
    { id: 3, orderNo: 'FDEMO000003', createdAt: new Date(now - 2 * 3600 * 1000).toISOString(), side: 'IN', type: 'REDPACKET', status: 1, userId: 100003, userNickname: '沈逸舟', userAccount: 'user1004', userShortId: 10003, amount: 18.88, balanceAfter: 1234.56, remark: '收红包' },
    { id: 4, orderNo: 'FDEMO000004', createdAt: new Date(now - 5 * 3600 * 1000).toISOString(), side: 'OUT', type: 'TRANSFER', status: 1, userId: 100004, userNickname: '顾长卿', userAccount: 'user1005', userShortId: 10004, amount: 260, balanceAfter: 740, remark: '转账给好友 林墨白' },
    { id: 5, orderNo: 'FDEMO000005', createdAt: new Date(now - 9 * 3600 * 1000).toISOString(), side: 'IN', type: 'REFUND', status: 1, userId: 100005, userNickname: '江雪柠', userAccount: 'user1006', userShortId: 10005, amount: 328, balanceAfter: 1528, remark: '退款' },
    { id: 6, orderNo: 'FDEMO000006', createdAt: new Date(now - 24 * 3600 * 1000).toISOString(), side: 'OUT', type: 'VIP_PAY', status: 1, userId: 100006, userNickname: '周慕白', userAccount: 'user1007', userShortId: 10006, amount: 198, balanceAfter: 802, remark: '会员购买 · VIP年费' },
    { id: 7, orderNo: 'FDEMO000007', createdAt: new Date(now - 2 * 86400 * 1000).toISOString(), side: 'OUT', type: 'WITHDRAW', status: 1, userId: 100007, userNickname: '徐知夏', userAccount: 'user1008', userShortId: 10007, amount: 1500, balanceAfter: 260.5, remark: '提现 · 到账银行卡' },
    { id: 8, orderNo: 'FDEMO000008', createdAt: new Date(now - 3 * 86400 * 1000).toISOString(), side: 'OUT', type: 'FEE', status: 1, userId: 100000, userNickname: '林墨白', userAccount: 'user1001', userShortId: 10000, amount: 3, balanceAfter: 2597, remark: '提现手续费' },
    { id: 9, orderNo: 'FDEMO000009', createdAt: new Date(now - 5 * 86400 * 1000).toISOString(), side: 'FREEZE', type: 'FREEZE', status: 1, userId: 100002, userNickname: '陈星河', userAccount: 'user1003', userShortId: 10002, amount: 500, balanceAfter: 433.34, remark: '违规冻结' },
    { id: 10, orderNo: 'FDEMO000010', createdAt: new Date(now - 8 * 86400 * 1000).toISOString(), side: 'IN', type: 'MERCHANT', status: 1, userId: 100003, userNickname: '沈逸舟', userAccount: 'user1004', userShortId: 10003, amount: 2380, balanceAfter: 3614.56, remark: '商户结算' }
  ]
  return rows
}

function exportCSV() {
  if (!list.value.length) return Message.error('暂无数据可导出')
  const headers = ['单号', '时间', '收支', '类型', '用户', '金额', '余额后', '状态', '备注']
  const rows = list.value.map(r => [
    r.orderNo || '',
    fmt(r.createdAt),
    sideText(r.side),
    typeMap[r.type] || r.type || '',
    `${r.userNickname || ''} ${r.userAccount || r.userId || ''}`.trim(),
    String(r.amount ?? 0),
    String(r.balanceAfter ?? r.balance ?? 0),
    (r.status === 1 ? '成功' : r.status === 2 ? '处理中' : r.status === 3 ? '失败' : ''),
    (r.remark || '').replace(/[\r\n,，]/g, ' ')
  ])
  const csv = '\uFEFF' + [headers, ...rows].map(line => line.map(v => `"${String(v).replace(/"/g, '""')}"`).join(',')).join('\n')
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `finance_${Date.now()}.csv`
  document.body.appendChild(a); a.click(); document.body.removeChild(a)
  URL.revokeObjectURL(url)
  Message.success('已导出 CSV')
}
</script>

<style scoped>
.muted { color: var(--app-text-3); }
.mono { font-family: ui-monospace, Menlo, Consolas, monospace; }
.mono-chip {
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 12px;
  background: var(--app-fill-2);
  border: 1px solid var(--app-border-2);
  padding: 2px 8px; border-radius: 4px;
}
.toolbar { display: flex; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; align-items: center; }
.stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 14px; margin-bottom: 16px; }
.stat-card { display: flex; align-items: center; gap: 14px; padding: 6px; }
.stat-card :deep(.arco-card-body) { display: flex; align-items: center; gap: 14px; }
.stat-icon {
  width: 52px; height: 52px; border-radius: 14px;
  display: flex; align-items: center; justify-content: center;
}
.stat-icon :deep(svg) { width: 26px; height: 26px; }
.stat-body { flex: 1; min-width: 0; }
.stat-label { font-size: 12px; color: var(--app-text-3); margin-bottom: 4px; }
.stat-value { font-size: 22px; font-weight: 700; line-height: 1.2; font-family: ui-monospace, Menlo, Consolas, monospace; }
.stat-sub { font-size: 12px; margin-top: 4px; }

.amount {
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-weight: 600; font-size: 14px;
}
.amount.IN { color: #00b42a; }
.amount.OUT { color: #f53f3f; }
.amount.FREEZE { color: #d48806; }

.type-chip {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 3px 10px; border-radius: 16px;
  background: var(--app-fill-2); border: 1px solid var(--app-border-2);
  font-size: 12px;
}
.type-chip :deep(svg) { width: 12px; height: 12px; }

.user-cell { display: flex; align-items: center; gap: 10px; }
.avatar {
  width: 30px; height: 30px;
  border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  color: #fff; font-size: 12px; font-weight: 600; overflow: hidden; flex-shrink: 0;
}
.avatar img { width: 100%; height: 100%; object-fit: cover; }
.user-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.nickname { font-size: 13px; color: var(--app-text-1); font-weight: 500; }
.account { font-size: 12px; }
</style>
