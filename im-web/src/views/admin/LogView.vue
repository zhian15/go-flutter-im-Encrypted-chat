<template>
  <div class="page">
    <a-card>
      <a-tabs v-model:active-key="tab">
        <a-tab-pane key="admin" title="操作日志">
          <a-table :data="adminLogs" row-key="id" :pagination="adminPagination" :loading="loading" @page-change="loadAdmin">
            <template #columns>
              <a-table-column title="管理员" data-index="adminId" :width="160" />
              <a-table-column title="动作" data-index="action" :width="140" />
              <a-table-column title="对象" data-index="target" :width="200" />
              <a-table-column title="IP" data-index="ip" :width="140" />
              <a-table-column title="时间" :width="180">
                <template #cell="{ record }">{{ fmt(record.createdAt) }}</template>
              </a-table-column>
            </template>
          </a-table>
        </a-tab-pane>
        <a-tab-pane key="login" title="登录日志">
          <a-table :data="loginLogs" row-key="id" :pagination="loginPagination" :loading="loading" @page-change="loadLogin">
            <template #columns>
              <a-table-column title="用户" data-index="userId" :width="160" />
              <a-table-column title="IP" data-index="ip" :width="140" />
              <a-table-column title="设备" data-index="device" :width="120" />
              <a-table-column title="结果" :width="90">
                <template #cell="{ record }">
                  <a-tag :color="record.result === 1 ? 'green' : 'red'">{{ record.result === 1 ? '成功' : '失败' }}</a-tag>
                </template>
              </a-table-column>
              <a-table-column title="时间" :width="180">
                <template #cell="{ record }">{{ fmt(record.createdAt) }}</template>
              </a-table-column>
            </template>
          </a-table>
        </a-tab-pane>
      </a-tabs>
    </a-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { adminApi } from '@/api/admin'

const tab = ref('admin')
const loading = ref(false)
const adminLogs = ref<Array<Record<string, any>>>([])
const loginLogs = ref<Array<Record<string, any>>>([])
const adminPagination = reactive({ current: 1, pageSize: 20, total: 0, showTotal: true })
const loginPagination = reactive({ current: 1, pageSize: 20, total: 0, showTotal: true })

onMounted(async () => {
  await Promise.all([loadAdmin(1), loadLogin(1)])
})

async function loadAdmin(page = adminPagination.current) {
  loading.value = true
  try {
    const { data } = await adminApi.logs({ page, size: adminPagination.pageSize })
    if (data.code === 0) {
      adminLogs.value = data.data.list
      adminPagination.total = data.data.total
      adminPagination.current = page
    }
  } finally {
    loading.value = false
  }
}

async function loadLogin(page = loginPagination.current) {
  loading.value = true
  try {
    const { data } = await adminApi.loginLogs({ page, size: loginPagination.pageSize })
    if (data.code === 0) {
      loginLogs.value = data.data.list
      loginPagination.total = data.data.total
      loginPagination.current = page
    }
  } finally {
    loading.value = false
  }
}

function fmt(t?: string) {
  return t ? new Date(t).toLocaleString() : '-'
}
</script>
