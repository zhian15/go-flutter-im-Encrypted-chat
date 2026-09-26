<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { Message, Modal } from '@arco-design/web-vue'
import { adminApi } from '@/api/admin'

const loading = ref(false)
const kw = ref('')
const status = ref<number | undefined>(undefined)
const list = ref<any[]>([])
const total = ref(0)
const pagination = reactive({ current: 1, pageSize: 20, showTotal: true, showPageSize: true })
const actLoading = ref<Record<string, boolean>>({})
const rejectModalVisible = ref(false)
const rejectTarget = ref<any>(null)
const rejectReason = ref('')

const payMethodMap: Record<number, string> = { 1: '微信', 2: '支付宝', 3: '银行卡' }
const statusMap: Record<number, { text: string; cls: string }> = {
  1: { text: '待审核', cls: 'tag tag-pending' },
  2: { text: '已通过', cls: 'tag tag-ok' },
  3: { text: '已拒绝', cls: 'tag tag-rej' }
}

async function load(page: number = pagination.current) {
  loading.value = true
  try {
    const { data } = await adminApi.rechargeOrders({
      kw: kw.value,
      status: status.value && status.value > 0 ? status.value : undefined,
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

function viewProof(url: string) {
  if (!url) return Message.warning('用户未上传凭证')
  Modal.open({
    title: '支付凭证',
    width: 520,
    content: () => h('img', { src: url, style: { width: '100%', display: 'block', borderRadius: '8px' } }),
    okText: '关闭',
    hideCancel: true
  })
}

async function doApprove(r: any) {
  const key = String(r.id)
  if (actLoading.value[key]) return
  actLoading.value[key] = true
  try {
    const { data } = await adminApi.rechargeOrderApprove(r.id)
    if (data.code === 0) {
      Message.success(`已通过充值 ¥${Number(r.amount).toFixed(2)}；入账后余额 ¥${Number(data.data?.balance ?? 0).toFixed(2)}`)
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
  if (!rejectReason.value.trim()) return Message.warning('请填写驳回原因')
  const key = String(rejectTarget.value.id)
  actLoading.value[key] = true
  try {
    const { data } = await adminApi.rechargeOrderReject(rejectTarget.value.id, rejectReason.value.trim())
    if (data.code === 0) {
      Message.success('已驳回')
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
  <div class="ro-view">
    <a-page-header title="充值订单">
      <template #sub-title>
        用户提交充值申请后，管理员在此审核支付凭证；确定充值即给用户加余额并记 recharge 流水。
      </template>
    </a-page-header>

    <a-card style="margin:0 16px 16px">
      <div class="toolbar">
        <a-input-search v-model="kw" placeholder="账号/昵称/ID/交易单号" style="width:360px" allow-clear @search="load(1)" />
        <a-select v-model="status" placeholder="全部状态" style="width:160px" allow-clear @change="load(1)">
          <a-option :value="1">待审核</a-option>
          <a-option :value="2">已通过</a-option>
          <a-option :value="3">已拒绝</a-option>
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
          <a-table-column title="订单ID" data-index="id" :width="110" />
          <a-table-column title="用户" :width="220">
            <template #cell="{ record }">
              <div>
                <b>{{ record.userNickname || '-' }}</b>
                <span class="muted">（{{ record.userAccount || ('UID:' + record.userId) }}）</span>
              </div>
              <div v-if="record.userShortId" class="muted small">ID：{{ record.userShortId }}</div>
            </template>
          </a-table-column>
          <a-table-column title="金额(元)" data-index="amount" :width="110" align="right">
            <template #cell="{ record }"><b class="money">¥{{ Number(record.amount).toFixed(2) }}</b></template>
          </a-table-column>
          <a-table-column title="支付方式" :width="90">
            <template #cell="{ record }">{{ payMethodMap[Number(record.payMethod)] || record.payMethod }}</template>
          </a-table-column>
          <a-table-column title="支付凭证/单号" :width="230">
            <template #cell="{ record }">
              <div v-if="record.proofImage">
                <img
                  :src="record.proofImage"
                  class="thumb"
                  @click="viewProof(record.proofImage)"
                  alt="凭证缩略图"
                />
                <a-button v-if="record.proofImage" type="text" size="mini" @click="viewProof(record.proofImage)">查看大图</a-button>
              </div>
              <div v-else class="muted">用户未上传凭证图</div>
              <div v-if="record.payTxNo" class="muted small">单号：{{ record.payTxNo }}</div>
            </template>
          </a-table-column>
          <a-table-column title="状态" :width="100">
            <template #cell="{ record }">
              <span :class="statusMap[Number(record.status)]?.cls">{{ statusMap[Number(record.status)]?.text || '未知' }}</span>
            </template>
          </a-table-column>
          <a-table-column title="驳回原因" data-index="rejectReason" :ellipsis="true" :width="160" />
          <a-table-column title="提交时间" data-index="createdAt" :width="168" />
          <a-table-column title="操作" :width="220" fixed="right">
            <template #cell="{ record }">
              <template v-if="Number(record.status) === 1">
                <a-button
                  type="primary" size="mini" class="btn-ok"
                  :loading="!!actLoading[String(record.id)]"
                  @click="doApprove(record)"
                >确定充值</a-button>
                <a-button
                  type="outline" size="mini" class="btn-danger"
                  :loading="!!actLoading[String(record.id)]"
                  style="margin-left:4px"
                  @click="openReject(record)"
                >驳回</a-button>
              </template>
              <template v-else-if="Number(record.status) === 2">
                <span class="muted small">
                  审核员ID：{{ record.reviewerId || '-' }}<br/>{{ record.reviewedAt || '' }}
                </span>
              </template>
              <template v-else>
                <span class="muted small">{{ record.reviewedAt || '—' }}</span>
              </template>
            </template>
          </a-table-column>
        </template>
      </a-table>
    </a-card>

    <a-modal
      v-model:visible="rejectModalVisible"
      title="驳回充值申请"
      @ok="confirmReject"
      okText="确认驳回"
      :cancelText="'取消'"
      :okLoading="!!actLoading[String(rejectTarget?.id)]"
    >
      <a-textarea v-model="rejectReason" :rows="3" placeholder="请填写驳回原因（会显示给用户，建议具体）" />
    </a-modal>
  </div>
</template>

<style scoped>
.ro-view { padding: 16px 0 40px; }
.toolbar { display:flex; gap:10px; align-items:center; margin-bottom: 14px; flex-wrap:wrap; }
.thumb { width:72px; height:72px; object-fit:cover; border-radius:6px; border:1px solid var(--color-border-2); cursor:zoom-in; margin-right:8px; vertical-align:middle }
.muted { color: var(--color-text-3); }
.small { font-size: 12px; }
.money { color: #00B42A; }
.btn-ok :deep(.arco-btn) { background: #00B42A; }
.btn-danger :deep(.arco-btn) { color: #F53F3F; border-color: #FFD1C7; }
.tag { display:inline-block; padding:2px 10px; border-radius:999px; font-size:12px; }
.tag-pending { background:#FFF7E8; color:#FF7D00; border:1px solid #FFE4BA }
.tag-ok { background:#E8FFEA; color:#00B42A; border:1px solid #B7E8BF }
.tag-rej { background:#FFECE8; color:#F53F3F; border:1px solid #FFD1C7 }
</style>
