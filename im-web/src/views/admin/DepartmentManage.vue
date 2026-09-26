<template>
  <div class="page">
    <a-card>
      <div class="toolbar">
        <a-button type="primary" @click="openEdit()">新增部门</a-button>
      </div>
      <a-table :data="list" row-key="id" :loading="loading" :pagination="false">
        <template #columns>
          <a-table-column title="ID" data-index="id" :width="80" />
          <a-table-column title="部门名（中文）" data-index="nameZh" :width="200" />
          <a-table-column title="部门名（英文）" data-index="nameEn" :width="200" />
          <a-table-column title="排序" data-index="sort" :width="80" />
          <a-table-column title="操作" :width="200">
            <template #cell="{ record }">
              <a-button size="mini" @click="openEdit(record)">编辑</a-button>
              <a-popconfirm content="确认删除该部门？" @ok="del(record)">
                <a-button size="mini" status="danger" style="margin-left: 8px">删除</a-button>
              </a-popconfirm>
            </template>
          </a-table-column>
        </template>
      </a-table>
    </a-card>

    <a-modal v-model:visible="showEdit" :title="editId ? '编辑部门' : '新增部门'" @ok="save">
      <a-form :model="form" layout="vertical">
        <a-form-item label="中文名" required><a-input v-model="form.nameZh" /></a-form-item>
        <a-form-item label="英文名"><a-input v-model="form.nameEn" /></a-form-item>
        <a-form-item label="排序"><a-input-number v-model="form.sort" :min="0" /></a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Message } from '@arco-design/web-vue'
import { adminApi } from '@/api/admin'

const list = ref<Array<Record<string, any>>>([])
const loading = ref(false)
const showEdit = ref(false)
const editId = ref(0)
const form = reactive({ nameZh: '', nameEn: '', sort: 0 })

onMounted(load)

async function load() {
  loading.value = true
  try {
    const { data } = await adminApi.departments()
    if (data.code === 0) list.value = data.data as never
  } finally {
    loading.value = false
  }
}

function openEdit(record?: Record<string, any>) {
  editId.value = record?.id || 0
  Object.assign(form, { nameZh: record?.nameZh || '', nameEn: record?.nameEn || '', sort: record?.sort || 0 })
  showEdit.value = true
}

async function save() {
  if (!form.nameZh) return Message.error('请填写中文名')
  const { data } = editId.value
    ? await adminApi.deptUpdate(editId.value, form)
    : await adminApi.deptCreate(form)
  if (data.code === 0) {
    Message.success('保存成功')
    showEdit.value = false
    await load()
  } else Message.error(data.message)
}

async function del(record: Record<string, any>) {
  const { data } = await adminApi.deptDelete(record.id)
  if (data.code === 0) {
    Message.success('已删除')
    await load()
  } else Message.error(data.message)
}
</script>

<style scoped>
.toolbar { margin-bottom: 16px; }
</style>
