<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Message, Modal } from '@arco-design/web-vue'
import { adminApi } from '@/api/admin'

// 投诉管理：用户在 App 端发起的投诉/举报，管理员在此处理。
// 数据契约见 im-server/doc/API.md「投诉」节：
//   GET  /api/v1/admin/reports          分页列表（status 0 待处理 / 1 已处理）
//   POST /api/v1/admin/reports/:id/handle   标记已处理
//   封禁被投诉账号复用既有端点 PUT /api/v1/admin/users/:id/status（status=2 禁用）

const loading = ref(false)
const status = ref<number | undefined>(undefined)
const list = ref<any[]>([])
const total = ref(0)
const pagination = reactive({ current: 1, pageSize: 20, showTotal: true, showPageSize: true })
const actLoading = ref<Record<string, boolean>>({})

const statusMap: Record<number, { text: string; cls: string }> = {
  0: { text: '待处理', cls: 'tag tag-pending' },
  1: { text: '已处理', cls: 'tag tag-ok' }
}

// category 契约未定死枚举：数字按映射翻译，字符串原样展示
const categoryNumMap: Record<number, string> = {
  1: '辱骂骚扰',
  2: '色情低俗',
  3: '欺诈诈骗',
  4: '广告引流',
  5: '违法违规',
  6: '侵权',
  7: '其他'
}
function categoryText(v: unknown): string {
  if (v === null || v === undefined || v === '') return '-'
  const n = Number(v)
  if (!Number.isNaN(n)) return categoryNumMap[n] || String(v)
  return String(v)
}

// 昵称字段契约未定死，兼容驼峰/下划线两种命名
function pick(obj: any, camel: string, snake: string): string {
  const v = obj?.[camel] ?? obj?.[snake]
  return v === undefined || v === null || v === '' ? '' : String(v)
}
function reporterName(r: any): string {
  return pick(r, 'reporterNickname', 'reporter_nickname') || pick(r, 'reporterName', 'reporter_name')
}
function peerName(r: any): string {
  return pick(r, 'peerNickname', 'peer_nickname') || pick(r, 'peerName', 'peer_name')
}
function fmtId(v: unknown): string {
  if (v === undefined || v === null || v === '') return '-'
  return 'UID:' + String(v)
}

async function load(page: number = pagination.current) {
  loading.value = true
  try {
    const { data } = await adminApi.reports({
      status: status.value === undefined ? undefined : Number(status.value),
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
  } catch (e: any) {
    Message.error(e?.message || '加载失败')
  } finally {
    loading.value = false
  }
}

async function doHandle(r: any) {
  const key = String(r.id)
  if (actLoading.value[key]) return
  actLoading.value[key] = true
  try {
    const { data } = await adminApi.reportHandle(r.id)
    if (data.code === 0) {
      Message.success('已标记为已处理')
      load(pagination.current)
    } else {
      Message.error(data.message || '操作失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '操作失败')
  } finally {
    actLoading.value[key] = false
  }
}

// 一键封禁被投诉账号：危险操作，必须二次确认
function confirmBan(r: any) {
  const peerId = r.peer_id ?? r.peerId
  if (peerId === undefined || peerId === null || peerId === '') {
    Message.warning('该投诉未关联被投诉账号，无法封禁')
    return
  }
  const name = peerName(r) || fmtId(peerId)
  Modal.confirm({
    title: '封禁账号确认',
    content: `确定封禁被投诉账号「${name}」（${fmtId(peerId)}）吗？封禁后该账号将无法登录，且此操作立即生效。`,
    okText: '确认封禁',
    cancelText: '取消',
    okButtonProps: { status: 'danger' },
    onOk: async () => {
      const key = String(r.id)
      actLoading.value[key] = true
      try {
        // 复用用户管理的封禁端点：status=2 禁用（与 UserManage.toggleStatus 一致）
        const { data } = await adminApi.userStatus(Number(peerId), 2)
        if (data.code === 0) {
          Message.success(`已封禁账号 ${name}（${fmtId(peerId)}）`)
          load(pagination.current)
        } else {
          Message.error(data.message || '封禁失败')
        }
      } catch (e: any) {
        Message.error(e?.message || '封禁失败')
      } finally {
        actLoading.value[key] = false
      }
    }
  })
}

onMounted(() => load(1))
</script>

<template>
  <div class="rm-view">
    <a-page-header title="投诉管理">
      <template #sub-title>
        用户在 App 端发起的投诉与举报在此集中处理；处理后可标记已处理，恶意账号可一键封禁。
      </template>
    </a-page-header>

    <a-card style="margin:0 16px 16px">
      <div class="toolbar">
        <a-select v-model="status" placeholder="全部状态" style="width:160px" allow-clear @change="load(1)">
          <a-option :value="0">待处理</a-option>
          <a-option :value="1">已处理</a-option>
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
          <a-table-column title="投诉ID" data-index="id" :width="90" />
          <a-table-column title="时间" :width="168">
            <template #cell="{ record }">{{ record.created_at || record.createdAt || '-' }}</template>
          </a-table-column>
          <a-table-column title="举报人" :width="200">
            <template #cell="{ record }">
              <div>
                <b>{{ reporterName(record) || '-' }}</b>
                <span v-if="record.reporter_id || record.reporterId" class="muted">（{{ fmtId(record.reporter_id ?? record.reporterId) }}）</span>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="被投诉人" :width="200">
            <template #cell="{ record }">
              <div>
                <b>{{ peerName(record) || '-' }}</b>
                <span v-if="record.peer_id || record.peerId" class="muted">（{{ fmtId(record.peer_id ?? record.peerId) }}）</span>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="类型" :width="110">
            <template #cell="{ record }">{{ categoryText(record.category) }}</template>
          </a-table-column>
          <a-table-column title="说明" :ellipsis="true" :tooltip="true">
            <template #cell="{ record }">
              <span :class="{ muted: !record.note }">{{ record.note || '-' }}</span>
            </template>
          </a-table-column>
          <a-table-column title="会话" :width="120">
            <template #cell="{ record }">
              <span class="muted small">{{ record.conv_id || record.convId || '-' }}</span>
            </template>
          </a-table-column>
          <a-table-column title="状态" :width="100">
            <template #cell="{ record }">
              <span :class="statusMap[Number(record.status)]?.cls || 'tag'">{{ statusMap[Number(record.status)]?.text || '未知' }}</span>
            </template>
          </a-table-column>
          <a-table-column title="操作" :width="220" fixed="right">
            <template #cell="{ record }">
              <template v-if="Number(record.status) === 0">
                <a-button
                  type="primary" size="mini" class="btn-ok"
                  :loading="!!actLoading[String(record.id)]"
                  @click="doHandle(record)"
                >标记已处理</a-button>
                <a-button
                  type="outline" size="mini" class="btn-danger"
                  :loading="!!actLoading[String(record.id)]"
                  style="margin-left:4px"
                  @click="confirmBan(record)"
                >封禁账号</a-button>
              </template>
              <template v-else>
                <span class="muted small">已处理</span>
              </template>
            </template>
          </a-table-column>
        </template>
        <template #empty>
          <a-empty description="暂无投诉记录" />
        </template>
      </a-table>
    </a-card>
  </div>
</template>

<style scoped>
.rm-view { padding: 16px 0 40px; }
.toolbar { display:flex; gap:10px; align-items:center; margin-bottom: 14px; flex-wrap:wrap; }
.muted { color: var(--color-text-3); }
.small { font-size: 12px; }
.btn-ok :deep(.arco-btn) { background: #00B42A; }
.btn-danger :deep(.arco-btn) { color: #F53F3F; border-color: #FFD1C7; }
.tag { display:inline-block; padding:2px 10px; border-radius:999px; font-size:12px; }
.tag-pending { background:#FFF7E8; color:#FF7D00; border:1px solid #FFE4BA }
.tag-ok { background:#E8FFEA; color:#00B42A; border:1px solid #B7E8BF }
</style>
