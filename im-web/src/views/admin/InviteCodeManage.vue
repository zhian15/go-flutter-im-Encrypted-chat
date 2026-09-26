<template>
  <div class="invite-page">
    <div class="page-head">
      <div>
        <h2 class="page-title">邀请码管理</h2>
        <p class="page-desc">
          自定义邀请码可关联多个好友；通过邀请码注册的新用户会<b>自动添加</b>这些好友（双向好友）。
          「注册认证」里的「邀请码注册」开关开启时，这里启用的邀请码同样可用于注册。
        </p>
      </div>
      <a-space>
        <a-button @click="load" :loading="loading">刷新</a-button>
        <a-button type="primary" @click="openCreate">
          <template #icon><IconPlus /></template>
          添加邀请码
        </a-button>
      </a-space>
    </div>

    <a-table
      :data="list"
      :loading="loading"
      :pagination="false"
      row-key="id"
      :bordered="{ cell: true }"
      class="invite-table"
    >
      <template #columns>
        <a-table-column title="邀请码" :width="180">
          <template #cell="{ record }">
            <span class="code-chip">{{ record.code }}</span>
          </template>
        </a-table-column>
        <a-table-column title="关联好友" min-width="240">
          <template #cell="{ record }">
            <a-space wrap size="mini">
              <template v-if="record.friendNames && record.friendNames.length">
                <a-tag v-for="(n, i) in record.friendNames" :key="i" color="arcoblue" size="small">{{ n }}</a-tag>
              </template>
              <template v-else-if="parseFriendIds(record.friendIds).length">
                <a-tag v-for="v in parseFriendIds(record.friendIds)" :key="v" color="arcoblue" size="small">用户 {{ v }}</a-tag>
              </template>
              <a-tag v-else color="gray" size="small">未关联</a-tag>
            </a-space>
          </template>
        </a-table-column>
        <a-table-column title="备注" :width="180">
          <template #cell="{ record }">
            <span class="remark">{{ record.remark || '-' }}</span>
          </template>
        </a-table-column>
        <a-table-column title="使用次数" :width="100" align="center">
          <template #cell="{ record }">
            <span class="used">{{ record.usedCount || 0 }}</span>
          </template>
        </a-table-column>
        <a-table-column title="状态" :width="100" align="center">
          <template #cell="{ record }">
            <a-switch
              :model-value="record.enabled === 1"
              @change="(v: any) => toggleEnabled(record, v)"
            />
          </template>
        </a-table-column>
        <a-table-column title="创建时间" :width="170">
          <template #cell="{ record }">
            <span class="time">{{ fmtTime(record.createdAt) }}</span>
          </template>
        </a-table-column>
        <a-table-column title="操作" :width="150" align="center">
          <template #cell="{ record }">
            <a-space size="mini">
              <a-button size="mini" type="text" @click="openEdit(record)">编辑</a-button>
              <a-popconfirm content="确定删除该邀请码？删除后注册填写将无效" type="warning" @ok="removeRow(record)">
                <a-button size="mini" type="text" status="danger">删除</a-button>
              </a-popconfirm>
            </a-space>
          </template>
        </a-table-column>
      </template>
      <template #empty>
        <a-empty description="暂无邀请码，点击右上角「添加邀请码」创建" />
      </template>
    </a-table>

    <!-- 创建 / 编辑弹窗 -->
    <a-modal
      v-model:visible="dlgVisible"
      :title="editing ? '编辑邀请码' : '添加邀请码'"
      :width="560"
      :ok-loading="saving"
      @ok="save"
      @close="resetForm"
    >
      <a-form layout="vertical">
        <a-form-item label="邀请码" required>
          <a-input
            v-model="form.code"
            placeholder="自定义邀请码，如 VIP888 / FX2026"
            allow-clear
            :max-length="32"
          />
          <div class="hint">仅限字母、数字、下划线和中划线，最长 32 位，保存后注册时填写此码即生效</div>
        </a-form-item>
        <a-form-item label="关联好友（可多选，注册自动添加）" required>
          <a-select
            v-model="form.friendIds"
            :options="userOptions"
            :loading="searching"
            allow-search
            search-placeholder="输入昵称 / 账号 / 靓号 ID 搜索用户"
            placeholder="搜索并选择要自动添加的好友"
            multiple
            :limit="20"
            :filter-option="false"
            @search="onSearchUser"
          />
          <div class="hint">输入关键字远程搜索用户；选中后注册用户将自动与这些用户建立双向好友关系</div>
        </a-form-item>
        <a-form-item label="备注（可选）">
          <a-input v-model="form.remark" placeholder="如：投放给 XX 渠道" :max-length="100" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Message } from '@arco-design/web-vue'
import { IconPlus } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'

const loading = ref(false)
const list = ref<Array<Record<string, any>>>([])

const dlgVisible = ref(false)
const saving = ref(false)
const editing = ref<Record<string, any> | null>(null)
const form = ref({ code: '', remark: '', friendIds: [] as string[] })

const userOptions = ref<Array<{ label: string; value: string }>>([])
const searching = ref(false)
let searchTimer: any = null

// friendIds 后端是 JSON 字符串（gorm type:json），也可能是数组/逗号分隔——统一容错解析
function parseFriendIds(raw: any): string[] {
  if (Array.isArray(raw)) return raw.map((v: any) => String(v)).filter(Boolean)
  if (typeof raw === 'number') return [String(raw)]
  if (typeof raw === 'string' && raw.trim()) {
    const s = raw.trim()
    try {
      const parsed = JSON.parse(s)
      if (Array.isArray(parsed)) return parsed.map((v: any) => String(v)).filter(Boolean)
    } catch { /* 非法 JSON 按逗号分隔兜底 */ }
    return s.split(/[,，]/).map((v) => v.trim()).filter(Boolean)
  }
  return []
}

async function load() {
  loading.value = true
  try {
    const { data } = await adminApi.inviteCodes()
    if (data?.code !== 0) { Message.error(data?.message || '读取邀请码失败'); return }
    list.value = data.data || []
  } catch (e: any) {
    Message.error('读取邀请码失败：' + (e?.message || ''))
  } finally {
    loading.value = false
  }
}

function onSearchUser(kw: string) {
  if (searchTimer) clearTimeout(searchTimer)
  const k = (kw || '').trim()
  if (!k) { userOptions.value = []; return }
  searchTimer = setTimeout(async () => {
    searching.value = true
    try {
      const { data } = await adminApi.users({ kw: k, page: 1, size: 20 })
      if (data?.code !== 0) return
      const rows = data.data?.list || []
      // 保留已选中但不在结果里的项，避免回显丢失
      const selected = form.value.friendIds
      const opts = rows.map((u: any) => ({
        label: `${u.nickname || u.account}${u.shortId ? '（ID ' + u.shortId + '）' : ''}`,
        value: String(u.id)
      }))
      const missing = selected.filter((v) => !opts.some((o: any) => o.value === v))
      const extra: Array<{ label: string; value: string }> = missing.map((v) => ({ label: `用户 ${v}`, value: v }))
      userOptions.value = [...extra, ...opts]
    } finally {
      searching.value = false
    }
  }, 300)
}

function openCreate() {
  editing.value = null
  form.value = { code: '', remark: '', friendIds: [] }
  userOptions.value = []
  dlgVisible.value = true
}

function openEdit(row: Record<string, any>) {
  editing.value = row
  const ids = parseFriendIds(row.friendIds)
  const names: string[] = Array.isArray(row.friendNames) ? row.friendNames : []
  form.value = {
    code: row.code || '',
    remark: row.remark || '',
    friendIds: ids
  }
  // 回显已选好友名称（friendNames 与 friendIds 顺序一一对应）
  userOptions.value = ids.map((v, i) => ({
    label: names[i] ? `${names[i]}（ID ${v}）` : `用户 ${v}`,
    value: v
  }))
  dlgVisible.value = true
}

async function save() {
  const code = form.value.code.trim()
  if (!code) { Message.warning('请填写邀请码'); return }
  if (!form.value.friendIds.length) { Message.warning('请至少关联一位好友'); return }
  saving.value = true
  try {
    if (editing.value) {
      const { data } = await adminApi.inviteCodeUpdate(editing.value.id, {
        code,
        friendIds: form.value.friendIds,
        remark: form.value.remark
      })
      if (data?.code !== 0) { Message.error(data?.message || '保存失败'); return }
      Message.success('邀请码已更新')
    } else {
      const { data } = await adminApi.inviteCodeCreate({
        code,
        friendIds: form.value.friendIds,
        remark: form.value.remark
      })
      if (data?.code !== 0) { Message.error(data?.message || '创建失败'); return }
      Message.success('邀请码已创建')
    }
    dlgVisible.value = false
    await load()
  } catch (e: any) {
    Message.error('保存失败：' + (e?.message || ''))
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(row: Record<string, any>, v: any) {
  const { data } = await adminApi.inviteCodeUpdate(row.id, { enabled: v ? 1 : 0 })
  if (data?.code !== 0) { Message.error(data?.message || '操作失败'); return }
  row.enabled = v ? 1 : 0
  Message.success(v ? '已启用' : '已停用')
}

async function removeRow(row: Record<string, any>) {
  const { data } = await adminApi.inviteCodeDelete(row.id)
  if (data?.code !== 0) { Message.error(data?.message || '删除失败'); return }
  Message.success('已删除')
  await load()
}

function resetForm() {
  editing.value = null
  form.value = { code: '', remark: '', friendIds: [] }
  userOptions.value = []
}

function fmtTime(t: any) {
  if (!t) return '-'
  const d = new Date(t)
  if (isNaN(d.getTime())) return String(t)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

onMounted(load)
</script>

<style scoped>
.invite-page { display: flex; flex-direction: column; gap: 16px; height: 100%; }
.page-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.page-title { margin: 0 0 4px; font-size: var(--app-font-size-xl); font-weight: var(--app-font-weight-semibold); color: var(--app-text-1); }
.page-desc { margin: 0; font-size: var(--app-font-size-sm); color: var(--app-text-3); max-width: 720px; }
.invite-table { background: var(--app-bg-card); border-radius: var(--app-radius-lg); }
.code-chip {
  display: inline-block; padding: 2px 10px; border-radius: 8px;
  background: var(--app-primary-bg); color: var(--app-primary);
  font-family: monospace; font-size: 13px; font-weight: 600; letter-spacing: 0.5px;
}
.remark { color: var(--app-text-2); font-size: 13px; }
.used { font-weight: 600; color: var(--app-text-1); }
.time { color: var(--app-text-3); font-size: 12.5px; }
.hint { color: var(--app-text-3); font-size: 12px; margin-top: 4px; }
</style>
