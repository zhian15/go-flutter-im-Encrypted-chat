<template>
  <div class="page">
    <a-card title="群组管理">
      <div class="toolbar">
        <a-input
          v-model="query.kw"
          placeholder="群名 / 群主昵称"
          allow-clear
          style="width: 260px"
          @press-enter="doSearch"
        />
        <a-button type="primary" @click="doSearch">查询</a-button>
        <a-button @click="resetQuery">重置</a-button>
      </div>

      <a-table
        :data="list"
        row-key="id"
        :loading="loading"
        :pagination="pagination"
        :scroll="{ x: 1180 }"
        @page-change="load"
      >
        <template #columns>
          <a-table-column title="群名称" :width="280">
            <template #cell="{ record }">
              <div class="group-cell">
                <span class="group-avatar" :style="{ background: avatarColor(record.id) }">
                  <img v-if="record.avatar" :src="record.avatar" alt="" />
                  <template v-else>{{ (record.nameZh || '群').slice(0, 1) }}</template>
                </span>
                <div class="group-info">
                  <span class="name">{{ record.nameZh || '(未命名)' }}</span>
                  <span v-if="record.nameEn" class="sub">{{ record.nameEn }}</span>
                </div>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="群主" :width="180">
            <template #cell="{ record }">
              <div class="group-cell">
                <span class="group-avatar owner-avatar" :style="{ background: memberColor(record.ownerId || record.id) }">
                  <img v-if="record.ownerAvatar" :src="record.ownerAvatar" alt="" />
                  <template v-else>{{ (record.ownerNickname || '群').slice(0, 1).toUpperCase() }}</template>
                </span>
                <div class="group-info">
                  <span class="name">{{ record.ownerNickname || '—' }}</span>
                  <span class="sub">{{ record.ownerShortId ? 'ID #' + record.ownerShortId : '—' }}</span>
                </div>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="成员数" :width="90">
            <template #cell="{ record }">
              <a-tag color="gray" v-if="record.memberCount != null">{{ record.memberCount }}</a-tag>
              <span class="muted" v-else>—</span>
            </template>
          </a-table-column>
          <a-table-column title="人数上限" :width="100">
            <template #cell="{ record }">
              <span v-if="record.maxMembers">{{ record.maxMembers }}</span>
              <span class="muted" v-else>不限</span>
            </template>
          </a-table-column>
          <a-table-column title="创建时间" :width="170">
            <template #cell="{ record }">{{ fmt(record.createdAt) }}</template>
          </a-table-column>
          <a-table-column title="操作" :width="360" fixed="right">
            <template #cell="{ record }">
              <a-button size="mini" @click="openEdit(record)">
                <template #icon><IconEdit /></template>编辑
              </a-button>
              <a-button size="mini" type="outline" style="margin-left: 6px" @click="openTransfer(record)">
                <template #icon><IconSwap /></template>转移群主
              </a-button>
              <a-button size="mini" type="outline" style="margin-left: 6px" @click="openMembers(record)">
                <template #icon><IconUserGroup /></template>成员
              </a-button>
              <a-button size="mini" type="outline" style="margin-left: 6px" @click="openMessages(record)">
                <template #icon><IconMessage /></template>消息
              </a-button>
            </template>
          </a-table-column>
        </template>
      </a-table>
      <a-empty v-if="!list.length && !loading" :description="'暂无群聊'" />
    </a-card>

    <!-- 编辑群资料弹窗 -->
    <a-modal
      v-model:visible="showEdit"
      :title="'编辑群资料 · ' + (currentGroup?.nameZh || currentGroup?.id || '')"
      :ok-loading="saving"
      :mask-closable="false"
      ok-text="保存"
      cancel-text="取消"
      width="520"
      @before-ok="submitEdit"
    >
      <a-form :model="editForm" layout="vertical">
        <a-form-item label="群名称">
          <a-input v-model="editForm.name" placeholder="请输入群名称" :max-length="50" show-word-limit />
        </a-form-item>
        <a-form-item label="群头像">
          <div class="avatar-row">
            <span class="avatar-preview">
              <img v-if="editForm.avatar" :src="editForm.avatar" alt="" />
              <template v-else>{{ (editForm.name || '群').slice(0, 1) }}</template>
            </span>
            <a-input v-model="editForm.avatar" placeholder="群头像 URL（留空则不修改）" allow-clear />
          </div>
        </a-form-item>
        <a-form-item label="群公告">
          <a-textarea
            v-model="editForm.announcement"
            placeholder="请输入群公告"
            :auto-size="{ minRows: 4, maxRows: 8 }"
            :max-length="500"
            show-word-limit
          />
        </a-form-item>
        <a-form-item label="人数上限">
          <a-input-number v-model="editForm.maxMembers" :min="0" :max="100000" :precision="0" style="width: 180px" />
          <span class="hint">0 表示不限</span>
        </a-form-item>
      </a-form>

      <template #footer>
        <div class="edit-footer">
          <a-popconfirm content="确认解散该群？解散后不可恢复" @ok="confirmDisbandFromEdit">
            <a-button status="danger">解散群聊</a-button>
          </a-popconfirm>
          <a-space>
            <a-button @click="showEdit = false">取消</a-button>
            <a-button type="primary" :loading="saving" @click="submitEdit">保存</a-button>
          </a-space>
        </div>
      </template>
    </a-modal>

    <!-- 转移群主弹窗 -->
    <a-modal
      v-model:visible="showTransfer"
      :title="'转移群主 · ' + (currentGroup?.nameZh || currentGroup?.id || '')"
      :ok-loading="transferring"
      :mask-closable="false"
      ok-text="确认转移"
      cancel-text="取消"
      width="520"
      @before-ok="submitTransfer"
    >
      <a-form :model="transferForm" layout="vertical">
        <a-form-item label="新群主">
          <a-select
            v-model="transferForm.newOwnerId"
            placeholder="搜索群成员（昵称 / 账号 / ID），或直接输入用户 ID"
            allow-clear
            allow-create
            allow-search
            :loading="ownerLoading"
            style="width: 100%"
            @dropdown-reach-bottom="loadMoreOwnerOptions"
          >
            <a-option v-for="o in ownerOptions" :key="o.value" :value="o.value">{{ o.label }}</a-option>
          </a-select>
          <span class="hint">仅可从本群成员中选择；转移后原群主将变为普通成员</span>
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 群成员列表弹窗 -->
    <a-modal
      v-model:visible="showMembers"
      :title="'群成员 · ' + (currentGroup?.nameZh || currentGroup?.id || '')"
      :footer="false"
      width="620"
    >
      <div class="member-list">
        <div class="member-row header">
          <span>成员</span><span>角色</span><span>加入时间</span>
        </div>
        <a-spin v-if="membersLoading" style="display:block;text-align:center;padding:40px 0" />
        <template v-else>
          <div v-for="(m, i) in members" :key="m.userId || m.id || i" class="member-row">
            <div class="member-user">
              <span class="avatar" :style="{ background: memberColor(m.userId || m.id) }">
                <img v-if="m.avatar" :src="m.avatar" />
                <template v-else>{{ (m.nickname || m.account || '?').toString().slice(0, 1).toUpperCase() }}</template>
              </span>
              <div class="mi">
                <span class="mnick">{{ m.nickname || '—' }}</span>
                <span class="muid">
                  <span v-if="m.account">{{ m.account }}</span>
                  <span v-if="m.shortId">{{ m.account ? ' · ID #' : 'ID #' }}{{ m.shortId }}</span>
                  <span v-if="!m.account && !m.shortId">—</span>
                </span>
              </div>
            </div>
            <span>
              <a-tag v-if="roleKey(m) === 1" color="gold">{{ roleText(m) }}</a-tag>
              <a-tag v-else-if="roleKey(m) === 2" color="arcoblue">{{ roleText(m) }}</a-tag>
              <span v-else class="muted">—</span>
            </span>
            <span class="mtime">{{ fmt(m.joinedAt) }}</span>
          </div>
          <a-empty v-if="!members.length && !membersLoading" description="暂无群成员" :style="{ padding: '20px 0' }" />
        </template>
      </div>
      <div class="member-pager">
        <a-pagination
          :current="memberPagination.current"
          :page-size="memberPagination.pageSize"
          :total="memberPagination.total"
          :show-total="true"
          :show-page-size="true"
          :page-size-options="[5, 20, 50, 100]"
          size="small"
          @change="onMemberPageChange"
          @page-size-change="onMemberPageSizeChange"
        />
      </div>
    </a-modal>

    <!-- 群聊天记录弹窗（居中模态，复用 ConvViewer 共享组件） -->
    <a-modal
      v-model:visible="showMsgs"
      :title="'群聊天记录 · ' + (currentGroup?.nameZh || currentGroup?.id || '')"
      :footer="false"
      :mask-closable="true"
      :mask="true"
      width="720"
      :body-style="{ padding: '16px 20px 20px', height: '600px', display: 'flex', flexDirection: 'column' }"
      unmount-on-close
    >
      <div class="msg-toolbar">
        <a-input-search v-model="msgKw" placeholder="搜索内容" allow-clear style="width: 260px" @search="reloadViewer" />
        <a-button type="primary" size="small" @click="reloadViewer">刷新</a-button>
      </div>
      <div class="conv-viewer-wrap">
        <ConvViewer v-if="currentGroup?.id" :key="currentGroup.id" ref="viewerRef" :conv-id="String(currentGroup.id)" :kw="msgKw" />
      </div>
    </a-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Message, Modal } from '@arco-design/web-vue'
import { IconUserGroup, IconMessage, IconEdit, IconSwap } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'
// 会话查看器（消息渲染解析 + 数据源 /admin/messages，与消息记录「查看会话」共用同一组件）
import ConvViewer from './ConvViewer.vue'

const list = ref<Array<Record<string, any>>>([])
const loading = ref(false)
const query = reactive<{ kw: string }>({ kw: '' })
// 后端 /admin/groups 已由「裸数组」改为 {list,total} 并支持 kw + 分页，这里必须同步
const pagination = reactive({ current: 1, pageSize: 10, total: 0, showTotal: true })

const AVATAR_COLORS = ['#4E8CFF', '#7B61FF', '#FF7D00', '#00B42A', '#F53F3F', '#14C9C9']
function avatarColor(id: string | number) {
  const n = String(id).split('').reduce((s, c) => s + c.charCodeAt(0), 0)
  return { backgroundColor: AVATAR_COLORS[n % AVATAR_COLORS.length] }
}
function memberColor(id: string | number) {
  return avatarColor(id)
}

function fmt(t?: string) {
  if (!t) return '—'
  const d = new Date(t)
  if (isNaN(+d)) return String(t)
  const p = (n: number) => n.toString().padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

/** 统一取后端业务错误信息：HTTP 400 会被 axios reject，message 在 response.data.message */
function errMsg(e: any, fallback: string): string {
  return (e?.response?.data?.message as string) || (e?.message as string) || fallback
}

// 内容解析（extractSec/shortText/parseLabel 已废弃，统一走 adminMsg.ts 共享解析）

onMounted(() => load(1))

async function load(page = pagination.current) {
  loading.value = true
  try {
    const { data } = await adminApi.groups({ kw: query.kw.trim() || undefined, page, size: pagination.pageSize })
    if (data.code === 0) {
      // 破坏性变更：data.data 现在是 {list,total}，不再是数组
      const d = (data.data || {}) as { list?: Array<Record<string, any>>; total?: number }
      list.value = d.list || []
      pagination.total = d.total ?? list.value.length
      pagination.current = page
      return
    }
    Message.error(data.message || '加载群列表失败')
    list.value = []
    pagination.total = 0
  } catch (e: any) {
    Message.error(errMsg(e, '加载群列表失败（网络错误）'))
    list.value = []
    pagination.total = 0
  } finally {
    loading.value = false
  }
}

function doSearch() {
  load(1)
}

function resetQuery() {
  query.kw = ''
  load(1)
}

async function disband(record: Record<string, any>) {
  const { data } = await adminApi.groupDisband(record.id as string)
  if (data.code === 0) {
    Message.success('已解散')
    await load(pagination.current)
  } else Message.error(data.message)
}

function confirmDisbandFromEdit() {
  const g = currentGroup.value
  if (!g) return
  showEdit.value = false
  disband(g)
}

// ============= 编辑群资料 =============
const showEdit = ref(false)
const saving = ref(false)
const editForm = reactive<{ id: string; name: string; avatar: string; announcement: string; maxMembers: number }>({
  id: '', name: '', avatar: '', announcement: '', maxMembers: 0
})
const editOrigin = reactive<{ name: string; avatar: string; announcement: string; maxMembers: number }>({
  name: '', avatar: '', announcement: '', maxMembers: 0
})

function openEdit(r: Record<string, any>) {
  currentGroup.value = r
  editForm.id = String(r.id ?? '')
  editForm.name = String(r.nameZh ?? '')
  editForm.avatar = String(r.avatar ?? '')
  editForm.announcement = String(r.announcementZh ?? '')
  const mm = Number(r.maxMembers)
  editForm.maxMembers = Number.isFinite(mm) && mm > 0 ? mm : 0
  editOrigin.name = editForm.name
  editOrigin.avatar = editForm.avatar
  editOrigin.announcement = editForm.announcement
  editOrigin.maxMembers = editForm.maxMembers
  showEdit.value = true
}

/** @before-ok 回调：返回 false 阻止弹窗关闭，便于把后端 400 提示给用户 */
async function submitEdit(): Promise<boolean> {
  const name = editForm.name.trim()
  if (!name) {
    Message.warning('群名称不能为空')
    return false
  }
  const mm = Number(editForm.maxMembers)
  if (!Number.isFinite(mm) || mm < 0) {
    Message.warning('人数上限不能小于 0')
    return false
  }

  // 只传改动过的字段
  const payload: { name?: string; avatar?: string; announcement?: string; maxMembers?: number } = {}
  if (name !== editOrigin.name) payload.name = name
  if (editForm.avatar.trim() !== editOrigin.avatar) payload.avatar = editForm.avatar.trim()
  if (editForm.announcement !== editOrigin.announcement) payload.announcement = editForm.announcement
  if (mm !== editOrigin.maxMembers) payload.maxMembers = mm

  if (!Object.keys(payload).length) {
    Message.info('没有需要保存的修改')
    return true
  }

  saving.value = true
  try {
    const { data } = await adminApi.groupUpdate(editForm.id, payload)
    if (data.code === 0) {
      Message.success('已保存')
      await load(pagination.current)
      return true
    }
    // 后端会校验「人数上限不能小于当前成员数」等，返回 400 + message
    Message.error(data.message || '保存失败')
    return false
  } catch (e: any) {
    Message.error(errMsg(e, '保存失败'))
    return false
  } finally {
    saving.value = false
  }
}

// ============= 转移群主 =============
const showTransfer = ref(false)
const transferring = ref(false)
const transferForm = reactive<{ newOwnerId: string }>({ newOwnerId: '' })
const ownerOptions = ref<Array<{ value: string; label: string }>>([])
const ownerLoading = ref(false)
const OWNER_PAGE_SIZE = 50
let ownerPage = 1
let ownerTotal = 0

const currentGroup = ref<Record<string, any> | null>(null)

function memberLabel(m: Record<string, any>): string {
  const nickname = String(m.nickname ?? '').trim()
  const account = String(m.account ?? '').trim()
  const shortId = m.shortId != null && m.shortId !== '' ? String(m.shortId) : ''
  const parts: string[] = [nickname || account || '未命名']
  if (account && nickname) parts.push(account)
  if (shortId) parts.push('#' + shortId)
  return parts.join(' · ')
}

async function loadOwnerOptions(reset: boolean) {
  const gid = String(currentGroup.value?.id ?? '')
  if (!gid) return
  if (reset) {
    ownerPage = 1
    ownerTotal = 0
    ownerOptions.value = []
  }
  if (!reset && ownerTotal > 0 && ownerOptions.value.length >= ownerTotal) return

  ownerLoading.value = true
  try {
    const { data } = await adminApi.groupMembers(gid, { page: ownerPage, size: OWNER_PAGE_SIZE })
    if (data.code === 0) {
      const d = (data.data || {}) as { list?: Array<Record<string, any>>; total?: number }
      ownerTotal = d.total ?? 0
      const curOwnerId = String(currentGroup.value?.ownerId ?? '')
      const rows = (d.list || []).filter((m: Record<string, any>) => String(m.userId ?? m.id ?? '') !== curOwnerId)
      ownerOptions.value = ownerOptions.value.concat(
        rows.map((m: Record<string, any>) => ({ value: String(m.userId ?? m.id ?? ''), label: memberLabel(m) }))
      )
      ownerPage += 1
      return
    }
    Message.error(data.message || '读取群成员失败')
  } catch (e: any) {
    Message.error(errMsg(e, '读取群成员失败（网络错误）'))
  } finally {
    ownerLoading.value = false
  }
}

function loadMoreOwnerOptions() {
  loadOwnerOptions(false)
}

function openTransfer(r: Record<string, any>) {
  currentGroup.value = r
  transferForm.newOwnerId = ''
  showTransfer.value = true
  loadOwnerOptions(true)
}

/** @before-ok 回调：二次确认 + 调接口；返回 false 阻止关闭 */
async function submitTransfer(): Promise<boolean> {
  const gid = String(currentGroup.value?.id ?? '')
  const newOwnerId = String(transferForm.newOwnerId ?? '').trim()
  if (!gid || !newOwnerId) {
    Message.warning('请选择或输入新群主')
    return false
  }
  if (newOwnerId === String(currentGroup.value?.ownerId ?? '')) {
    Message.warning('该用户已经是本群群主')
    return false
  }

  const targetLabel = ownerOptions.value.find((o) => o.value === newOwnerId)?.label || newOwnerId
  const groupName = String(currentGroup.value?.nameZh ?? gid)

  const confirmed = await new Promise<boolean>((resolve) => {
    let settled = false
    const done = (v: boolean) => {
      if (settled) return
      settled = true
      resolve(v)
    }
    Modal.confirm({
      title: '确认转移群主',
      content: `确定要把「${groupName}」的群主转让给【${targetLabel}】吗？转移后原群主将变为普通成员。`,
      okText: '确认转移',
      cancelText: '取消',
      maskClosable: false,
      onOk: () => done(true),
      onCancel: () => done(false),
      onClose: () => done(false)
    })
  })
  if (!confirmed) return false

  transferring.value = true
  try {
    const { data } = await adminApi.groupTransferOwner(gid, newOwnerId)
    if (data.code === 0) {
      Message.success('群主已转移')
      await load(pagination.current)
      return true
    }
    // 后端会校验「新群主不是该群成员」/「已是群主」并返回 400 + message
    Message.error(data.message || '转移群主失败')
    return false
  } catch (e: any) {
    Message.error(errMsg(e, '转移群主失败'))
    return false
  } finally {
    transferring.value = false
  }
}

// ============= 群成员 =============
const showMembers = ref(false)
const membersLoading = ref(false)
const members = ref<Array<Record<string, any>>>([])
const memberPagination = reactive({ current: 1, pageSize: 20, total: 0, showTotal: true })

/** role: 1=群主 / 2=管理员 / 3=普通成员（兼容旧数据里的 'owner' / 'admin' 字符串） */
function roleKey(m: Record<string, any>): number {
  const r = m?.role
  if (r === 1 || r === '1' || r === 'owner' || r === 'OWNER') return 1
  if (r === 2 || r === '2' || r === 'admin' || r === 'ADMIN') return 2
  return 3
}

/** 优先用后端 roleText，旧数据没有时按 role 数字兜底 */
function roleText(m: Record<string, any>): string {
  const t = m?.roleText
  if (t != null && String(t).trim() !== '') return String(t)
  const k = roleKey(m)
  return k === 1 ? '群主' : k === 2 ? '管理员' : '成员'
}

async function loadMembers(page = memberPagination.current) {
  const gid = String(currentGroup.value?.id ?? '')
  if (!gid) return
  membersLoading.value = true
  try {
    const { data } = await adminApi.groupMembers(gid, { page, size: memberPagination.pageSize })
    if (data.code === 0) {
      const d = (data.data || {}) as { list?: Array<Record<string, any>>; total?: number }
      members.value = d.list || []
      memberPagination.total = d.total ?? members.value.length
      memberPagination.current = page
      return
    }
    Message.error(data.message || '读取群成员失败')
    members.value = []
    memberPagination.total = 0
  } catch (e: any) {
    Message.error(errMsg(e, '读取群成员失败（网络错误）'))
    members.value = []
    memberPagination.total = 0
  } finally {
    membersLoading.value = false
  }
}

function onMemberPageChange(page: number) {
  loadMembers(page)
}

function onMemberPageSizeChange(size: number) {
  memberPagination.pageSize = size
  loadMembers(1)
}

async function openMembers(r: Record<string, any>) {
  currentGroup.value = r
  showMembers.value = true
  members.value = []
  memberPagination.total = 0
  await loadMembers(1)
}

// ============= 群聊天记录（复用 ConvViewer，数据源 /admin/messages，含昵称/ID/头像） =============
const showMsgs = ref(false)
const msgKw = ref('')
const viewerRef = ref<InstanceType<typeof ConvViewer> | null>(null)

async function openMessages(r: Record<string, any>) {
  currentGroup.value = r
  msgKw.value = ''
  showMsgs.value = true
}

function reloadViewer() {
  viewerRef.value?.reload()
}
</script>

<style scoped>
.toolbar { display: flex; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; align-items: center; }

.group-cell { display: flex; align-items: center; gap: 10px; }
.group-avatar {
  width: 36px; height: 36px;
  border-radius: var(--app-radius-md);
  display: flex; align-items: center; justify-content: center;
  color: #fff; font-size: 14px; font-weight: 600;
  overflow: hidden; flex-shrink: 0;
}
.group-avatar img { width: 100%; height: 100%; object-fit: cover; }
.group-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.name { font-size: var(--app-font-size-base); color: var(--app-text-1); font-weight: var(--app-font-weight-medium); }
.sub { font-size: var(--app-font-size-xs); color: var(--app-text-3); }
.muted { color: var(--app-text-3); }

/* 编辑群资料 */
.avatar-row { display: flex; align-items: center; gap: 12px; width: 100%; }
.avatar-preview {
  width: 64px; height: 64px;
  border-radius: var(--app-radius-md);
  background: #4E8CFF;
  color: #fff; font-size: 24px; font-weight: 600;
  display: flex; align-items: center; justify-content: center;
  overflow: hidden; flex-shrink: 0;
}
.avatar-preview img { width: 100%; height: 100%; object-fit: cover; }
.hint { font-size: var(--app-font-size-xs); color: var(--app-text-3); margin-left: 10px; }
.edit-footer { display: flex; align-items: center; justify-content: space-between; }

/* 成员弹窗 */
.member-list { display: flex; flex-direction: column; }
.member-row {
  display: grid; grid-template-columns: 1fr 110px 170px; align-items: center;
  padding: 10px 6px;
  border-bottom: 1px solid var(--app-border-2);
}
.member-row.header {
  font-size: var(--app-font-size-xs);
  color: var(--app-text-3);
  padding: 8px 6px;
  background: var(--app-border-2);
  border-radius: var(--app-radius-sm) var(--app-radius-sm) 0 0;
  border-bottom: none;
}
.member-user { display: flex; align-items: center; gap: 10px; }
.member-user .avatar {
  width: 32px; height: 32px; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  color: #fff; font-weight: 600; font-size: 13px; overflow: hidden;
}
.member-user .avatar img { width: 100%; height: 100%; object-fit: cover; }
.mi { display: flex; flex-direction: column; gap: 1px; }
.mnick { font-size: var(--app-font-size-base); color: var(--app-text-1); font-weight: var(--app-font-weight-medium); }
.muid { font-size: var(--app-font-size-xs); color: var(--app-text-3); font-family: ui-monospace, Menlo, monospace; }
.mtime { font-size: var(--app-font-size-xs); color: var(--app-text-3); }
.member-pager { display: flex; justify-content: flex-end; padding-top: 14px; }

/* 消息弹窗 */
.msg-toolbar {
  display: flex; gap: 10px; margin-bottom: 12px; align-items: center;
  flex-shrink: 0;
}
.conv-viewer-wrap { flex: 1; min-height: 0; }
.conv-viewer-wrap :deep(.conv-window) { height: 100%; }
</style>
