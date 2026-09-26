<template>
  <div class="page">
    <a-card title="接入节点管理（多节点：客户端自动选择可用的 API/WS 网关）">
      <template #extra>
        <a-button type="primary" @click="addNode">新增节点</a-button>
      </template>
      <a-table :data="nodes" :pagination="false" row-key="id">
        <template #columns>
          <a-table-column title="ID" data-index="id" :width="100" />
          <a-table-column title="名称" data-index="name" :width="140">
            <template #cell="{ record }">
              <a-input v-model="record.name" size="small" />
            </template>
          </a-table-column>
          <a-table-column title="WS 地址" data-index="wss">
            <template #cell="{ record }">
              <a-input v-model="record.wss" size="small" placeholder="wss://im.example.com/ws" />
            </template>
          </a-table-column>
          <a-table-column title="API 地址" data-index="api">
            <template #cell="{ record }">
              <a-input v-model="record.api" size="small" placeholder="https://im.example.com" />
            </template>
          </a-table-column>
          <a-table-column title="权重" data-index="weight" :width="100">
            <template #cell="{ record }">
              <a-input-number v-model="record.weight" :min="1" :max="999" size="small" />
            </template>
          </a-table-column>
          <a-table-column title="操作" :width="120">
            <template #cell="{ record }">
              <a-popconfirm content="确认删除该节点？" @ok="removeNode(record)">
                <a-button type="text" status="danger" size="small">删除</a-button>
              </a-popconfirm>
            </template>
          </a-table-column>
        </template>
      </a-table>
      <div style="margin-top: 16px; display: flex; gap: 12px; align-items: center">
        <a-button type="primary" @click="save">保存节点列表</a-button>
        <span style="color: #86909c; font-size: 12px">保存后立即生效（GET /access/nodes 返回最新列表）</span>
      </div>
    </a-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Message } from '@arco-design/web-vue'
import { adminApi } from '@/api/admin'

const nodes = ref<Array<{ id: string; name: string; wss: string; api: string; weight: number }>>([])

onMounted(async () => {
  const { data } = await adminApi.configGet('access_nodes')
  // 后端存的是 JSON 字符串（可能是字符串或数组，兼容两种）
  let raw = data.data
  if (typeof raw === 'string' && raw.trim()) {
    try { raw = JSON.parse(raw) } catch (_) { raw = [] }
  }
  if (Array.isArray(raw)) nodes.value = raw as any
})

function addNode() {
  nodes.value.push({ id: 'node-' + Date.now().toString(36), name: '新节点', wss: '', api: '', weight: 100 })
}

async function removeNode(record: any) {
  nodes.value = nodes.value.filter(n => n.id !== record.id)
  await save(false)
}

async function save(toast = true) {
  // 校验：至少要有 id/name，wss/api 可后续填
  const clean = nodes.value.map(n => ({
    id: n.id || 'node-' + Date.now().toString(36),
    name: n.name || '节点',
    wss: n.wss || '',
    api: n.api || '',
    weight: Number(n.weight || 100)
  }))
  const { data } = await adminApi.configSet('access_nodes', JSON.stringify(clean))
  if (data.code === 0) {
    if (toast) Message.success('节点已保存')
  } else Message.error(data.message)
}
</script>
