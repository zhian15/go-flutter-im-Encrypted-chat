<template>
  <div class="page">
    <a-row :gutter="16">
      <a-col :span="6">
        <a-card class="stat-card">
          <a-statistic title="注册用户" :value="overview.userTotal || 0" />
        </a-card>
      </a-col>
      <a-col :span="6">
        <a-card class="stat-card">
          <a-statistic title="当前在线" :value="overview.online || 0" />
        </a-card>
      </a-col>
      <a-col :span="6">
        <a-card class="stat-card">
          <a-statistic title="消息总数" :value="overview.msgTotal || 0" />
        </a-card>
      </a-col>
      <a-col :span="6">
        <a-card class="stat-card">
          <a-statistic title="存储估算 (MB)" :value="overview.storageMB || 0" />
        </a-card>
      </a-col>
    </a-row>

    <a-card title="近 7 日消息量" style="margin-top: 16px">
      <div class="chart">
        <div v-for="s in series" :key="s.day" class="bar-col">
          <div class="bar-val">{{ s.count }}</div>
          <div class="bar" :style="{ height: barHeight(s.count) + 'px' }"></div>
          <div class="bar-day">{{ s.day.slice(5) }}</div>
        </div>
        <div v-if="!series.length" class="empty">暂无数据</div>
      </div>
    </a-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminApi } from '@/api/admin'

const overview = ref<Record<string, any>>({})
const series = ref<Array<{ day: string; count: number }>>([])
const maxCount = ref(1)

onMounted(async () => {
  const [o, m] = await Promise.all([adminApi.statsOverview(), adminApi.statsMessages(7)])
  if (o.data.code === 0) overview.value = o.data.data as never
  if (m.data.code === 0) {
    series.value = (m.data.data as { series: Array<{ day: string; count: number }> }).series
    maxCount.value = Math.max(...series.value.map((s) => s.count), 1)
  }
})

function barHeight(count: number) {
  return Math.max(4, Math.round((count / maxCount.value) * 160))
}
</script>

<style scoped>
.stat-card { text-align: center; }
.chart { display: flex; align-items: flex-end; gap: 12px; height: 220px; padding: 12px; border: 1px solid var(--color-border-2); border-radius: 8px; }
.bar-col { flex: 1; display: flex; flex-direction: column; align-items: center; gap: 4px; height: 100%; justify-content: flex-end; }
.bar { width: 60%; background: var(--app-primary); border-radius: var(--app-radius-xs) var(--app-radius-xs) 0 0; transition: opacity var(--app-transition-base); }
.bar:hover { opacity: 0.8; }
.bar-val { font-size: 12px; color: var(--color-text-2); }
.bar-day { font-size: 12px; color: var(--color-text-3); }
.empty { margin: auto; color: var(--color-text-3); }
</style>
