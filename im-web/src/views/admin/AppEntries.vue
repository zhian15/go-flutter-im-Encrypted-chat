<template>
  <div class="page">
    <a-card>
      <div class="toolbar">
        <a-button type="primary" @click="openEdit()">上架小程序</a-button>
        <a-tooltip content="小程序即网页 URL，客户端「发现」页列表展示，点击打开">
          <icon-question-circle style="color: var(--color-text-3)" />
        </a-tooltip>
      </div>

      <a-table :data="list" row-key="id" :loading="loading" :pagination="false">
        <template #columns>
          <a-table-column title="小程序" :width="220">
            <template #cell="{ record }">
              <div class="app-cell">
                <span class="app-icon">
                  <img v-if="record.icon" :src="record.icon" alt="" />
                  <IconApps v-else />
                </span>
                <div class="app-info">
                  <span class="name">{{ record.nameZh }}</span>
                  <span v-if="record.nameEn" class="sub">/ {{ record.nameEn }}</span>
                </div>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="地址" data-index="url">
            <template #cell="{ record }">
              <a-link :href="record.url" target="_blank">{{ record.url }}</a-link>
            </template>
          </a-table-column>
          <a-table-column title="分类" data-index="category" :width="100" />
          <a-table-column title="排序" data-index="sort" :width="80" />
          <a-table-column title="状态" :width="90">
            <template #cell="{ record }">
              <a-switch :model-value="record.enabled === 1" size="small" @change="(v: any) => toggle(record, !!v)" />
            </template>
          </a-table-column>
          <a-table-column title="操作" :width="150">
            <template #cell="{ record }">
              <a-button size="mini" @click="openEdit(record)">编辑</a-button>
              <a-popconfirm content="确认下架删除该小程序？" @ok="del(record)">
                <a-button size="mini" status="danger" style="margin-left: 8px">删除</a-button>
              </a-popconfirm>
            </template>
          </a-table-column>
        </template>
      </a-table>
    </a-card>

    <a-modal v-model:visible="showEdit" :title="editId ? '编辑小程序' : '上架小程序'" @ok="save">
      <a-form :model="form" layout="vertical">
        <a-form-item label="图标">
          <ImageUpload v-model="form.icon" dir="app/" hint="建议尺寸 64×64，PNG/SVG" />
        </a-form-item>
        <a-form-item label="名称（中文）" required><a-input v-model="form.nameZh" /></a-form-item>
        <a-form-item label="名称（英文）"><a-input v-model="form.nameEn" /></a-form-item>
        <a-form-item label="网页地址" required>
          <a-input v-model="form.url" placeholder="https://example.com/page" />
        </a-form-item>
        <a-form-item label="分类"><a-input v-model="form.category" placeholder="如：办公 / 工具" /></a-form-item>
        <a-form-item label="排序"><a-input-number v-model="form.sort" :min="0" /></a-form-item>
        <a-form-item label="上架">
          <a-switch v-model="form.enabled" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Message } from '@arco-design/web-vue'
import { IconApps } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'
import ImageUpload from './ImageUpload.vue'

const list = ref<Array<Record<string, any>>>([])
const loading = ref(false)
const showEdit = ref(false)
const editId = ref(0)
const form = reactive({ nameZh: '', nameEn: '', url: '', icon: '', category: '', sort: 0, enabled: true })

onMounted(load)

async function load() {
  loading.value = true
  try {
    const { data } = await adminApi.apps()
    if (data.code === 0) list.value = data.data as never
  } finally {
    loading.value = false
  }
}

function openEdit(record?: Record<string, any>) {
  editId.value = record?.id || 0
  Object.assign(form, {
    nameZh: record?.nameZh || '', nameEn: record?.nameEn || '', url: record?.url || '',
    icon: record?.icon || '', category: record?.category || '', sort: record?.sort || 0,
    enabled: record ? record.enabled === 1 : true
  })
  showEdit.value = true
}

async function save() {
  if (!form.nameZh || !form.url) return Message.error('请填写名称和地址')
  const { data } = editId.value
    ? await adminApi.appUpdate(editId.value, { ...form })
    : await adminApi.appCreate({ ...form })
  if (data.code === 0) {
    Message.success('已保存')
    showEdit.value = false
    await load()
  } else Message.error(data.message)
}

async function toggle(record: Record<string, any>, on: boolean) {
  const { data } = await adminApi.appUpdate(record.id, { enabled: on })
  if (data.code === 0) {
    record.enabled = on ? 1 : 0
    Message.success(on ? '已上架' : '已下架')
  }
}

async function del(record: Record<string, any>) {
  const { data } = await adminApi.appDelete(record.id)
  if (data.code === 0) {
    Message.success('已删除')
    await load()
  } else Message.error(data.message)
}
</script>

<style scoped>
.toolbar { display: flex; align-items: center; gap: 8px; margin-bottom: 16px; }
.sub { color: var(--color-text-3); font-size: 12px; }

.app-cell { display: flex; align-items: center; gap: 10px; }
.app-icon {
  width: 36px; height: 36px;
  border-radius: var(--app-radius-md);
  background: var(--app-primary-bg);
  color: var(--app-primary);
  display: flex; align-items: center; justify-content: center;
  overflow: hidden; flex-shrink: 0;
}
.app-icon :deep(svg) { width: 20px; height: 20px; }
.app-icon img { width: 100%; height: 100%; object-fit: cover; }
.app-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.name { font-size: var(--app-font-size-base); color: var(--app-text-1); font-weight: var(--app-font-weight-medium); }
</style>
