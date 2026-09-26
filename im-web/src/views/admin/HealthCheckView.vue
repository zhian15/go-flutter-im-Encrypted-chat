<template>
  <div class="hc-page">
    <a-card>
      <div class="toolbar">
        <div class="summary">
          <span class="summary-title">
            <IconSync :class="{ spin: checking }" />
            系统状态
          </span>
          <a-tag :color="okCount === checks.length ? 'green' : okCount > 0 ? 'orange' : 'red'">
            {{ okCount }}/{{ checks.length }} 正常
          </a-tag>
          <span class="summary-hint">检测数据来自服务端真实连接探测（.env 配置）</span>
        </div>
        <a-button type="primary" :loading="checking" @click="runAll">
          <template #icon><IconRefresh /></template>重新检测
        </a-button>
      </div>

      <div class="check-grid">
        <div
          v-for="c in checks"
          :key="c.key"
          class="check-card"
          :class="['is-' + c.status]"
        >
          <div class="check-head">
            <div class="check-icon" :style="{ background: c.gradient }">
              <component :is="c.icon" />
            </div>
            <div class="check-head-info">
              <span class="check-name">{{ c.name }}</span>
              <span class="check-key">{{ c.key }}</span>
            </div>
          </div>
          <div class="check-status" :class="'st-' + c.status">
            <IconCheckCircleFill v-if="c.status === 'ok'" />
            <IconExclamationCircleFill v-else-if="c.status === 'warn'" />
            <IconClose v-else />
            <span>{{ statusText(c.status) }}</span>
          </div>
          <div class="check-msg">{{ c.message || '—' }}</div>
          <div v-if="c.details" class="check-details">
            <div v-for="(v, k) in c.details" :key="k" class="detail-row">
              <span>{{ k }}</span>
              <b>{{ v }}</b>
            </div>
          </div>
          <div class="check-foot">
            <span class="check-time">
              上次检测：{{ c.checkedAt || '未检测' }}<template v-if="c.latencyMs > 0"> · {{ c.latencyMs }}ms</template>
            </span>
            <span class="foot-btns">
              <a-button
                v-if="c.key === 'api' || c.key === 'wss'"
                size="mini" status="danger"
                :disabled="restarting"
                @click="confirmRestart(c)"
              >重启服务</a-button>
              <a-button size="mini" :loading="checking" @click="runOne(c)">单项检测</a-button>
            </span>
          </div>
        </div>
      </div>

      <a-card class="env-card" style="margin-top: 16px" :bordered="false" title="环境信息">
        <a-descriptions :column="2" bordered size="small">
          <a-descriptions-item label="检测时间">{{ runAt || '—' }}</a-descriptions-item>
          <a-descriptions-item label="当前节点 ID">{{ nodeId || '—' }}</a-descriptions-item>
          <a-descriptions-item label="Go 运行版本">{{ goVersion || '—' }}</a-descriptions-item>
          <a-descriptions-item label="前端版本">{{ frontVersion }}</a-descriptions-item>
        </a-descriptions>
      </a-card>
    </a-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, markRaw } from 'vue'
import { Message, Modal } from '@arco-design/web-vue'
import {
  IconSync, IconRefresh,
  IconCheckCircleFill, IconExclamationCircleFill, IconClose,
  IconCommand, IconExperiment, IconThunderbolt, IconCloud, IconBook, IconFileImage, IconStorage, IconSchedule
} from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'

type St = 'idle' | 'ok' | 'warn' | 'err'
type CheckItem = {
  key: string
  name: string
  icon: any
  gradient: string
  status: St
  message: string
  checkedAt: string
  latencyMs: number
  restartable?: boolean
  details?: Record<string, string>
}

// key 与服务端 AdminHealthCheck 一一对应（GET /admin/health/:key）
const checks: CheckItem[] = reactive([
  {
    key: 'api', name: 'API 服务', icon: markRaw(IconCommand),
    gradient: 'linear-gradient(135deg, #165dff, #4080ff)',
    status: 'idle', message: '', checkedAt: '', latencyMs: 0, restartable: true
  },
  {
    key: 'wss', name: 'WSS 长连接服务', icon: markRaw(IconThunderbolt),
    gradient: 'linear-gradient(135deg, #F7BA2A, #FFC84C)',
    status: 'idle', message: '', checkedAt: '', latencyMs: 0, restartable: true
  },
  {
    key: 'mysql', name: 'MySQL 主数据库', icon: markRaw(IconBook),
    gradient: 'linear-gradient(135deg, #00B42A, #37D45C)',
    status: 'idle', message: '', checkedAt: '', latencyMs: 0
  },
  {
    key: 'redis', name: 'Redis 缓存', icon: markRaw(IconExperiment),
    gradient: 'linear-gradient(135deg, #DC382D, #EF6B5F)',
    status: 'idle', message: '', checkedAt: '', latencyMs: 0
  },
  {
    key: 'mongo', name: 'MongoDB 消息存储', icon: markRaw(IconFileImage),
    gradient: 'linear-gradient(135deg, #47A248, #7EC870)',
    status: 'idle', message: '', checkedAt: '', latencyMs: 0
  },
  {
    key: 'minio', name: 'MinIO 对象存储', icon: markRaw(IconStorage),
    gradient: 'linear-gradient(135deg, #C72C49, #E06B7D)',
    status: 'idle', message: '', checkedAt: '', latencyMs: 0
  },
  {
    key: 'jpush', name: '极光推送配置', icon: markRaw(IconCloud),
    gradient: 'linear-gradient(135deg, #722ED1, #A36EE0)',
    status: 'idle', message: '', checkedAt: '', latencyMs: 0
  },
  {
    key: 'version', name: '运行环境（.env）', icon: markRaw(IconSchedule),
    gradient: 'linear-gradient(135deg, #86909C, #A9B4BE)',
    status: 'idle', message: '', checkedAt: '', latencyMs: 0
  }
])

const checking = ref(false)
const restarting = ref(false)
const runAt = ref('')
const nodeId = ref('')
const goVersion = ref('')
const frontVersion = '1.0.0'

const okCount = computed(() => checks.filter((c) => c.status === 'ok').length)

function statusText(s: St) {
  return ({ idle: '待检测', ok: '正常', warn: '警告', err: '异常' } as Record<St, string>)[s]
}

function fmt(d: Date) {
  const p = (n: number) => n.toString().padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

onMounted(() => { runAll() })

async function runAll() {
  checking.value = true
  try {
    for (const c of checks) await runOne(c, true)
    runAt.value = fmt(new Date())
  } finally {
    checking.value = false
  }
}

async function runOne(c: CheckItem, batch = false) {
  c.status = 'idle'
  c.message = ''
  c.checkedAt = ''
  c.latencyMs = 0
  c.details = undefined
  try {
    const { data } = await adminApi.healthCheck(c.key)
    if (data.code === 0) {
      // 服务端返回 { [key]: { status, message, details, latencyMs } }
      const d = (data.data || {})[c.key] as
        { status?: string; message?: string; details?: Record<string, string>; latencyMs?: number } | undefined
      if (d && d.status) {
        c.status = (d.status === 'ok' ? 'ok' : d.status === 'warn' ? 'warn' : 'err') as St
        c.message = d.message || ''
        c.details = d.details
        c.latencyMs = d.latencyMs || 0
        if (c.key === 'version') {
          const dd = d.details || {}
          nodeId.value = dd['NodeID'] || nodeId.value
        }
        if (c.key === 'api') {
          const dd = d.details || {}
          goVersion.value = dd['Go版本'] || goVersion.value
          if (!nodeId.value) nodeId.value = dd['NodeID'] || ''
        }
      } else {
        c.status = 'err'
        c.message = '接口返回格式异常'
      }
    } else {
      c.status = 'err'
      c.message = data.message || '接口返回异常'
    }
  } catch (e: any) {
    c.status = 'err'
    c.message = '检测接口请求失败：' + (e?.message || e)
  } finally {
    c.checkedAt = fmt(new Date())
    if (!batch) Message.info(`${c.name} 检测完成`)
  }
}

/** 重启 api / gateway（systemd 托管环境） */
function confirmRestart(c: CheckItem) {
  const target = c.key === 'api' ? 'API 服务' : 'WSS 网关'
  Modal.confirm({
    title: '确认重启',
    content: `重启${target}（systemctl restart，Restart=always 自动拉起）。` +
      (c.key === 'api' ? '重启期间后台与 App 接口会短暂中断（约 3~5 秒）。' : '在线客户端的 WS 连接会断开并自动重连。'),
    okText: '确认重启',
    cancelText: '取消',
    onOk: () => doRestart(c.key === 'api' ? 'api' : 'gateway')
  })
}

async function doRestart(target: 'api' | 'gateway') {
  restarting.value = true
  try {
    const { data } = await adminApi.systemRestart(target)
    if (data.code === 0) {
      Message.success(data.message || '重启指令已下发')
      // gateway 重启后等 3 秒刷新状态；api 重启会短暂失联，等 5 秒再刷
      await new Promise((r) => setTimeout(r, target === 'api' ? 5000 : 3000))
      const card = checks.find((x) => x.key === target)
      if (card) await runOne(card, true)
      runAt.value = fmt(new Date())
    } else {
      Message.warning(data.message || '重启失败')
    }
  } catch (e: any) {
    Message.error('重启请求失败：' + (e?.message || e))
  } finally {
    restarting.value = false
  }
}
</script>

<style scoped>
.toolbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 20px; }
.summary { display: flex; align-items: center; gap: 10px; }
.summary-title { display: inline-flex; align-items: center; gap: 8px; font-size: var(--app-font-size-lg); font-weight: var(--app-font-weight-semibold); color: var(--app-text-1); }
.summary-title :deep(.spin) { animation: spin 1.2s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
.summary-hint { font-size: 12px; color: var(--app-text-3); }

.check-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--app-space-lg);
}

.check-card {
  display: flex; flex-direction: column;
  padding: 18px;
  background: var(--app-bg-card);
  border: 1px solid var(--app-border-2);
  border-radius: var(--app-radius-lg);
  box-shadow: var(--app-shadow-card);
  transition: transform var(--app-transition-base), border-color var(--app-transition-base);
}
.check-card:hover { transform: translateY(-2px); }
.check-card.is-ok { border-left: 4px solid #00b42a; }
.check-card.is-warn { border-left: 4px solid #ff7d00; }
.check-card.is-err { border-left: 4px solid #f53f3f; animation: shake 0.4s ease-in-out 1; }
@keyframes shake {
  0%, 100% { transform: translateX(0); }
  25% { transform: translateX(-3px); }
  75% { transform: translateX(3px); }
}

.check-head { display: flex; align-items: center; gap: 12px; margin-bottom: 14px; }
.check-icon {
  width: 42px; height: 42px;
  border-radius: var(--app-radius-md);
  display: flex; align-items: center; justify-content: center;
  color: #fff; flex-shrink: 0;
}
.check-icon :deep(svg) { width: 22px; height: 22px; }
.check-head-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.check-name { font-size: var(--app-font-size-base); font-weight: var(--app-font-weight-semibold); color: var(--app-text-1); }
.check-key { font-size: 11px; color: var(--app-text-3); font-family: ui-monospace, Menlo, monospace; }

.check-status { display: inline-flex; align-items: center; gap: 6px; font-size: var(--app-font-size-sm); font-weight: var(--app-font-weight-medium); padding-bottom: 6px; }
.check-status.st-ok { color: #00b42a; }
.check-status.st-warn { color: #ff7d00; }
.check-status.st-err { color: #f53f3f; }
.check-status.st-idle { color: var(--app-text-3); }
.check-status :deep(svg) { width: 16px; height: 16px; }

.check-msg {
  font-size: var(--app-font-size-sm); color: var(--app-text-2);
  min-height: 40px;
  padding-bottom: 10px;
  line-height: 1.5;
}

.check-details {
  background: var(--app-border-2);
  border-radius: var(--app-radius-md);
  padding: 10px 12px;
  margin-bottom: 12px;
}
.detail-row {
  display: flex; align-items: center; justify-content: space-between;
  gap: 12px;
  padding: 3px 0;
  font-size: 12px;
  color: var(--app-text-2);
}
.detail-row b { color: var(--app-text-1); font-weight: var(--app-font-weight-medium); text-align: right; word-break: break-all; }

.check-foot {
  display: flex; align-items: center; justify-content: space-between;
  gap: 8px;
  margin-top: auto;
  padding-top: 8px;
  border-top: 1px dashed var(--app-border-2);
}
.check-time { font-size: 11px; color: var(--app-text-3); }
.foot-btns { display: inline-flex; gap: 6px; flex-shrink: 0; }

.env-card :deep(.arco-card-header-title) { font-weight: var(--app-font-weight-semibold); }

@media (max-width: 1200px) { .check-grid { grid-template-columns: repeat(2, 1fr); } }
@media (max-width: 640px) { .check-grid { grid-template-columns: 1fr; } }
</style>
