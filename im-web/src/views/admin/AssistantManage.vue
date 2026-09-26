<template>
  <div class="asst-page">
    <!-- 左侧分区导航 -->
    <aside class="asst-nav">
      <button
        v-for="s in sections"
        :key="s.key"
        class="nav-btn"
        :class="{ active: activeSection === s.key }"
        @click="activeSection = s.key"
      >
        <component :is="s.icon" />
        <span>{{ s.title }}</span>
      </button>
    </aside>

    <!-- 右侧内容 -->
    <div class="asst-body">
      <!-- ===== 基础设置 ===== -->
      <div v-show="activeSection === 'base'" class="section">
        <h2 class="section-title">基础设置</h2>
        <p class="section-desc">智能助手的开关、名称、头像及自动添加行为</p>

        <a-card class="form-card">
          <a-form layout="vertical" :model="cfg">
            <a-form-item label="启用小助手">
              <a-switch v-model="cfg.enabled" />
            </a-form-item>
            <a-form-item label="助手名称">
              <a-input v-model="cfg.name" placeholder="小助手" />
            </a-form-item>
            <a-form-item label="助手头像">
              <ImageUpload v-model="cfg.avatar" dir="assistant/" round :inline="true" :size="96"
                           hint="圆形预览，建议 128×128。上传后点击「保存配置」生效，点「清除」可恢复默认头像。" />
            </a-form-item>
            <a-form-item label="新注册自动添加">
              <a-switch v-model="cfg.autoAdd" />
            </a-form-item>
            <a-form-item label="自动添加后的欢迎语">
              <a-textarea v-model="cfg.welcomeText" :rows="2" placeholder="你好，我是小助手，有问题随时找我～" />
            </a-form-item>
            <div class="form-actions">
              <a-button type="primary" :loading="saving" @click="saveConfig">保存配置</a-button>
            </div>
          </a-form>
        </a-card>
      </div>

      <!-- ===== 推送消息 ===== -->
      <div v-show="activeSection === 'push'" class="section">
        <h2 class="section-title">推送消息</h2>
        <p class="section-desc">向指定用户主动发送文本/图片消息</p>

        <a-card class="form-card">
          <a-form layout="vertical" :model="push">
            <a-form-item label="目标用户">
              <a-popover position="bl" trigger="click" :popup-visible="pickVisible" @popup-visible-change="(v: boolean) => pickVisible = v">
                <template #content>
                  <div style="width: 320px">
                    <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px">
                      <a-checkbox :model-value="allChecked" @change="(c: any) => toggleAll(!!c)">全选</a-checkbox>
                      <span style="color: #86909c; font-size: 12px">已选 {{ push.userIds.length }} / {{ users.length }} 人</span>
                      <a-button size="mini" type="text" @click="push.userIds = []; allChecked = false">清空</a-button>
                    </div>
                    <div style="max-height: 240px; overflow-y: auto; border: 1px solid var(--color-border-2); border-radius: 6px; padding: 6px 8px">
                      <a-checkbox-group v-model="push.userIds" direction="vertical" style="width: 100%">
                        <a-checkbox v-for="u in userOptions" :key="u.id" :value="u.id" style="margin: 4px 0; width: 100%">
                          {{ u.label }}
                        </a-checkbox>
                      </a-checkbox-group>
                      <div v-if="!userOptions.length" style="text-align: center; color: #86909c; padding: 16px 0">暂无用户</div>
                    </div>
                  </div>
                </template>
                <a-button style="width: 100%">
                  选择用户（已选 {{ push.userIds.length }} 人）
                </a-button>
              </a-popover>
            </a-form-item>
            <a-form-item label="消息内容">
              <a-textarea v-model="push.content" :rows="3" placeholder="输入要发送的文字内容" />
            </a-form-item>
            <a-form-item label="图片（可选）">
              <div style="display: flex; align-items: center; gap: 12px">
                <a-upload :show-file-list="false" :custom-request="(opt: any) => uploadImage(opt.file)">
                  <button type="button" class="tool-btn" :disabled="uploadingImage" title="上传图片">
                    <component :is="iconImage" />
                  </button>
                </a-upload>
                <img v-if="push.fileUrl" :src="push.fileUrl" alt="推送图片"
                     style="width: 56px; height: 56px; border-radius: 8px; object-fit: cover" />
                <a-input v-else v-model="push.fileUrl" placeholder="或直接填图片 URL" style="max-width: 260px" />
              </div>
            </a-form-item>
            <div class="form-actions">
              <a-button type="primary" status="success" :loading="pushing" @click="doPush">立即推送</a-button>
            </div>
          </a-form>
        </a-card>
      </div>

      <!-- ===== 会话消息 ===== -->
      <div v-show="activeSection === 'conv'" class="section conv-section">
        <h2 class="section-title">会话消息</h2>
        <p class="section-desc">查看用户与助手的会话记录，并可以助手身份回复</p>

        <div class="conv-wrap">
          <!-- 左：会话列表 -->
          <aside class="conv-list">
            <div class="conv-list-head">
              <span>会话</span>
              <span class="conv-count">{{ convTotal > convs.length ? `${convs.length} / ${convTotal}` : convs.length }}</span>
            </div>
            <div class="conv-list-body">
              <a-spin :loading="convLoading" style="width: 100%">
                <div v-if="!convs.length && !convLoading" class="conv-empty">暂无助手会话</div>
                <div v-for="cv in convs" :key="cv.userId" class="conv-item"
                     :class="{ active: sel && sel.userId === cv.userId }" @click="openConv(cv)">
                  <div class="conv-avatar" :style="avatarBg(cv.nickname || cv.account || 'U')">
                    <img v-if="cv.avatar" :src="cv.avatar" alt="" />
                    <span v-else>{{ (cv.nickname || cv.account || cv.userId || 'U').toString().slice(0, 1) }}</span>
                    <span class="conv-online-dot" :class="cv.online ? 'online' : ''"></span>
                  </div>
                  <div class="conv-info">
                    <div class="conv-name">
                      <span class="nick">{{ cv.nickname || cv.account || cv.userId }}</span>
                      <span class="time">{{ fmtTime(cv.lastMessage?.createdAt) }}</span>
                    </div>
                    <div class="conv-last">{{ preview(cv.lastMessage) }}</div>
                  </div>
                  <span v-if="cv.unread" class="conv-unread">{{ cv.unread > 99 ? '99+' : cv.unread }}</span>
                </div>
              </a-spin>
            </div>
          </aside>

          <!-- 右：消息记录 + 回复 -->
          <section class="conv-main">
            <template v-if="sel">
              <!-- 聊天 header -->
              <header class="conv-title">
                <div class="conv-title-left">
                  <div class="conv-title-avatar" :style="avatarBg(sel.nickname || sel.account || 'U')">
                    <img v-if="sel.avatar" :src="sel.avatar" alt="" />
                    <span v-else>{{ (sel.nickname || sel.account || sel.userId || 'U').toString().slice(0, 1) }}</span>
                  </div>
                  <div class="conv-title-text">
                    <h3>{{ sel.nickname || sel.account || sel.userId }}</h3>
                    <div class="conv-title-meta">
                      <span class="meta-item"><span class="meta-label">ID</span>{{ sel.userId }}</span>
                      <span v-if="sel.account" class="meta-item"><span class="meta-label">账号</span>{{ sel.account }}</span>
                      <span v-if="sel.phone" class="meta-item"><span class="meta-label">手机</span>{{ sel.phone }}</span>
                      <span v-if="sel.ip" class="meta-item"><span class="meta-label">IP</span>{{ sel.ip }}</span>
                      <span class="meta-item"><span class="meta-label">活跃</span>{{ fmtTime(sel.lastMessage?.createdAt) || '—' }}</span>
                    </div>
                  </div>
                </div>
                <div class="conv-title-right">
                  <a-button size="mini" type="outline" @click="loadMsgs(true)"
                            :loading="msgsLoading" :disabled="!hasMore">
                    <template #icon><component :is="iconRefresh" /></template>
                    加载更早
                  </a-button>
                </div>
              </header>

              <!-- 消息滚动 -->
              <div class="msg-scroll" ref="msgScroll">
                <a-spin :loading="msgsLoading" style="width: 100%">
                  <div v-if="!msgs.length && !msgsLoading" class="conv-empty-msg">
                    <div class="empty-msg-mark">💬</div>
                    <div>暂无消息，开始和用户沟通</div>
                  </div>
                  <div v-for="m in msgs" :key="m.msgId" class="msg-row" :class="{ mine: String(m.senderId) === '-1' }">
                    <!-- 时间胶囊 -->
                    <div class="msg-time-chip">{{ fmtDetailTime(m.createdAt) }}</div>
                    <!-- 消息气泡 -->
                    <div class="msg-bubble" :class="{ recalled: m.recalled }">
                      <template v-if="m.recalled"><span class="recalled-tip">该消息已撤回</span></template>
                      <template v-else-if="m.type === 2">
                        <img v-if="m.content" :src="m.content" class="msg-img" alt="" />
                        <span v-else class="msg-type-badge">[图片]</span>
                      </template>
                      <template v-else-if="m.type === 3">
                        <div class="msg-type-card">
                          <span class="mtc-ico">📄</span>
                          <span class="mtc-text">{{ m.content || '一个文件' }}</span>
                        </div>
                      </template>
                      <template v-else-if="m.type === 7">
                        <div class="msg-type-card call">
                          <span class="mtc-ico">📞</span>
                          <span class="mtc-text">通话记录</span>
                        </div>
                      </template>
                      <template v-else-if="m.type === 8">
                        <div class="msg-type-card redpacket">
                          <span class="mtc-ico">🧧</span>
                          <span class="mtc-text">红包</span>
                        </div>
                      </template>
                      <template v-else-if="m.type === 9">
                        <div class="msg-type-card transfer">
                          <span class="mtc-ico">💸</span>
                          <span class="mtc-text">转账</span>
                        </div>
                      </template>
                      <template v-else><span v-html="formatMsg(m.content)"></span></template>
                    </div>
                  </div>
                </a-spin>
              </div>

              <!-- 回复栏：上传图片 = 纯图标按钮（与表情按钮同款 tool-btn，无文字） -->
              <footer class="reply-bar">
                <div class="reply-tools">
                  <input
                    type="file"
                    ref="replyFileInput"
                    accept="image/*"
                    style="display:none"
                    @change="onReplyFileChange"
                  />
                  <button type="button" class="tool-btn" :disabled="replyUploading" title="上传图片" @click="$refs.replyFileInput.click()">
                    <component :is="iconImage" />
                  </button>
                  <button type="button" class="tool-btn" title="表情" @click="toggleEmoji">
                    <component :is="iconFaceSmile" />
                  </button>
                  <div class="reply-tools-divider"></div>
                  <span v-if="replyFileUrl" class="reply-img-chip">
                    <img :src="replyFileUrl" alt="回复图片" class="reply-img" />
                    <a-button type="text" status="danger" size="mini" @click="replyFileUrl = ''">×</a-button>
                  </span>
                  <span v-else class="reply-tools-hint">支持文本 + 图片；以「助手」身份推送到用户</span>
                </div>
                <!-- emoji 面板（与 App 端表情一致） -->
                <div v-if="emojiOpen" class="emoji-panel">
                  <button v-for="e in emojis" :key="e" type="button" class="emoji-cell" @click="insertEmoji(e)">{{ e }}</button>
                </div>
                <div class="reply-input-row">
                  <a-textarea
                    v-model="replyText"
                    :rows="2"
                    :max-length="2000"
                    allow-clear
                    placeholder="以助手身份回复此用户…（Enter 换行，Ctrl/Cmd + Enter 发送）"
                    @keydown.meta.enter.exact.prevent="sendReply"
                    @keydown.ctrl.enter.exact.prevent="sendReply"
                    class="reply-textarea"
                  />
                  <a-button type="primary" :loading="replying" @click="sendReply" class="send-btn">
                    <template #icon><component :is="iconSend" /></template>
                    发送
                  </a-button>
                </div>
              </footer>
            </template>
            <div v-else class="conv-empty" style="height: 100%">选择左侧会话查看消息</div>
          </section>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, nextTick, computed, watch, markRaw } from 'vue'
import { Message } from '@arco-design/web-vue'
import { IconSettings, IconSend, IconMessage, IconRefresh, IconImage, IconFaceSmileFill } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'
import ImageUpload from './ImageUpload.vue'

const activeSection = ref('base')

const sections = [
  { key: 'base', title: '基础设置', icon: markRaw(IconSettings) },
  { key: 'push', title: '推送消息', icon: markRaw(IconSend) },
  { key: 'conv', title: '会话消息', icon: markRaw(IconMessage) }
]

const cfg = ref({ enabled: false, name: '小助手', avatar: '', autoAdd: false, welcomeText: '' })
const saving = ref(false)
const pushing = ref(false)
const push = ref<{ userIds: string[]; content: string; fileUrl: string }>({ userIds: [], content: '', fileUrl: '' })
const users = ref<Array<{ id: string; label: string }>>([])
const uploadingImage = ref(false)
const allChecked = ref(false)
const pickVisible = ref(false)

const userOptions = computed(() => users.value)

// ===== 助手会话消息 =====
const convs = ref<Array<Record<string, any>>>([])
const convTotal = ref(0) // 会话总数（列表只显示最近活跃的前 50 个）
const convLoading = ref(false)
const sel = ref<Record<string, any> | null>(null)
const msgs = ref<Array<Record<string, any>>>([])
// msgs 按 新→旧 排序（后端 assistantMessages 返回就是新→旧，直接用）
// 渲染直接用 msgs，最新在列表顶部，打开会话即看到最新消息
const msgsLoading = ref(false)
const replyText = ref('')
const replying = ref(false)
const hasMore = ref(false)
const replyFileUrl = ref('') // 回复可附图片（MinIO URL）
const replyUploading = ref(false)
const replyFileInput = ref<HTMLInputElement | null>(null)
const msgScroll = ref<HTMLElement | null>(null)
const iconRefresh = markRaw(IconRefresh)
const iconSend = markRaw(IconSend)
const iconImage = markRaw(IconImage)
const iconFaceSmile = markRaw(IconFaceSmileFill)

// ===== 表情选择（与 App 端 chat_page.dart _emojis 完全一致） =====
const emojiOpen = ref(false)
const emojis = [
  '😀', '😄', '😁', '😂', '😊', '😍', '🥰', '😘',
  '😎', '🤔', '😅', '😭', '😡', '👍', '👏', '🙏',
  '💪', '🎉', '❤️', '💙', '🔥', '✨', '✅', '👀',
  '🙌', '🤝', '🌹', '🎁', '🍵', '☕', '📌', '💡'
]
function toggleEmoji() {
  emojiOpen.value = !emojiOpen.value
}
function insertEmoji(e: string) {
  replyText.value += e
}

/** 首字母取色渐变，做头像背景 */
function avatarBg(name: any): Record<string, string> {
  const s = String(name || 'U').charAt(0).toLowerCase()
  const code = s.charCodeAt(0) || 65
  const palettes = [
    ['#3b82f6', '#6366f1'],
    ['#14b8a6', '#3b82f6'],
    ['#8b5cf6', '#ec4899'],
    ['#f59e0b', '#ef4444'],
    ['#22c55e', '#14b8a6'],
    ['#f97316', '#ef4444'],
    ['#06b6d4', '#8b5cf6'],
    ['#64748b', '#334155'],
  ]
  const [a, b] = palettes[code % palettes.length]
  return { background: `linear-gradient(135deg, ${a} 0%, ${b} 100%)` }
}

/** 文本消息：URL 变链接；简单换行保留 */
function formatMsg(content: any): string {
  if (!content) return ''
  const esc = String(content).replace(/[&<>"']/g, (c: string) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] || c))
  const linked = esc.replace(/(https?:\/\/[^\s<"'，,。；;]+)/g, (u: string) => {
    const safe = u.replace(/"/g, '%22')
    return `<a href="${safe}" target="_blank" rel="noopener" class="msg-link">${u}</a>`
  })
  return linked.replace(/\n/g, '<br>')
}

/** 详情时间：HH:mm:ss + MM-DD */
function fmtDetailTime(t: any): string {
  if (!t) return ''
  const d = new Date(t)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  const now = new Date()
  const today = d.toDateString() === now.toDateString()
  const datePart = today ? '' : `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} `
  return `${datePart}${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/** 滚到最新位置 —— msgs 新→旧，最新在顶部，滚到 scrollTop = 0 */
function scrollToLatest() {
  nextTick(() => {
    requestAnimationFrame(() => {
      if (msgScroll.value) msgScroll.value.scrollTop = 0
    })
  })
}

async function loadConvs() {
  convLoading.value = true
  try {
    // 新后端返回 {list,total}（按最近消息倒序的前 limit 个）；
    // 旧后端返回裸数组——两种都兼容，避免 im-server 未重启时列表显示为空
    const { data } = await adminApi.assistantConversations({ limit: 50 })
    if (data.code === 0) {
      const d = data.data as any
      if (Array.isArray(d)) {
        convs.value = d
        convTotal.value = d.length
      } else {
        convs.value = d?.list || []
        convTotal.value = Number(d?.total || 0)
      }
    }
  } catch (_) {} finally { convLoading.value = false }
}

async function openConv(cv: Record<string, any>) {
  sel.value = cv
  msgs.value = []
  hasMore.value = false
  await loadMsgs()
}

/** 拉取消息：默认只拉最近 20 条；older=true 时向前翻页（更早的消息追加到末尾）
 * 后端 assistantMessages 返回顺序 = 新→旧；msgs 直接按此存储，模板 v-for 渲染时最新在顶部
 */
async function loadMsgs(older = false) {
  if (!sel.value) return
  msgsLoading.value = true
  try {
    // 翻页时：取当前数组末尾（最旧那条）的 msgId 作为 beforeMsgId，向后端要"更早"的消息
    const before = older && msgs.value.length
      ? (msgs.value[msgs.value.length - 1].msgId as string | number)
      : undefined
    const PAGE = 20
    const { data } = await adminApi.assistantMessages({
      userId: sel.value.userId as string, beforeMsgId: before, limit: PAGE
    })
    if (data.code === 0) {
      const list: Array<Record<string, any>> = data.data || []
      hasMore.value = list.length >= PAGE
      // 后端返回 新→旧，直接用；older=true 时更早消息（后端同样 新→旧）追加到末尾
      msgs.value = older ? [...msgs.value, ...list] : list
      // 首次加载滚到顶部（最新在顶）；翻页时保持当前滚动位置，不跳动
      if (!older) scrollToLatest()
    } else Message.error(data.message)
  } catch (e: any) {
    Message.error('消息加载失败：' + (e.message || e))
  } finally { msgsLoading.value = false }
}

async function sendReply() {
  if (!sel.value) return
  const content = replyText.value.trim()
  if (!content && !replyFileUrl.value) { Message.warning('请输入回复内容或附上图片'); return }
  replying.value = true
  try {
    const { data } = await adminApi.assistantPush({
      userIds: [String(sel.value.userId)],
      content,
      fileUrl: replyFileUrl.value || undefined
    })
    if (data.code === 0) {
      Message.success('已回复')
      replyText.value = ''
      replyFileUrl.value = ''
      await loadMsgs()
      await loadConvs()
    } else Message.error(data.message)
  } finally { replying.value = false }
}

/** 原生 input[type=file] change 回调 */
function onReplyFileChange(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (file) uploadReplyImage(file)
  input.value = '' // 重置，允许再次选同一文件
}

/** 回复图片上传（复用推送图片的上传通道） */
async function uploadReplyImage(file: File) {
  replyUploading.value = true
  try {
    replyFileUrl.value = await uploadToMinio(file, 'assistant/')
    Message.success('图片已上传')
  } catch (e: any) {
    Message.error('图片上传失败：' + (e.message || e))
  } finally { replyUploading.value = false }
}

function fmtTime(t: any): string {
  if (!t) return ''
  const d = new Date(t)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}-${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function preview(m: any): string {
  if (!m) return ''
  if (m.recalled) return '[已撤回]'
  const map: Record<number, string> = { 2: '[图片]', 3: '[文件]', 7: '[通话]', 8: '[红包]', 9: '[转账]' }
  if (map[m.type]) return map[m.type]
  return (m.content || '').slice(0, 40)
}

// 勾选变化时同步"全选"状态
watch(() => push.value.userIds, (v: string[]) => {
  allChecked.value = users.value.length > 0 && v.length === users.value.length
}, { deep: true })

function toggleAll(checked: boolean) {
  push.value.userIds = checked ? users.value.map(u => u.id) : []
}
function toggleOne(id: string) {
  const i = push.value.userIds.indexOf(id)
  if (i >= 0) push.value.userIds.splice(i, 1)
  else push.value.userIds.push(id)
  allChecked.value = push.value.userIds.length === users.value.length && users.value.length > 0
}

/** 通用上传：admin 接口 → MinIO，返回 URL
 * 走 adminApi.uploadFile（axios），不再用裸 fetch('/api/v1/upload')，
 * 否则反向代环境下 multipart body 可能丢失 → 后端 FormFile("file") 拿不到 → 报 1001「缺少文件」。
 */
async function uploadToMinio(file: File, dir: string): Promise<string> {
  return adminApi.uploadFile(file, dir)
}

async function uploadImage(file: File) {
  uploadingImage.value = true
  try {
    push.value.fileUrl = await uploadToMinio(file, 'assistant/')
    Message.success('图片已上传')
  } catch (e: any) {
    Message.error('图片上传失败：' + (e.message || e))
  } finally {
    uploadingImage.value = false
  }
}

function selectAll() {
  push.value.userIds = users.value.map(u => u.id)
  allChecked.value = true
}

onMounted(async () => {
  // 读配置
  try {
    const { data } = await adminApi.assistantConfigGet()
    if (data.code === 0 && data.data) {
      const d: any = data.data
      cfg.value = {
        enabled: !!d.enabled,
        name: String(d.name || '小助手'),
        avatar: String(d.avatar || ''),
        autoAdd: !!d.autoAdd,
        welcomeText: String(d.welcomeText || '')
      }
    }
  } catch (_) {}
  // 助手会话列表
  loadConvs()
  // 用户列表（推送目标）
  try {
    const { data } = await adminApi.users({ page: 1, size: 200 })
    if (data.code === 0) {
      const list: any[] = data.data?.list || []
      users.value = list.map(u => ({ id: String(u.id), label: `${u.nickname || ''} (${u.account || ''})` }))
    }
  } catch (_) {}
})

async function saveConfig() {
  saving.value = true
  try {
    const { data } = await adminApi.assistantConfigSet(cfg.value)
    if (data.code === 0) Message.success('配置已保存')
    else Message.error(data.message)
  } finally {
    saving.value = false
  }
}

async function doPush() {
  if (!push.value.userIds.length) { Message.warning('请选择目标用户'); return }
  if (!push.value.content && !push.value.fileUrl) { Message.warning('请输入消息内容或图片'); return }
  pushing.value = true
  try {
    const { data } = await adminApi.assistantPush({
      userIds: push.value.userIds,
      content: push.value.content,
      fileUrl: push.value.fileUrl
    })
    if (data.code === 0) {
      Message.success(`已推送 ${push.value.userIds.length} 人`)
      push.value = { userIds: [], content: '', fileUrl: '' }
    } else Message.error(data.message)
  } finally {
    pushing.value = false
  }
}
</script>

<style scoped>
/* ===== 主布局：左导航 + 右内容（与 ConfigView 一致） ===== */
.asst-page { display: flex; gap: var(--app-space-lg); height: 100%; min-height: 600px; }

/* 左侧分区导航 */
.asst-nav {
  width: 180px;
  display: flex; flex-direction: column; gap: 4px;
  padding: 12px;
  background: var(--app-bg-card);
  border: 1px solid var(--app-border-2);
  border-radius: var(--app-radius-lg);
  box-shadow: var(--app-shadow-card);
  flex-shrink: 0;
  height: fit-content;
  position: sticky; top: 0;
}
.nav-btn {
  display: flex; align-items: center; gap: 10px;
  width: 100%; padding: 10px 12px;
  background: transparent; border: none;
  border-radius: var(--app-radius-md);
  color: var(--app-text-2);
  font-size: var(--app-font-size-base);
  cursor: pointer; text-align: left;
  transition: background var(--app-transition-base), color var(--app-transition-base);
}
.nav-btn:hover { background: var(--app-border-2); color: var(--app-text-1); }
.nav-btn.active {
  background: var(--app-primary-bg);
  color: var(--app-primary);
  font-weight: var(--app-font-weight-medium);
}
.nav-btn :deep(svg) { width: 18px; height: 18px; flex-shrink: 0; }

/* 右侧内容 */
.asst-body { flex: 1; min-width: 0; }
.section { max-width: 820px; }
.section-title { margin: 0 0 6px; font-size: var(--app-font-size-xl); font-weight: var(--app-font-weight-semibold); color: var(--app-text-1); }
.section-desc { margin: 0 0 16px; font-size: var(--app-font-size-sm); color: var(--app-text-3); }
.form-card { border-radius: var(--app-radius-lg); }
.form-card :deep(.arco-form-item-label) { padding-bottom: 6px; font-weight: 500; }
.form-card :deep(.arco-form-item) { margin-bottom: 18px; }
.form-actions {
  display: flex; align-items: center; justify-content: flex-start;
  padding-top: 8px;
}

/* ===== 会话消息区域 ===== */
.conv-section { max-width: none; }
.conv-wrap {
  display: flex;
  gap: 16px;
  height: 640px;
  margin-top: 8px;
}

/* ========== 左侧：会话列表 ========== */
.conv-list {
  width: 320px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  background: var(--app-bg-card);
  border: 1px solid var(--app-border-2);
  border-radius: 14px;
  box-shadow: var(--app-shadow-card);
  overflow: hidden;
}
.conv-list-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 14px 16px;
  border-bottom: 1px dashed var(--app-border-2);
  font-size: 14px;
  font-weight: 600;
  color: var(--app-text-1);
}
.conv-count {
  min-width: 22px;
  padding: 0 8px;
  height: 20px;
  border-radius: 10px;
  background: #165dff;
  color: #fff;
  font-size: 11px;
  font-weight: 600;
  display: inline-flex; align-items: center; justify-content: center;
  box-shadow: 0 2px 6px rgba(22,93,255,.3);
}
.conv-list-body {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}
.conv-list-body::-webkit-scrollbar { width: 6px; }
.conv-list-body::-webkit-scrollbar-thumb {
  background: rgba(22,93,255,.3);
  border-radius: 3px;
}
.conv-item {
  position: relative;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 10px;
  margin-bottom: 2px;
  border-radius: 12px;
  cursor: pointer;
  overflow: hidden;
  transition: transform .2s ease, background .2s ease;
}
.conv-item::before {
  content: '';
  position: absolute;
  inset: 0;
  background: rgba(22,93,255,.06);
  opacity: 0;
  transition: opacity .25s ease;
  pointer-events: none;
}
.conv-item:hover { transform: translateX(2px); }
.conv-item:hover::before { opacity: 1; }
.conv-item.active {
  background: rgba(22,93,255,.10);
  box-shadow: inset 0 0 0 1px rgba(22,93,255,.18);
}
.conv-item.active::after {
  content: '';
  position: absolute;
  left: 0; top: 20%; bottom: 20%;
  width: 3px;
  border-radius: 0 3px 3px 0;
  background: #165dff;
  box-shadow: 0 0 8px rgba(22,93,255,.6);
}
/* 会话头像 */
.conv-avatar {
  position: relative;
  width: 40px; height: 40px;
  border-radius: 12px;
  overflow: hidden;
  flex-shrink: 0;
  display: flex; align-items: center; justify-content: center;
  color: #fff;
  font-weight: 600;
  font-size: 15px;
  box-shadow: 0 2px 8px rgba(0,0,0,.12);
}
.conv-avatar img {
  width: 100%; height: 100%;
  object-fit: cover;
  display: block;
}
.conv-online-dot {
  position: absolute;
  right: 2px; bottom: 2px;
  width: 10px; height: 10px;
  border-radius: 50%;
  background: #c9cdd4;
  border: 2px solid #fff;
}
.conv-online-dot.online {
  background: #00b42a;
  box-shadow: 0 0 0 2px rgba(0,180,42,.22);
  animation: online-pulse 1.8s infinite;
}
@keyframes online-pulse {
  0%, 100% { box-shadow: 0 0 0 2px rgba(0,180,42,.22); }
  50%      { box-shadow: 0 0 0 5px rgba(0,180,42,.05); }
}
/* 会话未读红点 */
.conv-unread {
  min-width: 18px; height: 18px;
  padding: 0 5px;
  border-radius: 9px;
  background: linear-gradient(135deg, #f53f3f 0%, #ff7d00 100%);
  color: #fff;
  font-size: 11px; font-weight: 600;
  display: inline-flex; align-items: center; justify-content: center;
  flex-shrink: 0;
  box-shadow: 0 2px 8px rgba(245,63,63,.35);
}
.conv-info { min-width: 0; flex: 1; }
.conv-name {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}
.conv-name .nick {
  font-size: 14px;
  font-weight: 600;
  color: var(--app-text-1);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.conv-name .time { font-size: 11px; color: #86909c; flex-shrink: 0; }
.conv-last {
  font-size: 12px;
  color: #86909c;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  margin-top: 3px;
}

/* ========== 右侧：消息区 ========== */
.conv-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  background: var(--app-bg-card);
  border: 1px solid var(--app-border-2);
  border-radius: 14px;
  box-shadow: var(--app-shadow-card);
  overflow: hidden;
}

/* 顶部聊天头 */
.conv-title {
  position: relative;
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 18px;
  border-bottom: 1px solid var(--app-border-2);
  background: var(--app-bg-card);
}
.conv-title::after {
  content: '';
  position: absolute;
  left: 18px; right: 18px; bottom: 0;
  height: 1px;
  background: rgba(22,93,255,.2);
}
.conv-title-left {
  display: flex; align-items: center; gap: 12px;
}
.conv-title-avatar {
  width: 44px; height: 44px;
  border-radius: 14px;
  color: #fff;
  display: flex; align-items: center; justify-content: center;
  font-weight: 600; font-size: 16px;
  box-shadow: 0 2px 10px rgba(0,0,0,.14);
  overflow: hidden;
  flex-shrink: 0;
}
.conv-title-avatar img { width: 100%; height: 100%; object-fit: cover; }
.conv-title-text h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--app-text-1);
}
.conv-title-meta {
  display: flex; flex-wrap: wrap; align-items: center; gap: 4px 14px;
  font-size: 12px;
  color: #86909c;
  margin-top: 4px;
  line-height: 1.5;
}
.meta-item {
  display: inline-flex; align-items: center; gap: 4px;
}
.meta-label {
  color: #c9cdd4;
  font-size: 11px;
  background: #f2f3f5;
  padding: 1px 5px;
  border-radius: 3px;
  margin-right: 2px;
}
.conv-title-right { display: flex; gap: 8px; align-items: center; }
.conv-title-right :deep(.arco-btn) { border-radius: 8px; }

/* 消息滚动区 */
.msg-scroll {
  flex: 1;
  overflow-y: auto;
  padding: 20px 28px;
  scroll-behavior: smooth;
}
.msg-scroll::-webkit-scrollbar { width: 7px; }
.msg-scroll::-webkit-scrollbar-thumb {
  background: rgba(22,93,255,.35);
  border-radius: 4px;
}

/* 空态 */
.conv-empty-msg {
  height: 100%;
  min-height: 280px;
  display: flex; flex-direction: column;
  align-items: center; justify-content: center;
  gap: 12px;
  color: #86909c;
  font-size: 13px;
}
.empty-msg-mark {
  width: 64px; height: 64px;
  border-radius: 22px;
  background: rgba(22,93,255,.08);
  display: flex; align-items: center; justify-content: center;
  font-size: 30px;
  box-shadow: inset 0 0 0 1px rgba(22,93,255,.2);
}

/* 消息行 */
.msg-row {
  display: flex;
  flex-direction: column;
  margin-bottom: 18px;
  align-items: flex-start;
  animation: msg-in .42s cubic-bezier(.22,.61,.36,1) both;
}
@keyframes msg-in {
  from { opacity: 0; transform: translateY(8px) scale(.98); }
  to   { opacity: 1; transform: translateY(0) scale(1); }
}
.msg-row.mine {
  align-items: flex-end;
}

/* 时间胶囊 */
.msg-time-chip {
  align-self: center;
  margin: 4px 0 10px;
  padding: 3px 10px;
  border-radius: 20px;
  font-size: 11px;
  color: #86909c;
  background: rgba(134,144,156,.08);
  border: 1px solid rgba(134,144,156,.15);
}
.msg-row.mine .msg-time-chip { order: 0; }

/* ========== 消息气泡 ========== */
.msg-bubble {
  position: relative;
  display: inline-block;
  max-width: min(72%, 560px);
  padding: 10px 14px;
  border-radius: 6px 14px 14px 14px;
  background: #ffffff;
  border: 1px solid #e5e6eb;
  box-shadow:
    0 1px 2px rgba(31,35,41,.04),
    0 4px 14px rgba(31,35,41,.06);
  font-size: 13px;
  line-height: 1.65;
  color: var(--app-text-1);
  word-break: break-word;
  white-space: normal;
  text-align: left;
  transition: transform .18s ease, box-shadow .18s ease;
}
.msg-bubble:hover {
  transform: translateY(-1px);
  box-shadow:
    0 2px 3px rgba(31,35,41,.06),
    0 10px 24px rgba(31,35,41,.09);
}
/* 气泡尾巴：用户发（左） */
.msg-bubble::before {
  content: '';
  position: absolute;
  top: 10px; left: -7px;
  width: 0; height: 0;
  border-style: solid;
  border-width: 0 8px 8px 0;
  border-color: transparent #ffffff transparent transparent;
  filter: drop-shadow(-1px 1px 0 #e5e6eb);
}

/* 助手消息（我发送的）右侧蓝色气泡 */
.msg-row.mine .msg-bubble {
  color: #fff;
  background: #165dff;
  border: none;
  border-radius: 14px 6px 14px 14px;
  box-shadow:
    0 2px 5px rgba(22,93,255,.22),
    0 6px 16px rgba(22,93,255,.18);
}
.msg-row.mine .msg-bubble::before {
  left: auto;
  right: -7px;
  border-width: 8px 8px 0 0;
  border-color: #165dff transparent transparent transparent;
  filter: none;
}
.msg-bubble.recalled {
  opacity: .6;
  background: repeating-linear-gradient(45deg, #f7f8fa, #f7f8fa 6px, #eef0f3 6px, #eef0f3 12px);
  color: #86909c;
  border: 1px dashed #c9cdd4;
  font-style: italic;
  box-shadow: none;
}
.msg-bubble.recalled::before { display: none; }
.recalled-tip { color: #86909c; }
.msg-link {
  color: inherit;
  text-decoration: underline;
  text-decoration-color: rgba(255,255,255,.35);
  word-break: break-all;
}
.msg-row:not(.mine) .msg-link {
  color: #165dff;
  text-decoration-color: rgba(22,93,255,.3);
}

/* 图片消息 */
.msg-img {
  max-width: 260px;
  max-height: 280px;
  border-radius: 10px;
  display: block;
  box-shadow: 0 4px 14px rgba(0,0,0,.12);
  transition: transform .25s ease, box-shadow .25s ease;
}
.msg-img:hover {
  transform: scale(1.03);
  box-shadow: 0 10px 30px rgba(0,0,0,.18);
}
.msg-type-badge {
  display: inline-block;
  padding: 6px 12px;
  border-radius: 10px;
  background: rgba(22,93,255,.08);
  color: #165dff;
  font-weight: 600;
}

/* 类型卡：文件/通话/红包/转账 */
.msg-type-card {
  display: inline-flex; align-items: center; gap: 10px;
  padding: 10px 14px;
  min-width: 200px;
  border-radius: 12px;
  background: #f0f5ff;
  border: 1px solid rgba(22,93,255,.18);
}
.msg-type-card.call {
  background: linear-gradient(135deg, #e6fffb 0%, #e0f2fe 100%);
  border-color: rgba(20,184,166,.2);
}
.msg-type-card.redpacket {
  background: linear-gradient(135deg, #fff1f0 0%, #fff7e6 100%);
  border-color: rgba(245,63,63,.2);
}
.msg-type-card.transfer {
  background: linear-gradient(135deg, #f6ffed 0%, #e6fffb 100%);
  border-color: rgba(0,180,42,.2);
}
.mtc-ico { font-size: 20px; line-height: 1; }
.mtc-text { font-size: 13px; font-weight: 500; color: var(--app-text-1); }

/* ========== 底部回复栏 ========== */
.reply-bar {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 18px 16px;
  border-top: 1px solid var(--app-border-2);
  background: var(--app-bg-card);
}
.reply-tools {
  display: flex; align-items: center; gap: 8px;
  color: #86909c;
  font-size: 12px;
}
.tool-btn {
  width: 32px; height: 32px;
  border-radius: 10px;
  background: #f7f8fa;
  border: 1px solid var(--app-border-2);
  font-size: 16px;
  cursor: pointer;
  color: #4e5969;
  transition: all .18s ease;
  display: inline-flex; align-items: center; justify-content: center;
}
.tool-btn svg { color: inherit; }
.tool-btn:not(:disabled):hover {
  background: rgba(22,93,255,.08);
  border-color: rgba(22,93,255,.25);
  transform: translateY(-1px);
  color: #165dff;
}
.tool-btn:disabled { opacity: .4; cursor: not-allowed; }
.reply-tools :deep(.arco-btn) { width: 32px; height: 32px; padding: 0; }
.reply-tools-divider {
  width: 1px; height: 18px;
  background: var(--app-border-2);
  margin: 0 6px;
}
.reply-tools-hint {
  margin-left: 4px;
  color: #86909c;
}
.reply-img-chip {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 4px;
  border-radius: 10px;
  background: #f7f8fa;
  border: 1px solid var(--app-border-2);
}
.reply-img {
  width: 44px; height: 44px;
  border-radius: 8px;
  object-fit: cover;
}

.reply-input-row {
  display: flex;
  gap: 10px;
  align-items: flex-end;
}
.reply-textarea {
  flex: 1;
}
.reply-textarea :deep(.arco-textarea-wrapper) {
  border-radius: 12px;
  transition: border-color .18s ease, box-shadow .18s ease;
}
.reply-textarea :deep(.arco-textarea-wrapper:hover),
.reply-textarea :deep(.arco-textarea-wrapper.arco-textarea-focus) {
  border-color: rgba(22,93,255,.45);
  box-shadow: 0 0 0 4px rgba(22,93,255,.08);
}
.send-btn {
  height: 44px;
  padding: 0 20px;
  border-radius: 12px;
  font-weight: 600;
  background: #165dff;
  border: none;
  box-shadow: 0 4px 14px rgba(22,93,255,.3);
  transition: transform .18s ease, box-shadow .18s ease, filter .18s ease;
}
.send-btn:hover {
  transform: translateY(-1px);
  box-shadow: 0 8px 24px rgba(22,93,255,.4);
  filter: brightness(1.05);
}
.send-btn:active { transform: translateY(0); }

/* ========== 通用空态 ========== */
.conv-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  color: #86909c;
  font-size: 13px;
  padding: 24px 0;
  height: 100%;
}

/* ========== emoji 面板 ========== */
.emoji-panel {
  display: grid;
  grid-template-columns: repeat(8, 1fr);
  gap: 2px;
  padding: 8px;
  border: 1px solid var(--app-border-2);
  border-radius: 12px;
  background: var(--app-bg-card);
  max-height: 148px;
  overflow-y: auto;
}
.emoji-cell {
  width: 34px; height: 34px;
  display: inline-flex; align-items: center; justify-content: center;
  font-size: 20px;
  border: none;
  background: transparent;
  border-radius: 8px;
  cursor: pointer;
  line-height: 1;
  transition: background .15s ease, transform .15s ease;
}
.emoji-cell:hover {
  background: rgba(22,93,255,.08);
  transform: scale(1.15);
}
.tool-btn :deep(svg),
.tool-btn svg {
  width: 18px; height: 18px;
}
</style>
