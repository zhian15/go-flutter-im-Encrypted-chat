<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { Message, Modal } from '@arco-design/web-vue'
import { adminApi } from '@/api/admin'

const loading = ref(false)
const kw = ref('')
const status = ref<number | undefined>(undefined)
const wType = ref<number | undefined>(undefined)
const list = ref<any[]>([])
const total = ref(0)
const pagination = reactive({ current: 1, pageSize: 20, showTotal: true, showPageSize: true })
const actLoading = ref<Record<string, boolean>>({})
const rejectModalVisible = ref(false)
const rejectTarget = ref<any>(null)
const rejectReason = ref('')

const typeMap: Record<number, string> = { 1: '微信', 2: '支付宝', 3: '银行卡' }
const statusMap: Record<number, { text: string; cls: string }> = {
  1: { text: '待审核', cls: 'tag tag-pending' },
  2: { text: '已通过', cls: 'tag tag-ok' },
  3: { text: '已拒绝', cls: 'tag tag-rej' }
}

async function load(page: number = pagination.current) {
  loading.value = true
  try {
    const { data } = await adminApi.withdrawOrders({
      kw: kw.value,
      status: status.value && status.value > 0 ? status.value : undefined,
      type: wType.value && wType.value > 0 ? wType.value : undefined,
      page,
      size: pagination.pageSize
    })
    if (data.code === 0) {
      list.value = (data.data?.list as any[]) ?? []
      total.value = Number(data.data?.total ?? 0)
    } else {
      Message.error(data.message || '加载失败')
    }
    pagination.current = page
  } finally {
    loading.value = false
  }
}

function snapshotText(r: any): string[] {
  const s = (r?.accountSnapshot || {}) as any
  const lines: string[] = []
  const t = Number(r.withdrawType ?? s.accountType ?? 1)
  lines.push(`方式：${typeMap[t] || t}`)
  if (t === 1) {
    if (s.wechatName) lines.push(`姓名：${s.wechatName}`)
  } else if (t === 2) {
    if (s.alipayAccount) lines.push(`支付宝账号：${s.alipayAccount}`)
    if (s.alipayName) lines.push(`姓名：${s.alipayName}`)
  } else if (t === 3) {
    if (s.bankName) lines.push(`开户银行：${s.bankName}`)
    if (s.bankCardNo) lines.push(`卡号：${s.bankCardNo}`)
    if (s.bankCardNoFull && s.bankCardNoFull !== s.bankCardNo) lines.push(`完整卡号：${s.bankCardNoFull}`)
    if (s.bankAccountName) lines.push(`开户姓名：${s.bankAccountName}`)
  }
  return lines
}

function openDetail(r: any) {
  const lines = snapshotText(r)
  const snap = (r?.accountSnapshot || {}) as any
  let imgUrl = ''
  const t = Number(r.withdrawType ?? snap.accountType ?? 1)
  if (t === 1) imgUrl = snap.wechatQrcodeUrl
  else if (t === 2) imgUrl = snap.alipayQrcodeUrl
  Modal.open({
    title: `提现账户快照 · 订单 #${r.id}`,
    width: 480,
    content: () => h('div', { style: 'line-height:1.7' }, [
      imgUrl ? h('img', { src: imgUrl, style: 'display:block;width:220px;margin:0 auto 14px;border-radius:8px;border:1px solid var(--color-border-2)' }) : null,
      ...lines.map((l) => h('div', l))
    ]),
    hideCancel: true,
    okText: '关闭'
  })
}

async function doApprove(r: any) {
  const confirmMsg = `确认已给用户（${r.userAccount || r.userId}）实际打款 ¥${Number(r.actualAmount).toFixed(2)}？
申请 ¥${Number(r.amount).toFixed(2)}，手续费 ¥${Number(r.fee).toFixed(2)}。
点击确定后：frozen -= amount；balance -= fee；记 withdraw 流水。`
  const ok = await new Promise<boolean>((resolve) => {
    Modal.confirm({
      title: '确定提现通过',
      content: confirmMsg,
      okText: '我已打款，确认通过',
      cancelText: '取消',
      width: 520,
      onOk: () => resolve(true),
      onCancel: () => resolve(false)
    })
  })
  if (!ok) return
  const key = String(r.id)
  actLoading.value[key] = true
  try {
    const { data } = await adminApi.withdrawOrderApprove(r.id)
    if (data.code === 0) {
      Message.success(`提现已通过，扣款 ¥${Number(r.fee).toFixed(2)}（手续费）`)
      load(pagination.current)
    } else {
      Message.error(data.message || '操作失败')
    }
  } finally {
    actLoading.value[key] = false
  }
}

function openReject(r: any) {
  rejectTarget.value = r
  rejectReason.value = ''
  rejectModalVisible.value = true
}

async function confirmReject() {
  if (!rejectTarget.value) return
  if (!rejectReason.value.trim()) return Message.warning('请填写驳回原因（用户会看到）')
  const key = String(rejectTarget.value.id)
  actLoading.value[key] = true
  try {
    const { data } = await adminApi.withdrawOrderReject(rejectTarget.value.id, rejectReason.value.trim())
    if (data.code === 0) {
      Message.success('已驳回；冻结金额已退回用户余额')
      rejectModalVisible.value = false
      load(pagination.current)
    } else {
      Message.error(data.message || '操作失败')
    }
  } finally {
    actLoading.value[key] = false
  }
}

onMounted(() => load(1))
</script>

<template>
  <div class="wo-view">
    <a-page-header title="提现订单">
      <template #sub-title>
        用户先绑定提现方式后申请 → 系统冻结 amount → 管理员审核打款；通过即 frozen 清 amount + 余额扣 fee；驳回则 frozen 原路退回。
      </template>
    </a-page-header>

    <a-card style="margin:0 16px 16px">
      <div class="toolbar">
        <a-input-search v-model="kw" placeholder="账号/昵称/ID" style="width:320px" allow-clear @search="load(1)" />
        <a-select v-model="status" placeholder="全部状态" style="width:140px" allow-clear @change="load(1)">
          <a-option :value="1">待审核</a-option>
          <a-option :value="2">已通过</a-option>
          <a-option :value="3">已拒绝</a-option>
        </a-select>
        <a-select v-model="wType" placeholder="全部方式" style="width:140px" allow-clear @change="load(1)">
          <a-option :value="1">微信</a-option>
          <a-option :value="2">支付宝</a-option>
          <a-option :value="3">银行卡</a-option>
        </a-select>
        <a-button type="outline" @click="load(1)">搜索</a-button>
      </div>

      <a-table
        :data="list"
        row-key="id"
        :loading="loading"
        :pagination="{ ...pagination, total }"
        @page-change="load"
      >
        <template #columns>
          <a-table-column title="订单ID" data-index="id" :width="96" />
          <a-table-column title="用户" :width="200">
            <template #cell="{ record }">
              <div>
                <b>{{ record.userNickname || '-' }}</b>
                <span class="muted">（{{ record.userAccount || ('UID:' + record.userId) }}）</span>
              </div>
              <div v-if="record.userShortId" class="muted small">ID：{{ record.userShortId }}</div>
            </template>
          </a-table-column>
          <a-table-column title="申请(元)" align="right" :width="104">
            <template #cell="{ record }"><b class="money-out">-¥{{ Number(record.amount).toFixed(2) }}</b></template>
          </a-table-column>
          <a-table-column title="手续费(元)" align="right" :width="96">
            <template #cell="{ record }">¥{{ Number(record.fee).toFixed(2) }}</template>
          </a-table-column>
          <a-table-column title="实际打款(元)" align="right" :width="112">
            <template #cell="{ record }"><b>¥{{ Number(record.actualAmount).toFixed(2) }}</b></template>
          </a-table-column>
          <a-table-column title="方式" :width="80">
            <template #cell="{ record }">{{ typeMap[Number(record.withdrawType)] || record.withdrawType }}</template>
          </a-table-column>
          <a-table-column title="收款账户(快照)" :width="260" :ellipsis="true">
            <template #cell="{ record }">
              <div class="snap-lines">
                <div v-for="(ln, idx) in snapshotText(record).slice(0, 4)" :key="idx" class="small">{{ ln }}</div>
              </div>
              <a-button type="text" size="mini" @click="openDetail(record)">查看完整快照</a-button>
            </template>
          </a-table-column>
          <a-table-column title="状态" :width="100">
            <template #cell="{ record }">
              <span :class="statusMap[Number(record.status)]?.cls">{{ statusMap[Number(record.status)]?.text || '未知' }}</span>
            </template>
          </a-table-column>
          <a-table-column title="提交时间" data-index="createdAt" :width="168" />
          <a-table-column title="操作" :width="230" fixed="right">
            <template #cell="{ record }">
              <template v-if="Number(record.status) === 1">
                <a-button
                  type="primary" size="mini" class="btn-warn"
                  :loading="!!actLoading[String(record.id)]"
                  @click="doApprove(record)"
                >确定提现</a-button>
                <a-button
                  type="outline" size="mini" class="btn-danger"
                  :loading="!!actLoading[String(record.id)]"
                  style="margin-left:4px"
                  @click="openReject(record)"
                >驳回</a-button>
              </template>
              <template v-else>
                <div class="muted small">{{ record.reviewedAt || '—' }}</div>
                <div v-if="Number(record.status) === 3" class="muted small">原因：{{ record.rejectReason }}</div>
              </template>
            </template>
          </a-table-column>
        </template>
      </a-table>
    </a-card>

    <a-modal
      v-model:visible="rejectModalVisible"
      title="驳回提现申请（将自动解冻 amount 回用户余额）"
      @ok="confirmReject"
      okText="确认驳回并解冻"
      :cancelText="'取消'"
      :okLoading="!!actLoading[String(rejectTarget?.id)]"
    >
      <a-textarea v-model="rejectReason" :rows="3" placeholder="请填写驳回原因（显示给用户）" />
    </a-modal>
  </div>
</template>

<style scoped>
.wo-view { padding: 16px 0 40px; }
.toolbar { display:flex; gap:10px; align-items:center; margin-bottom: 14px; flex-wrap:wrap }
.muted { color: var(--color-text-3); }
.small { font-size: 12px; }
.snap-lines { line-height: 1.5; margin-bottom: 2px; }
.money-out { color: #F53F3F; }
.btn-warn :deep(.arco-btn-primary) { background:#FF7D00; border-color:#FF7D00; }
.btn-danger :deep(.arco-btn) { color: #F53F3F; border-color:#FFD1C7; }
.tag { display:inline-block; padding:2px 10px; border-radius:999px; font-size:12px; }
.tag-pending { background:#FFF7E8; color:#FF7D00; border:1px solid #FFE4BA }
.tag-ok { background:#E8FFEA; color:#00B42A; border:1px solid #B7E8BF }
.tag-rej { background:#FFECE8; color:#F53F3F; border:1px solid #FFD1C7 }
</style>
