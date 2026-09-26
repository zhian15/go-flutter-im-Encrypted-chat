<template>
  <div class="page">
    <a-card>
      <div class="toolbar">
        <a-input-search v-model="query.kw" placeholder="按消息内容搜索" style="width: 240px" allow-clear @search="load(1)" />
        <a-input-search v-model="query.userKw" placeholder="用户 ID / 昵称 / 账号 / 手机号" style="width: 260px" allow-clear @search="load(1)" />
        <a-select v-model="query.type" placeholder="消息类型" allow-clear style="width: 150px">
          <a-option v-for="(label, v) in typeMap" :key="v" :value="Number(v)">{{ label }}</a-option>
        </a-select>
        <a-date-picker v-model="dateRange" type="daterange" value-format="YYYY-MM-DD" style="width: 260px" />
        <a-button type="primary" @click="load(1)">查询</a-button>
        <a-button @click="resetQuery">重置</a-button>
      </div>

      <a-table :data="list" row-key="msgId" :pagination="pagination" :loading="loading" @page-change="load" :scroll="{ x: 1280 }">
        <template #columns>
          <a-table-column title="消息ID" :width="180">
            <template #cell="{ record }">
              <span class="msg-id">{{ record.msgId }}</span>
            </template>
          </a-table-column>
          <a-table-column title="发送者" :width="190">
            <template #cell="{ record }">
              <div class="peer-cell">
                <a-avatar :size="30" :image-url="record.senderAvatar || undefined" class="peer-avatar-fallback" :style="!record.senderAvatar ? { background: peerColor(record.senderId) } : {}">
                  {{ firstChar(record.senderName) }}
                </a-avatar>
                <div class="peer-info">
                  <span class="peer-nick">
                    {{ record.senderName || '未知' }}
                    <a-tag v-if="String(record.senderId) === '-1'" color="orangered" size="small" class="official-tag">官方</a-tag>
                  </span>
                  <span class="peer-sub">{{ record.senderShortId ? '#' + record.senderShortId : (record.senderId ?? '—') }}</span>
                </div>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="接收者" :width="190">
            <template #cell="{ record }">
              <div class="peer-cell">
                <a-avatar :size="30" :image-url="record.receiverAvatar || undefined" :style="!record.receiverAvatar ? { background: peerColor(record.receiverId) } : {}">
                  <template v-if="isGroupMsg(record)"><IconUserGroup /></template>
                  <template v-else>{{ firstChar(record.receiverName) }}</template>
                </a-avatar>
                <div class="peer-info">
                  <span class="peer-nick">{{ record.receiverName || '未知' }}</span>
                  <span class="peer-sub" :class="{ 'group-sub': isGroupMsg(record) }">
                    {{ isGroupMsg(record) ? '群聊' : (record.receiverShortId ? '#' + record.receiverShortId : (record.receiverId || '—')) }}
                  </span>
                </div>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="内容" ellipsis>
            <template #cell="{ record }">
              <div class="c-body">
                <span class="type-tag" :style="kindStyle(kindOf(record))">{{ kindLabel(kindOf(record)) }}</span>
                <a-image
                  v-if="kindOf(record) === 'image' && imageSrcOf(record)"
                  :src="imageSrcOf(record)"
                  width="42" height="42" fit="cover"
                  class="msg-thumb"
                  :preview-src-list="[imageSrcOf(record)]"
                />
                <span v-else-if="kindOf(record) === 'image'" class="muted">[图片]</span>
                <span v-else-if="kindOf(record) === 'text'" class="c-text">{{ displayText(record) }}</span>
                <!-- 文件消息：文件名 + 大小 可点击下载（管理员可下载任意会话附件） -->
                <div v-else-if="kindOf(record) === 'file'" class="m-chip m-file">
                  <a-link v-if="fileIdOf(record)" class="file-link" :hoverable="false" @click="onDownloadFile(record)">
                    {{ displayText(record) }}
                  </a-link>
                  <span v-else>{{ displayText(record) }}</span>
                  <a-tooltip :content="fileIdOf(record) ? '下载文件' : '该文件无 fileId，可能是旧消息'">
                    <span class="dl-wrap">
                      <a-button
                        size="mini" type="text"
                        :disabled="!fileIdOf(record)"
                        :loading="!!dlLoading[String(record.msgId)]"
                        @click="onDownloadFile(record)"
                      >
                        <template #icon><IconDownload /></template>
                      </a-button>
                    </span>
                  </a-tooltip>
                </div>
                <div v-else class="m-chip">
                  <span>{{ displayText(record) }}</span>
                </div>
                <a-tag v-if="record.blocked" color="red" size="small">已屏蔽</a-tag>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="时间" :width="165">
            <template #cell="{ record }">{{ fmt(record.createdAt) }}</template>
          </a-table-column>
          <a-table-column title="操作" :width="170" align="center" :fixed="'right'">
            <template #cell="{ record }">
              <a-space size="mini">
                <a-button size="mini" type="text" @click="openConv(record)">查看会话</a-button>
                <a-popconfirm
                  :content="record.blocked ? '确定恢复显示该消息？' : '确定屏蔽该消息？屏蔽后用户端将不再显示'"
                  type="warning"
                  @ok="toggleBlock(record)"
                >
                  <a-button size="mini" type="text" :status="record.blocked ? 'normal' : 'danger'">
                    {{ record.blocked ? '恢复' : '屏蔽' }}
                  </a-button>
                </a-popconfirm>
              </a-space>
            </template>
          </a-table-column>
        </template>
        <template #empty>
          <a-empty description="暂无消息记录" />
        </template>
      </a-table>
    </a-card>

    <!-- 查看会话弹窗（聊天窗口样式，复用 ConvViewer 共享组件） -->
    <a-modal v-model:visible="convVisible" :title="convTitle" :width="640" :footer="false" unmount-on-close>
      <ConvViewer v-if="convConvId" :key="convConvId" :conv-id="convConvId" :mine-sender-id="convMineId" />
    </a-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, watch, nextTick } from 'vue'
import { Message } from '@arco-design/web-vue'
import { IconUserGroup, IconDownload } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'
// 消息渲染 helper（kind 样式 / displayText / imageSrcOf）与群组管理共用，见 adminMsg.ts
import { kindOf, kindLabel, kindStyle, displayText, imageSrcOf, fileIdOf, downloadAdminFile } from './adminMsg'
// 查看会话弹窗体（与群组管理共用同一组件）
import ConvViewer from './ConvViewer.vue'

const list = ref<Array<Record<string, any>>>([])
const loading = ref(false)
const dateRange = ref<Array<string | number> | undefined>(undefined)
// userKw：按用户维度搜索（用户 ID / 昵称 / 账号 / 手机号，后端反查用户后按 sender_id 过滤）
const query = reactive({ kw: '', userKw: '', type: undefined as number | undefined })
const pagination = reactive({ current: 1, pageSize: 20, total: 0, showTotal: true })
// 文件下载中状态：key = msgId
const dlLoading = ref<Record<string, boolean>>({})

// 消息类型（服务端 type 字段；App 未上线不考虑历史遗留旧类型 10/11/20/21）
const typeMap: Record<number, string> = {
  1: '文本', 2: '图片', 3: '文件', 4: '语音', 5: '视频',
  6: '系统', 7: '语音通话', 8: '红包', 9: '转账', 99: '撤回'
}

// 重置筛选时统一清空，期间屏蔽 watch 触发的重复请求
let suppressWatch = false
onMounted(() => load(1))
watch(dateRange, () => { if (suppressWatch) return; load(1) })
watch(() => query.type, () => { if (suppressWatch) return; load(1) })

// 清空全部筛选条件（内容关键字 / 用户关键字 / 类型 / 日期范围）并回到第 1 页
function resetQuery() {
  suppressWatch = true
  query.kw = ''
  query.userKw = ''
  query.type = undefined
  dateRange.value = undefined
  load(1)
  nextTick(() => { suppressWatch = false })
}

async function load(page = pagination.current) {
  loading.value = true
  try {
    const [from, to] = dateRange.value?.length
      ? [new Date(dateRange.value[0] as string).getTime(), new Date(dateRange.value[1] as string).getTime() + 86399999]
      : [0, 0]
    // 空关键字不传给后端，避免空串命中全表语义歧义
    const kw = String(query.kw || '').trim()
    const userKw = String(query.userKw || '').trim()
    const { data } = await adminApi.messages({
      ...query,
      kw: kw || undefined,
      userKw: userKw || undefined,
      from,
      to,
      page,
      size: pagination.pageSize
    })
    if (data?.code !== 0) { Message.error(data?.message || '查询失败'); return }
    list.value = data.data?.list || []
    pagination.total = data.data?.total || 0
    pagination.current = page
  } catch (e: any) {
    Message.error('查询失败：' + (e?.message || ''))
  } finally {
    loading.value = false
  }
}

// ===== 文件消息下载（需求 6）：管理员可下载任意会话附件 =====
async function onDownloadFile(row: Record<string, any>) {
  if (!fileIdOf(row)) { Message.warning('该文件无 fileId，可能是旧消息'); return }
  const key = String(row.msgId || fileIdOf(row))
  if (dlLoading.value[key]) return
  dlLoading.value[key] = true
  try {
    await downloadAdminFile(row)
  } finally {
    dlLoading.value[key] = false
  }
}

// ===== 屏蔽 / 恢复 =====
async function toggleBlock(row: Record<string, any>) {
  try {
    const { data } = await adminApi.messageBlock(String(row.msgId), !row.blocked)
    if (data?.code !== 0) { Message.error(data?.message || '操作失败'); return }
    row.blocked = !row.blocked
    Message.success(row.blocked ? '已屏蔽，用户端不再显示该消息' : '已恢复显示')
  } catch (e: any) {
    Message.error('操作失败：' + (e?.message || ''))
  }
}

// ===== 查看会话（弹窗体复用 ConvViewer 共享组件，数据源 /admin/messages）=====
const convVisible = ref(false)
const convTitle = ref('会话消息')
// 打开会话时来源行的发送者（单聊）：作为聊天窗口右侧「我方」视角；群聊置空（全部左侧）
const convMineId = ref('')
let convConvId = ''

function openConv(row: Record<string, any>) {
  convConvId = String(row.conversationId || '')
  if (!convConvId) { Message.warning('缺少会话 ID'); return }
  convTitle.value = `会话消息 · ${row.convName || row.conversationId}`
  // 单聊以该条消息的发送者为右侧视角（发送者在右、接收者在左）；群聊 convMineId 置空（全部左侧显示）
  convMineId.value = isGroupMsg(row) ? '' : String(row.senderId || '')
  convVisible.value = true
}

// ===== 通用渲染 helper =====
const PEER_COLORS = ['#4E8CFF', '#7B61FF', '#FF7D00', '#00B42A', '#F53F3F', '#14C9C9', '#9A73FF', '#FF57A2']
function peerColor(id: any) {
  const s = String(id || '')
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return { backgroundColor: PEER_COLORS[h % PEER_COLORS.length] }
}
function firstChar(s: string) {
  return (s || '?').trim().charAt(0).toUpperCase() || '?'
}
function isGroupMsg(r: Record<string, any>) {
  return Number(r.convType) === 2
}
function fmt(t?: string) {
  return t ? new Date(t).toLocaleString() : '-'
}

// ===== 内容解析：kind 样式 / displayText / imageSrcOf 已抽到 adminMsg.ts（与群组管理共用）=====
</script>

<style scoped>
.toolbar { display: flex; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.msg-thumb { border-radius: var(--app-radius-sm); border: 1px solid var(--app-border-2); cursor: pointer; }
.msg-id { font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 12px; color: var(--app-text-2); }

.peer-cell { display: flex; align-items: center; gap: 8px; min-width: 0; }
.peer-avatar-fallback { flex-shrink: 0; }
.peer-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; max-width: 100%; }
.peer-nick { font-size: 13px; color: var(--app-text-1); font-weight: 500; line-height: 1.2; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; display: flex; align-items: center; gap: 4px; }
.official-tag { flex-shrink: 0; transform: scale(0.85); margin-left: 2px; }
.peer-sub { font-size: 11px; color: var(--app-text-3); line-height: 1.2; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-family: ui-monospace, Menlo, Consolas, monospace; }
.group-sub { color: var(--app-primary); font-family: inherit; }

.c-body { display: flex; align-items: center; gap: 8px; min-height: 40px; max-width: 100%; flex-wrap: wrap; }
.type-tag { display: inline-flex; align-items: center; padding: 2px 8px; border-radius: 10px; border: 1px solid; font-size: 11px; line-height: 1.4; font-weight: 500; flex-shrink: 0; }
.c-text { max-width: 420px; white-space: normal; word-break: break-word; line-height: 1.5; }
.m-chip { display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px; border-radius: 16px; background: var(--app-fill-2, #f5f7fa); color: var(--app-text-1, #232a3a); border: 1px solid var(--app-border-2, #e5e7eb); font-size: 12px; line-height: 1.4; }
/* 文件消息：文件名可点击 + 下载按钮 */
.m-file { max-width: 420px; padding-right: 4px; }
.m-file .file-link { font-size: 12px; line-height: 1.4; padding: 0; color: var(--app-text-1, #232a3a); }
.m-file .dl-wrap { display: inline-flex; align-items: center; }
.muted { color: var(--app-text-3); }

/* 查看会话弹窗体样式已随 ConvViewer 共享组件迁移 */
</style>
