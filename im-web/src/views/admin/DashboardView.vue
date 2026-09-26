<template>
  <div class="dashboard">
    <!-- 欢迎横幅 -->
    <section class="welcome">
      <div class="welcome-text">
        <h1>{{ greeting }}，管理员</h1>
        <p>欢迎使用企业 IM 管理后台，今日数据一览</p>
      </div>
      <div class="welcome-stats">
        <div class="ws-item">
          <span class="ws-num">{{ overview.userTotal || 0 }}</span>
          <span class="ws-label">注册用户</span>
        </div>
        <span class="ws-divider"></span>
        <div class="ws-item">
          <span class="ws-num">{{ overview.online || 0 }}</span>
          <span class="ws-label">当前在线</span>
        </div>
        <span class="ws-divider"></span>
        <div class="ws-item">
          <span class="ws-num">{{ overview.msgTotal || 0 }}</span>
          <span class="ws-label">消息总数</span>
        </div>
      </div>
    </section>

    <!-- 指标卡片 -->
    <section class="stat-grid">
      <div v-for="s in statCards" :key="s.key" class="stat-card">
        <div class="stat-icon" :style="{ background: s.gradient }">
          <component :is="s.icon" />
        </div>
        <div class="stat-info">
          <span class="stat-num">{{ s.value }}</span>
          <span class="stat-label">{{ s.label }}</span>
        </div>
      </div>
    </section>

    <!-- 图表 + 快捷入口 -->
    <section class="bottom-grid">
      <!-- 近 7 日消息量 -->
      <div class="chart-card">
        <div class="card-head">
          <h2>近 7 日消息量</h2>
          <span class="card-sub">趋势分析</span>
        </div>
        <div class="chart" v-if="series.length">
          <svg class="line-svg" viewBox="0 0 100 100" preserveAspectRatio="none">
            <defs>
              <linearGradient id="lineGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stop-color="#165dff" stop-opacity="0.28" />
                <stop offset="100%" stop-color="#165dff" stop-opacity="0" />
              </linearGradient>
            </defs>
            <line v-for="i in 3" :key="'g'+i" :x1="0" :x2="100" :y1="i*25" :y2="i*25" class="grid-line" vector-effect="non-scaling-stroke" />
            <path :d="areaPath" fill="url(#lineGradient)" />
            <path :d="linePath" class="line-stroke" fill="none" vector-effect="non-scaling-stroke" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          <div class="points">
            <div v-for="(p, i) in points" :key="i" class="point" :style="{ left: p.x + '%', top: p.y + '%' }">
              <span class="dot"></span>
              <span class="tooltip">{{ p.count }}</span>
            </div>
          </div>
        </div>
        <div class="x-axis" v-if="series.length">
          <span v-for="s in series" :key="s.day">{{ s.day.slice(5) }}</span>
        </div>
        <div v-else class="empty">暂无数据</div>
      </div>

      <!-- 快捷入口 -->
      <div class="quick-card">
        <div class="card-head">
          <h2>快捷入口</h2>
        </div>
        <div class="quick-grid">
          <router-link
            v-for="q in quickLinks"
            :key="q.path"
            :to="q.path"
            class="quick-item"
          >
            <span class="quick-icon" :style="{ color: q.color, background: q.bg }">
              <component :is="q.icon" />
            </span>
            <span class="quick-label">{{ q.label }}</span>
          </router-link>
        </div>

        <div class="card-head" style="margin-top: 20px">
          <h2>系统概览</h2>
        </div>
        <ul class="sys-list">
          <li><span>群组总数</span><b>{{ groupCount }}</b></li>
          <li><span>小程序数量</span><b>{{ appCount }}</b></li>
          <li><span>存储估算</span><b>{{ overview.storageMB || 0 }} MB</b></li>
        </ul>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, markRaw } from 'vue'
import {
  IconUserGroup, IconDashboard, IconMessage, IconStorage,
  IconRelation, IconApps, IconFile, IconSettings
} from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'

const overview = ref<Record<string, any>>({})
const series = ref<Array<{ day: string; count: number }>>([])
const maxCount = ref(1)
const groupCount = ref(0)
const appCount = ref(0)

const greeting = computed(() => {
  const h = new Date().getHours()
  if (h < 6) return '凌晨好'
  if (h < 12) return '早上好'
  if (h < 14) return '中午好'
  if (h < 18) return '下午好'
  return '晚上好'
})

const statCards = computed(() => [
  { key: 'user', label: '注册用户', value: overview.value.userTotal || 0, icon: markRaw(IconUserGroup), gradient: 'linear-gradient(135deg, #165dff, #4080ff)' },
  { key: 'online', label: '当前在线', value: overview.value.online || 0, icon: markRaw(IconDashboard), gradient: 'linear-gradient(135deg, #00b42a, #23c343)' },
  { key: 'msg', label: '消息总数', value: overview.value.msgTotal || 0, icon: markRaw(IconMessage), gradient: 'linear-gradient(135deg, #ff7d00, #ff9a2e)' },
  { key: 'storage', label: '存储 (MB)', value: overview.value.storageMB || 0, icon: markRaw(IconStorage), gradient: 'linear-gradient(135deg, #7b61ff, #9a86ff)' }
])

const quickLinks = [
  { path: '/admin/users', label: '用户管理', icon: markRaw(IconUserGroup), color: '#165dff', bg: '#e8f3ff' },
  { path: '/admin/groups', label: '群组管理', icon: markRaw(IconRelation), color: '#00b42a', bg: '#e8ffea' },
  { path: '/admin/apps', label: '小程序', icon: markRaw(IconApps), color: '#ff7d00', bg: '#fff7e8' },
  { path: '/admin/logs', label: '日志', icon: markRaw(IconFile), color: '#7b61ff', bg: '#f3f0ff' },
  { path: '/admin/messages', label: '消息记录', icon: markRaw(IconMessage), color: '#14c9c9', bg: '#e8fffb' },
  { path: '/admin/configs', label: '系统配置', icon: markRaw(IconSettings), color: '#f53f3f', bg: '#ffece8' }
]

// 折线图坐标点：x/y 均为 0-100 百分比
const points = computed(() => {
  const n = series.value.length
  if (!n) return []
  return series.value.map((s, i) => {
    const x = n === 1 ? 50 : (i / (n - 1)) * 100
    const y = (1 - s.count / maxCount.value) * 88 + 6
    return { x, y, count: s.count, day: s.day.slice(5) }
  })
})

const linePath = computed(() => {
  const pts = points.value
  if (!pts.length) return ''
  return pts.map((p, i) => (i === 0 ? `M ${p.x} ${p.y}` : `L ${p.x} ${p.y}`)).join(' ')
})

const areaPath = computed(() => {
  const pts = points.value
  if (!pts.length) return ''
  const line = pts.map((p, i) => (i === 0 ? `M ${p.x} ${p.y}` : `L ${p.x} ${p.y}`)).join(' ')
  return `${line} L 100 100 L 0 100 Z`
})

onMounted(async () => {
  // 容错：后端未启动或返回 null 时降级为 0 / 空
  try {
    const [o, m, g, a] = await Promise.all([
      adminApi.statsOverview(),
      adminApi.statsMessages(7),
      adminApi.groups(),
      adminApi.apps()
    ])
    if (o.data.code === 0 && o.data.data) overview.value = o.data.data as never
    if (m.data.code === 0 && m.data.data) {
      const md = m.data.data as { series?: Array<{ day: string; count: number }> }
      const arr = md.series ?? []
      series.value = arr
      maxCount.value = Math.max(...arr.map((s) => s.count), 1)
    }
    if (g.data.code === 0 && Array.isArray(g.data.data)) groupCount.value = (g.data.data as any[]).length
    if (a.data.code === 0 && Array.isArray(a.data.data)) appCount.value = (a.data.data as any[]).length
  } catch {
    /* 后端未启动，保持默认空值 */
  }
})
</script>

<style scoped>
.dashboard { display: flex; flex-direction: column; gap: var(--app-space-xl); }

/* ===== 欢迎横幅 ===== */
.welcome {
  position: relative;
  display: flex; align-items: center; justify-content: space-between;
  padding: 24px 28px;
  border-radius: var(--app-radius-lg);
  background: linear-gradient(135deg, #165dff 0%, #4080ff 100%);
  color: #fff;
  overflow: hidden;
  box-shadow: 0 8px 24px rgba(22, 93, 255, 0.25);
}
.welcome::after {
  content: '';
  position: absolute;
  right: -40px; top: -40px;
  width: 220px; height: 220px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.08);
}
.welcome::before {
  content: '';
  position: absolute;
  right: 60px; bottom: -60px;
  width: 140px; height: 140px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.06);
}
.welcome-text { position: relative; z-index: 1; }
.welcome-text h1 { margin: 0; font-size: var(--app-font-size-2xl); font-weight: var(--app-font-weight-semibold); }
.welcome-text p { margin: 6px 0 0; font-size: var(--app-font-size-sm); opacity: 0.9; }

.welcome-stats { display: flex; align-items: center; gap: 24px; position: relative; z-index: 1; }
.ws-item { display: flex; flex-direction: column; align-items: center; gap: 2px; }
.ws-num { font-size: var(--app-font-size-2xl); font-weight: var(--app-font-weight-bold); }
.ws-label { font-size: var(--app-font-size-xs); opacity: 0.85; }
.ws-divider { width: 1px; height: 32px; background: rgba(255, 255, 255, 0.25); }

/* ===== 指标卡片 ===== */
.stat-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--app-space-lg);
}
.stat-card {
  display: flex; align-items: center; gap: 14px;
  background: var(--app-bg-card);
  border: 1px solid var(--app-border-2);
  border-radius: var(--app-radius-lg);
  padding: 18px 20px;
  box-shadow: var(--app-shadow-card);
  transition: transform var(--app-transition-base), box-shadow var(--app-transition-base);
}
.stat-card:hover { transform: translateY(-2px); box-shadow: var(--app-shadow-md); }
.stat-icon {
  width: 48px; height: 48px;
  border-radius: var(--app-radius-md);
  display: flex; align-items: center; justify-content: center;
  color: #fff;
  flex-shrink: 0;
}
.stat-icon :deep(svg) { width: 24px; height: 24px; }
.stat-info { display: flex; flex-direction: column; gap: 2px; }
.stat-num { font-size: var(--app-font-size-2xl); font-weight: var(--app-font-weight-bold); color: var(--app-text-1); line-height: 1.2; }
.stat-label { font-size: var(--app-font-size-sm); color: var(--app-text-3); }

/* ===== 底部网格 ===== */
.bottom-grid {
  display: grid;
  grid-template-columns: 1.6fr 1fr;
  gap: var(--app-space-lg);
}

.chart-card, .quick-card {
  background: var(--app-bg-card);
  border: 1px solid var(--app-border-2);
  border-radius: var(--app-radius-lg);
  padding: 20px;
  box-shadow: var(--app-shadow-card);
}
.card-head { display: flex; align-items: baseline; gap: 10px; margin-bottom: 16px; }
.card-head h2 { margin: 0; font-size: var(--app-font-size-lg); font-weight: var(--app-font-weight-semibold); color: var(--app-text-1); }
.card-sub { font-size: var(--app-font-size-xs); color: var(--app-text-3); }

/* 折线图 */
.chart { position: relative; height: 220px; padding: 8px 4px 0; }
.line-svg { width: 100%; height: 100%; display: block; }
.grid-line { stroke: var(--app-border-2); stroke-width: 1; stroke-dasharray: 3 3; }
.line-stroke { stroke: var(--app-primary); stroke-width: 2.5; transition: stroke var(--app-transition-base); }

.points { position: absolute; inset: 8px 4px 0; pointer-events: none; }
.point { position: absolute; transform: translate(-50%, -50%); pointer-events: auto; }
.dot {
  display: block; width: 8px; height: 8px;
  border-radius: 50%;
  background: #fff;
  border: 2px solid var(--app-primary);
  box-shadow: 0 1px 4px rgba(22, 93, 255, 0.3);
  transition: transform var(--app-transition-base);
}
.point:hover .dot { transform: scale(1.5); }
.tooltip {
  position: absolute; bottom: 14px; left: 50%; transform: translateX(-50%);
  background: var(--app-text-1); color: #fff;
  font-size: var(--app-font-size-xs); padding: 3px 8px;
  border-radius: var(--app-radius-sm);
  white-space: nowrap; opacity: 0;
  transition: opacity var(--app-transition-base);
  pointer-events: none;
}
.tooltip::after {
  content: ''; position: absolute; top: 100%; left: 50%; transform: translateX(-50%);
  border: 4px solid transparent; border-top-color: var(--app-text-1);
}
.point:hover .tooltip { opacity: 1; }

.x-axis {
  display: flex; justify-content: space-between;
  padding: 8px 4px 0;
  font-size: var(--app-font-size-xs); color: var(--app-text-3);
}

.empty { height: 220px; display: flex; align-items: center; justify-content: center; color: var(--app-text-3); }

/* 快捷入口 */
.quick-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px;
}
.quick-item {
  display: flex; flex-direction: column; align-items: center; gap: 8px;
  padding: 14px 8px;
  border-radius: var(--app-radius-md);
  text-decoration: none;
  transition: background var(--app-transition-base);
}
.quick-item:hover { background: var(--app-border-2); }
.quick-icon {
  width: 40px; height: 40px;
  border-radius: var(--app-radius-md);
  display: flex; align-items: center; justify-content: center;
}
.quick-icon :deep(svg) { width: 22px; height: 22px; }
.quick-label { font-size: var(--app-font-size-sm); color: var(--app-text-2); }

/* 系统概览列表 */
.sys-list { list-style: none; margin: 0; padding: 0; }
.sys-list li {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px 0;
  border-bottom: 1px solid var(--app-border-2);
  font-size: var(--app-font-size-sm);
}
.sys-list li:last-child { border-bottom: none; }
.sys-list span { color: var(--app-text-3); }
.sys-list b { color: var(--app-text-1); font-weight: var(--app-font-weight-semibold); }

/* ===== 响应式 ===== */
@media (max-width: 1100px) {
  .stat-grid { grid-template-columns: repeat(2, 1fr); }
  .bottom-grid { grid-template-columns: 1fr; }
}
@media (max-width: 640px) {
  .welcome { flex-direction: column; align-items: flex-start; gap: 16px; }
  .stat-grid { grid-template-columns: 1fr; }
  .quick-grid { grid-template-columns: repeat(2, 1fr); }
}
</style>
