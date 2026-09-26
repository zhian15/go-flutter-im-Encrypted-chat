<template>
  <div class="dc-page">
    <div class="dc-hero">
      <div class="dc-hero-icon">
        <component :is="iconWarning" />
      </div>
      <div class="dc-hero-text">
        <h2 class="dc-hero-title">数据清空</h2>
        <p class="dc-hero-desc">
          危险操作：清空后数据<span class="dc-hero-warn">不可恢复</span>（服务器不会备份）。
          管理员账号会被保留，不会被清空锁在门外。
        </p>
      </div>
    </div>

    <div class="dc-options">
      <div
        v-for="opt in options"
        :key="opt.value"
        class="dc-card"
        :class="{ active: scope === opt.value, danger: opt.danger }"
        @click="scope = opt.value"
      >
        <div class="dc-card-check">
          <span class="dc-radio" :class="{ checked: scope === opt.value }"></span>
        </div>
        <div class="dc-card-body">
          <div class="dc-card-head">
            <span class="dc-card-icon" :class="{ danger: opt.danger }">
              <component :is="opt.icon" />
            </span>
            <span class="dc-card-title" :class="{ danger: opt.danger }">{{ opt.label }}</span>
          </div>
          <p class="dc-card-desc">{{ opt.desc }}</p>
        </div>
      </div>
    </div>

    <div class="dc-actions">
      <a-button type="primary" status="danger" size="large" :loading="clearing" @click="confirmFirst">
        <template #icon><component :is="iconDelete" /></template>
        清空所选数据
      </a-button>
    </div>

    <a-alert v-if="resultText" type="success" class="dc-result" show-icon>
      {{ '清空完成：' + resultText }}
    </a-alert>

    <!-- 第二次确认：必须输入「清空」两个字 -->
    <a-modal
      v-model:visible="showFinal"
      title="最终确认"
      :ok-text="'确认清空'"
      :ok-loading="clearing"
      :ok-button-props="{ status: 'danger', disabled: !canFinal }"
      @ok="doClear"
      width="420"
    >
      <div class="final-body">
        <p class="final-line">
          即将清空：<b>{{ scopeLabel }}</b>。该操作<b class="danger">不可恢复</b>。
        </p>
        <p class="final-line muted">如确认执行，请在下方输入「清空」两个字：</p>
        <a-input v-model="confirmText" placeholder="输入：清空" allow-clear />
      </div>
    </a-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, markRaw } from 'vue'
import { Message, Modal } from '@arco-design/web-vue'
import {
  IconUser, IconMessage, IconUserGroup, IconQrcode, IconWechatpay,
  IconDelete, IconExclamationCircle, IconStorage
} from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'

const iconWarning = markRaw(IconExclamationCircle)
const iconDelete = markRaw(IconDelete)

const scope = ref('chats')
const clearing = ref(false)
const showFinal = ref(false)
const confirmText = ref('')
const resultText = ref('')

const options = [
  { value: 'users', label: '用户数据', desc: '删除普通用户/客服账号及其好友关系、好友申请、黑名单、设备、E2E 密钥；靓号池仅解除占用不删除', icon: markRaw(IconUser), danger: false },
  { value: 'chats', label: '聊天数据', desc: '删除全部消息（含图片/文件记录）、会话列表、已读回执、收藏', icon: markRaw(IconMessage), danger: false },
  { value: 'groups', label: '群组数据', desc: '删除全部群会话与群成员记录', icon: markRaw(IconUserGroup), danger: false },
  { value: 'recharge', label: '充值记录', desc: '删除全部充值订单（不影响用户余额与钱包流水）', icon: markRaw(IconQrcode), danger: false },
  { value: 'withdraw', label: '提现记录', desc: '删除全部提现订单（不影响用户余额与钱包流水）', icon: markRaw(IconWechatpay), danger: false },
  { value: 'all', label: '所有数据', desc: '清空全部业务数据：用户、好友、聊天、群组、钱包流水与红包、靓号池、充值/提现订单与提现绑定、朋友圈、登录日志等；仅保留后台配置与管理员账号', icon: markRaw(IconStorage), danger: true },
]

const SCOPE_LABELS: Record<string, string> = {
  users: '用户数据', chats: '聊天数据', groups: '群组数据',
  recharge: '充值记录', withdraw: '提现记录', all: '所有数据'
}
const scopeLabel = computed(() => SCOPE_LABELS[scope.value] || scope.value)
const canFinal = computed(() => confirmText.value.trim() === '清空')

function confirmFirst() {
  Modal.confirm({
    title: `确认清空「${scopeLabel.value}」？`,
    content: '清空后数据不可恢复。点击「继续」后还需输入「清空」二次确认。',
    okText: '继续',
    cancelText: '取消',
    onOk: () => {
      confirmText.value = ''
      showFinal.value = true
    }
  })
}

async function doClear() {
  if (!canFinal.value) return
  clearing.value = true
  resultText.value = ''
  try {
    const { data } = await adminApi.dataClear(scope.value)
    if (data.code === 0) {
      const d = data.data || {}
      const parts = Object.entries(d).map(([k, v]) => `${k} ${v ?? 0}`)
      resultText.value = parts.length ? parts.join('，') : '已完成'
      Message.success('清空完成')
      showFinal.value = false
    } else {
      Message.error(data.message || '清空失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '清空失败（网络错误）')
  } finally {
    clearing.value = false
  }
}
</script>

<style scoped>
.dc-page {
  display: flex; flex-direction: column; gap: 20px;
  max-width: 980px;
}

/* 顶部警示条 */
.dc-hero {
  display: flex; align-items: center; gap: 16px;
  padding: 20px 24px;
  background: linear-gradient(120deg, rgba(245,63,63,.08) 0%, rgba(255,125,0,.06) 100%);
  border: 1px solid rgba(245,63,63,.25);
  border-radius: var(--app-radius-lg, 12px);
  box-shadow: 0 2px 8px rgba(245,63,63,.06);
}
.dc-hero-icon {
  width: 48px; height: 48px; flex-shrink: 0;
  border-radius: 14px;
  background: linear-gradient(135deg, #f53f3f, #ff7d00);
  color: #fff;
  display: flex; align-items: center; justify-content: center;
  box-shadow: 0 4px 14px rgba(245,63,63,.35);
}
.dc-hero-icon :deep(svg) { width: 24px; height: 24px; }
.dc-hero-text { min-width: 0; }
.dc-hero-title {
  margin: 0 0 4px;
  font-size: var(--app-font-size-xl, 18px);
  font-weight: var(--app-font-weight-semibold, 600);
  color: var(--app-text-1);
}
.dc-hero-desc {
  margin: 0;
  font-size: var(--app-font-size-sm, 13px);
  color: var(--app-text-2);
  line-height: 1.6;
}
.dc-hero-warn {
  color: #f53f3f;
  font-weight: 600;
}

/* 选项卡片网格 */
.dc-options {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 14px;
}
.dc-card {
  position: relative;
  display: flex; gap: 12px;
  padding: 18px;
  background: var(--app-bg-card, #fff);
  border: 2px solid var(--app-border-2, #e5e6eb);
  border-radius: var(--app-radius-lg, 12px);
  cursor: pointer;
  transition: all .22s cubic-bezier(.22,.61,.36,1);
  box-shadow: 0 1px 3px rgba(31,35,41,.04);
}
.dc-card:hover {
  border-color: rgba(22,93,255,.35);
  box-shadow: 0 4px 16px rgba(22,93,255,.1);
  transform: translateY(-2px);
}
.dc-card.active {
  border-color: #165dff;
  background: linear-gradient(135deg, rgba(22,93,255,.04) 0%, rgba(120,67,255,.04) 100%);
  box-shadow: 0 6px 20px rgba(22,93,255,.15);
}
.dc-card.danger:hover {
  border-color: rgba(245,63,63,.4);
  box-shadow: 0 4px 16px rgba(245,63,63,.12);
}
.dc-card.danger.active {
  border-color: #f53f3f;
  background: linear-gradient(135deg, rgba(245,63,63,.05) 0%, rgba(255,125,0,.04) 100%);
  box-shadow: 0 6px 20px rgba(245,63,63,.18);
}
.dc-card-check { padding-top: 2px; flex-shrink: 0; }
.dc-radio {
  display: block;
  width: 18px; height: 18px;
  border-radius: 50%;
  border: 2px solid #c9cdd4;
  background: #fff;
  transition: all .2s ease;
  position: relative;
}
.dc-radio.checked {
  border-color: #165dff;
  background: #165dff;
}
.dc-radio.checked::after {
  content: '';
  position: absolute;
  top: 50%; left: 50%;
  transform: translate(-50%, -50%);
  width: 6px; height: 6px;
  border-radius: 50%;
  background: #fff;
}
.dc-card.danger .dc-radio.checked {
  border-color: #f53f3f;
  background: #f53f3f;
}
.dc-card-body { min-width: 0; flex: 1; }
.dc-card-head {
  display: flex; align-items: center; gap: 8px;
  margin-bottom: 8px;
}
.dc-card-icon {
  width: 28px; height: 28px; flex-shrink: 0;
  border-radius: 8px;
  background: linear-gradient(135deg, rgba(22,93,255,.1), rgba(120,67,255,.1));
  color: #165dff;
  display: flex; align-items: center; justify-content: center;
}
.dc-card-icon.danger {
  background: linear-gradient(135deg, rgba(245,63,63,.1), rgba(255,125,0,.1));
  color: #f53f3f;
}
.dc-card-icon :deep(svg) { width: 16px; height: 16px; }
.dc-card-title {
  font-size: var(--app-font-size-base, 14px);
  font-weight: var(--app-font-weight-semibold, 600);
  color: var(--app-text-1);
}
.dc-card-title.danger { color: #f53f3f; }
.dc-card-desc {
  margin: 0;
  font-size: var(--app-font-size-xs, 12px);
  color: var(--app-text-3);
  line-height: 1.6;
}

/* 操作区 */
.dc-actions {
  display: flex; justify-content: flex-start;
  padding-top: 4px;
}
.dc-actions :deep(.arco-btn) {
  height: 44px;
  padding: 0 28px;
  border-radius: 10px;
  font-weight: 600;
}

/* 结果提示 */
.dc-result {
  border-radius: var(--app-radius-lg, 12px);
}

/* 二次确认弹窗 */
.final-body { display: flex; flex-direction: column; gap: 10px; }
.final-line { margin: 0; line-height: 1.6; }
.final-line .danger { color: var(--color-danger, #f53f3f); }
.muted { color: var(--app-text-3); font-size: 12px; }
</style>
