<template>
  <div class="page">
    <a-card>
      <div class="toolbar">
        <a-input-search
          v-model="keyword"
          placeholder="按域名搜索"
          allow-clear
          style="width: 240px"
          @search="load"
          @clear="load"
        />
        <a-button type="primary" @click="openEdit()">新增白名单</a-button>
      </div>

      <a-table :data="list" row-key="id" :loading="loading" :pagination="false">
        <template #columns>
          <a-table-column title="图标" :width="72">
            <template #cell="{ record }">
              <img v-if="record.logo" :src="record.logo" alt="logo" class="cell-logo" />
              <span v-else class="cell-empty">—</span>
            </template>
          </a-table-column>
          <a-table-column title="域名" :width="240">
            <template #cell="{ record }">
              <span class="mono">{{ record.domain }}</span>
            </template>
          </a-table-column>
          <a-table-column title="展示名称" data-index="displayName" />
          <a-table-column title="启用" :width="90">
            <template #cell="{ record }">
              <a-switch :model-value="truthy(record.enabled)" size="small" @change="(v: any) => toggleEnabled(record, !!v)" />
            </template>
          </a-table-column>
          <a-table-column title="原生桥" :width="90">
            <template #cell="{ record }">
              <a-switch :model-value="truthy(record.nativeBridge)" size="small" @change="(v: any) => toggleBridge(record, !!v)" />
            </template>
          </a-table-column>
          <a-table-column title="操作" :width="150">
            <template #cell="{ record }">
              <a-button size="mini" @click="openEdit(record)">编辑</a-button>
              <a-popconfirm content="确认删除该白名单？" @ok="del(record)">
                <a-button size="mini" status="danger" style="margin-left: 8px">删除</a-button>
              </a-popconfirm>
            </template>
          </a-table-column>
        </template>
      </a-table>
    </a-card>

    <a-modal
      v-model:visible="showEdit"
      :title="editId ? '编辑白名单' : '新增白名单'"
      :width="560"
      :ok-loading="saving"
      :on-before-ok="save"
      @cancel="closeEdit"
    >
      <a-form :model="form" layout="vertical">
        <a-form-item label="域名" required>
          <a-input v-model="form.domain" placeholder="example.com" />
        </a-form-item>
        <a-form-item label="展示名称" required>
          <a-input v-model="form.displayName" placeholder="在聊天卡片页脚展示的名称" />
        </a-form-item>
        <a-form-item label="小程序 logo">
          <ImageUpload
            v-model="form.logo"
            dir="whitelist/"
            :inline="true"
            :size="80"
            hint="（可选）不上传则自动用站点 favicon。建议 128×128 正方形图片。"
          />
        </a-form-item>
        <a-form-item label="封面图">
          <ImageUpload
            v-model="form.cover"
            dir="whitelist/"
            :inline="true"
            :size="120"
            hint="（可选）不上传则自动用网页 og:image 封面。建议 16:9 横图。"
          />
        </a-form-item>
        <a-form-item label="启用">
          <a-switch v-model="form.enabled" />
        </a-form-item>
        <a-form-item label="原生桥（注入 IM_BRIDGE）">
          <a-switch v-model="form.nativeBridge" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Message } from '@arco-design/web-vue'
import { adminApi } from '@/api/admin'
import ImageUpload from './ImageUpload.vue'

interface WhitelistItem {
  id?: number
  domain: string
  displayName: string
  enabled?: number | boolean
  nativeBridge?: number | boolean
  logo?: string
  cover?: string
}

/**
 * 弹窗表单的形状：与列表项刻意区分开。
 * enabled / nativeBridge 用 boolean 便于 a-switch 双向绑定（提交前归一化成 int 1/0）；
 * logo / cover 是必填 string（不是 string | undefined），以匹配 ImageUpload 的 modelValue 契约。
 */
interface WhitelistForm {
  domain: string
  displayName: string
  enabled: boolean
  nativeBridge: boolean
  logo: string
  cover: string
}

const list = ref<WhitelistItem[]>([])
const loading = ref(false)
const keyword = ref('')
const showEdit = ref(false)
const saving = ref(false)
const editId = ref<number>(0)
const form = reactive<WhitelistForm>({
  domain: '',
  displayName: '',
  enabled: true,
  nativeBridge: false,
  logo: '',
  cover: ''
})

onMounted(load)

/** 兼容后端可能返回的 1 / true / '1' 三种真值写法 */
function truthy(v: unknown): boolean {
  return v === 1 || v === true || v === '1'
}

/**
 * 把后端返回的单条记录归一化：
 * 统一 camelCase（对 snake_case 做兜底），布尔位统一成 int 1/0，
 * 这样模板与后续提交逻辑只需面对一种形状。
 */
function normalize(raw: Record<string, any>): WhitelistItem {
  return {
    id: raw.id,
    domain: raw.domain || '',
    displayName: raw.displayName ?? raw.display_name ?? '',
    enabled: truthy(raw.enabled) ? 1 : 0,
    nativeBridge: truthy(raw.nativeBridge ?? raw.native_bridge) ? 1 : 0,
    logo: raw.logo ?? '',
    cover: raw.cover ?? ''
  }
}

/**
 * 加载白名单列表。
 *
 * 关键：后端返回的是 **{list,total}**（handler/admin_whitelist.go:34），不是裸数组。
 * 早期写成 ((data.data as Array) || []).map(normalize) → 运行时抛
 * "xxx.map is not a function"，表现为两个连体症状：
 *   ① 列表永远空白（即使数据已入库）；
 *   ② 保存成功后紧跟着一条「保存失败」——因为 save() 里 await load() 被 catch 到。
 * 这里对「数组」和「{list}」两种形状都兜住，并把失败显式报出来。
 */
async function load() {
  loading.value = true
  try {
    const { data } = await adminApi.webWhitelistList({ keyword: keyword.value || undefined })
    if (data.code !== 0) {
      Message.error(data.message || '加载白名单失败')
      return
    }
    const payload = data.data as unknown
    const rows: Array<Record<string, any>> = Array.isArray(payload)
      ? payload
      : ((payload as { list?: Array<Record<string, any>> } | null | undefined)?.list ?? [])
    list.value = rows.map(normalize)
  } catch (e: any) {
    Message.error('加载白名单失败：' + (e?.response?.data?.message || e?.message || '请稍后重试'))
  } finally {
    loading.value = false
  }
}

function openEdit(record?: WhitelistItem) {
  editId.value = record?.id || 0
  form.domain = record?.domain || ''
  form.displayName = record?.displayName || ''
  form.enabled = record ? truthy(record.enabled) : true
  form.nativeBridge = record ? truthy(record.nativeBridge) : false
  form.logo = record?.logo || ''
  form.cover = record?.cover || ''
  showEdit.value = true
}
function closeEdit() {
  showEdit.value = false
}

/**
 * 保存（新增 / 编辑）。作为 a-modal 的 on-before-ok：
 * 返回 false 时弹窗保持打开，避免校验失败或接口报错时用户已填的内容被丢掉。
 *
 * enabled / nativeBridge 必须按 int 1/0 提交 —— 后端结构体是 int，
 * 传 boolean 会导致 JSON 绑定失败直接 400「参数错误」。
 * logo / cover 传空字符串表示清空。
 */
async function save(): Promise<boolean> {
  if (!form.domain.trim()) {
    Message.error('请填写域名')
    return false
  }
  if (!form.displayName.trim()) {
    Message.error('请填写展示名称')
    return false
  }
  const payload = {
    domain: form.domain.trim(),
    displayName: form.displayName.trim(),
    enabled: form.enabled ? 1 : 0,
    nativeBridge: form.nativeBridge ? 1 : 0,
    logo: form.logo.trim(),
    cover: form.cover.trim()
  }
  saving.value = true
  try {
    const { data } = editId.value
      ? await adminApi.webWhitelistUpdate(editId.value, payload)
      : await adminApi.webWhitelistCreate(payload)
    if (data.code === 0) {
      Message.success('已保存')
      // load() 内部已自行 catch 并提示，这里不能让它再抛出去——
      // 否则刷新失败会把刚弹出的「已保存」翻成「保存失败」，误导用户重复提交（进而撞上「域名已存在」）。
      await load()
      return true
    }
    Message.error(data.message || '保存失败')
    return false
  } catch (e: any) {
    Message.error('保存失败：' + (e?.response?.data?.message || e?.message || '请稍后重试'))
    return false
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(record: WhitelistItem, on: boolean) {
  try {
    const { data } = await adminApi.webWhitelistUpdate(record.id as number, { enabled: on ? 1 : 0 })
    if (data.code === 0) {
      record.enabled = on ? 1 : 0
      Message.success(on ? '已启用' : '已停用')
    } else Message.error(data.message || '操作失败')
  } catch (e: any) {
    Message.error('操作失败：' + (e?.response?.data?.message || e?.message || '请稍后重试'))
  }
}

async function toggleBridge(record: WhitelistItem, on: boolean) {
  try {
    const { data } = await adminApi.webWhitelistUpdate(record.id as number, { nativeBridge: on ? 1 : 0 })
    if (data.code === 0) {
      record.nativeBridge = on ? 1 : 0
      Message.success('已更新')
    } else Message.error(data.message || '操作失败')
  } catch (e: any) {
    Message.error('操作失败：' + (e?.response?.data?.message || e?.message || '请稍后重试'))
  }
}

async function del(record: WhitelistItem) {
  try {
    const { data } = await adminApi.webWhitelistDelete(record.id as number)
    if (data.code === 0) {
      Message.success('已删除')
      await load()
    } else Message.error(data.message || '删除失败')
  } catch (e: any) {
    Message.error('删除失败：' + (e?.response?.data?.message || e?.message || '请稍后重试'))
  }
}
</script>

<style scoped>
.toolbar { display: flex; align-items: center; gap: 8px; margin-bottom: 16px; }
.mono { font-family: var(--app-font-mono, ui-monospace, monospace); }
.cell-logo {
  width: 28px;
  height: 28px;
  border-radius: var(--app-radius-sm, 4px);
  object-fit: cover;
  display: block;
  background: var(--app-border-2);
}
.cell-empty { color: var(--app-text-3); }
</style>
