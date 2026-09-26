<template>
  <div ref="scrollEl" class="conv-window" @scroll="onScroll">
    <a-spin v-if="loading" class="conv-spin" />
    <template v-for="m in list" :key="m.msgId">
      <div class="conv-day" v-if="m.__day">{{ m.__day }}</div>
      <div class="conv-row" :class="{ mine: isMine(m) }">
        <a-avatar :size="34" :image-url="m.senderAvatar || undefined" :style="!m.senderAvatar ? { background: peerColor(m.senderId) } : {}">
          {{ firstChar(m.senderName) }}
        </a-avatar>
        <div class="conv-bubble-wrap">
          <div class="conv-sender">
            <span class="conv-nick">{{ m.senderName || '未知' }}</span>
            <span v-if="m.senderShortId" class="conv-short">#{{ m.senderShortId }}</span>
            <a-tag v-if="String(m.senderId) === '-1'" color="orangered" size="small" class="official-tag">官方</a-tag>
          </div>
          <div class="conv-bubble">
            <a-image
              v-if="kindOf(m) === 'image' && imageSrcOf(m)"
              :src="imageSrcOf(m)" width="180" fit="cover" :preview-src-list="[imageSrcOf(m)]"
            />
            <!-- 文件消息：文件名可点击下载（与消息记录共用 adminMsg.downloadAdminFile） -->
            <a-space v-else-if="kindOf(m) === 'file'" :size="4">
              <a-link v-if="fileIdOf(m)" :hoverable="false" @click="onDownloadFile(m)">{{ displayText(m) }}</a-link>
              <span v-else>{{ displayText(m) }}</span>
              <a-tooltip :content="fileIdOf(m) ? '下载文件' : '该文件无 fileId，可能是旧消息'">
                <span class="dl-wrap">
                  <a-button
                    size="mini" type="text"
                    :disabled="!fileIdOf(m)"
                    :loading="!!dlLoading[String(m.msgId)]"
                    @click="onDownloadFile(m)"
                  >
                    <template #icon><IconDownload /></template>
                  </a-button>
                </span>
              </a-tooltip>
            </a-space>
            <template v-else>{{ displayText(m) }}</template>
          </div>
          <div class="conv-time">{{ fmt(m.createdAt) }}<a-tag v-if="m.blocked" color="red" size="small" class="blocked-tag">已屏蔽</a-tag></div>
        </div>
      </div>
    </template>
    <a-empty v-if="!loading && !list.length" description="该会话暂无消息" />
  </div>
</template>

<script setup lang="ts">
/**
 * 会话消息查看器（共享组件）：消息记录「查看会话」与群组管理「消息」抽屉共用，
 * 数据源统一走 /admin/messages（AdminMessageOut，含发送者昵称/头像/ID 冗余），
 * 保证两处渲染完全一致，不重复造轮子。
 */
import { ref, onMounted, nextTick } from 'vue'
import { Message } from '@arco-design/web-vue'
import { IconDownload } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'
import { kindOf, displayText, imageSrcOf, fileIdOf, downloadAdminFile } from './adminMsg'

const props = defineProps<{
  convId: string
  // 单聊：右侧「我方」视角的发送者 ID（发送者在右、接收者在左）；群聊传 ''（全部左侧）
  mineSenderId?: string
  // 关键字过滤（可选；点击搜索/刷新后调 reload() 生效）
  kw?: string
}>()

const list = ref<Array<Record<string, any>>>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const scrollEl = ref<HTMLElement>()
// 文件下载中状态：key = msgId
const dlLoading = ref<Record<string, boolean>>({})

// 文件消息下载：取签名直链后新窗口打开（管理员可下载任意附件）
async function onDownloadFile(m: Record<string, any>) {
  if (!fileIdOf(m)) { Message.warning('该文件无 fileId，可能是旧消息'); return }
  const key = String(m.msgId || fileIdOf(m))
  if (dlLoading.value[key]) return
  dlLoading.value[key] = true
  try {
    await downloadAdminFile(m)
  } finally {
    dlLoading.value[key] = false
  }
}

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
function fmt(t?: string) {
  return t ? new Date(t).toLocaleString() : '-'
}
function isMine(m: Record<string, any>) {
  return (props.mineSenderId || '') !== '' && String(m.senderId) === props.mineSenderId
}

async function load(p = 1) {
  if (!props.convId) return
  loading.value = true
  try {
    const { data } = await adminApi.messages({ convId: props.convId, kw: props.kw || undefined, page: p, size: 50 })
    if (data?.code !== 0) { Message.error(data?.message || '读取会话消息失败'); return }
    const rows = (data.data?.list || []) as Array<Record<string, any>>
    total.value = data.data?.total || 0
    // 接口按 msgId 倒序返回，正序展示；标记日期分组
    rows.forEach((m) => {
      const d = m.createdAt ? new Date(m.createdAt) : null
      m.__day = d ? `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}` : ''
    })
    rows.reverse()
    list.value = [...rows, ...list.value]
    await nextTick()
    scrollToBottom()
  } finally {
    loading.value = false
  }
}

function onScroll() {
  const el = scrollEl.value
  if (!el || loading.value) return
  // 顶部滚动加载更早消息
  if (el.scrollTop < 60 && list.value.length < total.value) {
    page.value++
    load(page.value).then(() => { if (el.scrollTop < 10) el.scrollTop = 10 })
  }
}

function scrollToBottom() {
  const el = scrollEl.value
  if (el) el.scrollTop = el.scrollHeight
}

// 外部搜索/刷新入口：清空后从第 1 页重新拉取
function reload() {
  list.value = []
  total.value = 0
  page.value = 1
  load(1)
}

onMounted(() => load(1))
defineExpose({ reload })
</script>

<style scoped>
.conv-window {
  height: 460px; overflow-y: auto;
  background: var(--app-fill-1, #f7f8fa);
  border-radius: 8px; padding: 16px;
  display: flex; flex-direction: column; gap: 14px;
}
.conv-spin { margin: 0 auto; }
.conv-day { text-align: center; font-size: 11px; color: var(--app-text-3); }
.conv-row { display: flex; gap: 10px; align-items: flex-start; }
.conv-row.mine { flex-direction: row-reverse; }
.conv-bubble-wrap { display: flex; flex-direction: column; gap: 4px; max-width: 70%; }
.conv-row.mine .conv-bubble-wrap { align-items: flex-end; }
.conv-sender { font-size: 11px; color: var(--app-text-3); display: flex; align-items: center; gap: 4px; }
.conv-nick { font-weight: var(--app-font-weight-medium, 500); color: var(--app-text-2, #4e5969); }
.conv-short { font-family: ui-monospace, Menlo, Consolas, monospace; color: var(--app-text-4, #c9cdd4); }
.official-tag { flex-shrink: 0; transform: scale(0.85); margin-left: 2px; }
.blocked-tag { margin-left: 4px; }
.conv-bubble {
  background: #fff; border: 1px solid var(--app-border-2, #e5e7eb);
  border-radius: 4px 12px 12px 12px; padding: 8px 12px;
  font-size: 13px; line-height: 1.55; color: var(--app-text-1);
  word-break: break-word; white-space: pre-wrap;
}
.conv-row.mine .conv-bubble { background: var(--app-primary-bg, #e8f3ff); border-color: transparent; border-radius: 12px 4px 12px 12px; }
.conv-bubble .dl-wrap { display: inline-flex; align-items: center; vertical-align: middle; }
.conv-time { font-size: 10.5px; color: var(--app-text-4, #c9cdd4); }
</style>
