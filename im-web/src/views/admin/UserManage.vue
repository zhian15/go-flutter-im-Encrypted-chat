<template>
  <div class="page">
    <a-card>
      <div class="toolbar">
        <a-input-search v-model="query.kw" placeholder="昵称 / 账号 / 手机号 / 邮箱 / ID" style="width: 280px" allow-clear @search="load(1)" />
        <a-select v-model="query.status" placeholder="状态" style="width: 120px" allow-clear @change="load(1)">
          <a-option :value="1">正常</a-option>
          <a-option :value="2">禁用</a-option>
        </a-select>
        <a-select v-model="query.role" placeholder="角色" style="width: 130px" allow-clear @change="load(1)">
          <a-option :value="0">全部</a-option>
          <a-option :value="1">普通用户</a-option>
          <a-option :value="2">管理员</a-option>
          <a-option :value="3">客服</a-option>
        </a-select>
        <a-input-search v-model="query.inviteCode" placeholder="按邀请码搜索（谁用这个码注册）" style="width: 200px" allow-clear @search="load(1)" @clear="load(1)" />
        <a-button @click="resetQuery">重置</a-button>
        <a-button type="primary" @click="openCreate">创建账号</a-button>
        <a-button type="outline" status="success" @click="openBatch">批量生成账号</a-button>
      </div>

      <a-table :data="list" :pagination="pagination" :loading="loading" row-key="id" @page-change="load" :scroll="{ x: 1500 }">
        <template #columns>
          <a-table-column title="用户" :width="240">
            <template #cell="{ record }">
              <div class="user-cell">
                <span class="avatar" :style="{ background: avatarColor(record.id) }">
                  <img v-if="record.avatar" :src="record.avatar" alt="" />
                  <template v-else>{{ (record.nickname || record.account || '?').slice(0, 1).toUpperCase() }}</template>
                </span>
                <div class="user-info">
                  <span class="nickname">{{ record.nickname || '-' }}</span>
                  <span class="account">{{ record.account }}</span>
                </div>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="ID" :width="110">
            <template #cell="{ record }">
              <span v-if="record.shortId" class="short-id">#{{ record.shortId }}</span>
              <span v-else class="muted">—</span>
            </template>
          </a-table-column>
          <a-table-column title="余额" :width="130" align="right">
            <template #cell="{ record }">
              <span class="balance">
                <IconGift /> {{ fmtMoney(record.balance ?? record.wallet?.balance ?? 0) }}
              </span>
            </template>
          </a-table-column>
          <a-table-column title="邀请码" :width="110">
            <template #cell="{ record }">
              <span v-if="record.invitedCode" class="short-id">{{ record.invitedCode }}</span>
              <span v-else class="muted">—</span>
            </template>
          </a-table-column>
          <a-table-column title="角色" :width="90">
            <template #cell="{ record }">
              <a-tag v-if="record.role === 3" color="orange">客服</a-tag>
              <a-tag v-else :color="record.role === 2 ? 'arcoblue' : 'gray'">{{ record.role === 2 ? '管理员' : '用户' }}</a-tag>
            </template>
          </a-table-column>
          <a-table-column title="状态" :width="90">
            <template #cell="{ record }">
              <a-tag :color="record.status === 1 ? 'green' : 'red'">{{ record.status === 1 ? '正常' : '禁用' }}</a-tag>
            </template>
          </a-table-column>
          <a-table-column title="最后登录IP" :width="140">
            <template #cell="{ record }">
              <span v-if="record.lastLoginIP || record.lastLoginIp" class="ip-chip">
                <IconLocation />
                {{ record.lastLoginIP || record.lastLoginIp }}
              </span>
              <span v-else class="muted">—</span>
            </template>
          </a-table-column>
          <a-table-column title="最后登录" :width="170">
            <template #cell="{ record }">
              <span v-if="record.lastLoginAt">{{ fmt(record.lastLoginAt) }}</span>
              <span v-else class="muted">从未登录</span>
            </template>
          </a-table-column>
          <a-table-column title="注册时间" :width="170">
            <template #cell="{ record }">{{ fmt(record.createdAt) }}</template>
          </a-table-column>
          <a-table-column title="操作" :width="360" fixed="right">
            <template #cell="{ record }">
              <a-space size="mini" style="flex-wrap:wrap">
                <a-switch :model-value="record.status === 1" size="small" @change="(v: any) => toggleStatus(record, !!v)" />
                <a-button size="mini" @click="openDetail(record)">详情</a-button>
                <a-button size="mini" @click="openEdit(record)">编辑</a-button>
                <a-button size="mini" status="warning" @click="resetPwd(record)">重置密码</a-button>
                <a-button size="mini" type="primary" :icon="IconGift" @click="openRecharge(record)">充值</a-button>
              </a-space>
            </template>
          </a-table-column>
        </template>
      </a-table>
    </a-card>

    <!-- 编辑 / 创建 通用弹窗 -->
    <a-modal v-model:visible="showUser" :title="editing.id ? '编辑用户' : '新建账号'" width="520" :mask-closable="false">
      <a-form :model="editing" layout="vertical" class="user-form">
        <a-form-item label="头像">
          <div class="avatar-row">
            <ImageUpload v-model="editing.avatar" dir="avatar/" round :size="64" />
            <a-input v-model="editing.avatar" placeholder="粘贴头像 URL 或点击左侧上传" allow-clear />
          </div>
          <template #extra>留空则使用 App 默认头像（后台「默认头像」配置）</template>
        </a-form-item>

        <a-row :gutter="14">
          <a-col :span="12">
            <a-form-item label="昵称" :required="!editing.id">
              <a-input v-model="editing.nickname" placeholder="留空自动生成" allow-clear />
            </a-form-item>
          </a-col>
          <!-- 创建：账号 / 手机号 / 邮箱 分开填，三选一（2026-09-25 需求） -->
          <a-col v-if="!editing.id" :span="12">
            <a-form-item label="登录账号（三选一）" required>
              <a-input v-model="editing.account" placeholder="用户名（3-20 位英文/数字/下划线）" allow-clear />
            </a-form-item>
          </a-col>
          <a-col v-else :span="12">
            <a-form-item label="账号">
              <a-input v-model="editing.account" disabled />
            </a-form-item>
          </a-col>
        </a-row>
        <a-form-item v-if="!editing.id" :help="loginTypeError" :class="{ 'field-error': !!loginTypeError }">
          <a-row :gutter="14">
            <a-col :span="12">
              <a-input v-model="editing.phone" placeholder="或手机号" allow-clear />
            </a-col>
            <a-col :span="12">
              <a-input v-model="editing.email" placeholder="或邮箱" allow-clear />
            </a-col>
          </a-row>
          <template #extra>账号 / 手机号 / 邮箱三选一，只填其中一项；填的那项即登录账号</template>
        </a-form-item>

        <a-row :gutter="14">
          <a-col :span="12">
            <a-form-item label="靓号 / 短 ID">
              <a-input v-model="editing.shortId" placeholder="留空自动生成随机靓号" allow-clear />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="密码" :required="!editing.id">
              <a-input-password v-model="editing.password" :placeholder="editing.id ? '留空则不修改密码' : '留空默认 123456'" allow-clear />
            </a-form-item>
          </a-col>
        </a-row>

        <a-form-item label="角色">
          <a-radio-group v-model="editing.role" type="button">
            <a-radio :value="1">普通用户</a-radio>
            <a-radio :value="2">管理员</a-radio>
            <a-radio :value="3">客服</a-radio>
          </a-radio-group>
        </a-form-item>
      </a-form>

      <template #footer>
        <a-space>
          <a-button @click="showUser = false">取消</a-button>
          <a-button type="primary" :loading="saving" @click="saveUser">确定</a-button>
        </a-space>
      </template>
    </a-modal>

    <!-- 批量生成账号弹窗（2026-09-25 需求） -->
    <a-modal v-model:visible="showBatch" title="批量生成账号" width="640" :mask-closable="false" :footer="batchResult.length ? false : undefined" @cancel="showBatch = false">
      <template v-if="!batchResult.length">
        <a-form layout="vertical">
          <a-form-item label="生成数量" required>
            <a-input-number v-model="batchCount" :min="1" :max="500" style="width: 160px" />
          </a-form-item>
          <a-alert type="info">
            按前端一键注册规则生成：g 开头随机账号 + 随机中文昵称 + 自动分配随机靓号 + App 默认头像，密码固定 <b>123456</b>。生成后自动添加小助手/客服、加入默认群聊（与游客注册一致）。
          </a-alert>
        </a-form>
        <div style="margin-top: 16px; text-align: right">
          <a-space>
            <a-button @click="showBatch = false">取消</a-button>
            <a-button type="primary" :loading="batchLoading" @click="doBatchCreate">开始生成</a-button>
          </a-space>
        </div>
      </template>
      <template v-else>
        <a-alert type="success">已生成 {{ batchResult.length }} 个账号（密码统一 123456），请及时复制保存</a-alert>
        <a-table :data="batchResult" :pagination="false" size="small" style="margin-top: 12px" :scroll="{ y: 360 }">
          <template #columns>
            <a-table-column title="账号" :width="150">
              <template #cell="{ record }"><code>{{ record.account }}</code></template>
            </a-table-column>
            <a-table-column title="昵称" :width="130">
              <template #cell="{ record }">{{ record.nickname }}</template>
            </a-table-column>
            <a-table-column title="靓号" :width="110">
              <template #cell="{ record }">#{{ record.shortId }}</template>
            </a-table-column>
            <a-table-column title="密码" :width="90">
              <template #cell>123456</template>
            </a-table-column>
          </template>
        </a-table>
        <div style="margin-top: 14px; text-align: right">
          <a-space>
            <a-button type="outline" @click="copyBatchResult">复制全部（账号 密码）</a-button>
            <a-button type="primary" @click="closeBatch">完成</a-button>
          </a-space>
        </div>
      </template>
    </a-modal>

    <!-- 充值/扣款弹窗 -->
    <a-modal v-model:visible="showRecharge" title="充值 / 扣款（正数=充值，负数=扣款）" width="460" @ok="handleRechargeSubmit" @cancel="showRecharge = false" :confirm-loading="recharging" okText="确认提交" cancelText="取消">
      <a-form layout="vertical">
        <a-form-item label="目标用户">
          <a-input v-model="rechargeTarget.account" disabled />
        </a-form-item>
        <a-form-item label="当前余额">
          <a-input v-model="rechargeTarget.balance" disabled />
        </a-form-item>
        <a-form-item label="变动金额（支持负数）" required field="amount">
          <a-input-number ref="rechargeInputRef" v-model="rechargeAmount" :min="-999999" :max="999999" :precision="2" :step="10" style="width:100%" placeholder="例：100 充值 / -100 扣款" hide-button />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model="rechargeRemark" placeholder="例：管理员手动充值 / 违规扣款" allow-clear />
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 用户详情弹窗 -->
    <a-modal v-model:visible="showDetail" title="用户详情" :footer="false" width="680" unmount-on-close>
      <a-spin v-if="detailLoading" style="display:block;text-align:center;padding:40px 0" />
      <template v-else-if="detail">
        <div class="detail-head">
          <span class="avatar detail-avatar" :style="{ background: avatarColor(Number(detail.user?.id) || 0) }">
            <img v-if="detail.user?.avatar" :src="detail.user.avatar" alt="" />
            <template v-else>{{ (detail.user?.nickname || detail.user?.account || '?').slice(0, 1).toUpperCase() }}</template>
          </span>
          <div class="detail-head-info">
            <span class="detail-name">{{ detail.user?.nickname || '-' }}</span>
            <span class="detail-sub">{{ detail.user?.account }} · ID {{ detail.user?.id }}</span>
            <div class="detail-tags">
              <a-tag :color="detail.user?.status === 1 ? 'green' : 'red'">{{ detail.user?.status === 1 ? '正常' : '禁用' }}</a-tag>
              <a-tag color="gray">{{ ['未知', '用户', '管理员', '客服'][Number(detail.user?.role) || 0] || '用户' }}</a-tag>
              <a-tag v-if="detail.online" color="green">在线</a-tag>
              <a-tag v-else color="gray">离线</a-tag>
            </div>
          </div>
        </div>
        <a-descriptions :column="2" size="medium" bordered title="基础资料" style="margin-top:14px">
          <a-descriptions-item label="ID">{{ detail.user?.shortId ? '#' + detail.user.shortId : '—' }}</a-descriptions-item>
          <a-descriptions-item label="个性签名">{{ detail.user?.signature || '—' }}</a-descriptions-item>
          <a-descriptions-item label="手机号">{{ detail.user?.phone || '—' }}</a-descriptions-item>
          <a-descriptions-item label="邮箱">{{ detail.user?.email || '—' }}</a-descriptions-item>
          <a-descriptions-item label="我的邀请码">{{ detail.user?.myInviteCode || '—' }}</a-descriptions-item>
          <a-descriptions-item label="注册邀请码">{{ detail.invitedCode || '—' }}</a-descriptions-item>
          <a-descriptions-item label="上级（邀请人）">
            <template v-if="detail.inviter">
              {{ detail.inviter.nickname || detail.inviter.account || '—' }}（{{ detail.inviter.account }} · ID #{{ detail.inviter.shortId || '—' }}）
              <a-tag v-if="(detail.inviteeCount ?? 0) > 0" size="small" color="arcoblue" style="margin-left:6px">已邀请 {{ detail.inviteeCount }} 人</a-tag>
            </template>
            <span v-else>—</span>
          </a-descriptions-item>
          <a-descriptions-item label="注册时间">{{ fmt(detail.user?.createdAt) || '—' }}</a-descriptions-item>
        </a-descriptions>
        <a-descriptions :column="2" size="medium" bordered title="钱包（服务端为准）" style="margin-top:12px">
          <a-descriptions-item label="可用余额">¥ {{ fmtMoney(detail.user?.balance) }}</a-descriptions-item>
          <a-descriptions-item label="冻结金额">¥ {{ fmtMoney(detail.user?.frozen) }}</a-descriptions-item>
          <a-descriptions-item label="累计充值">¥ {{ fmtMoney(detail.totalRecharge) }}</a-descriptions-item>
          <a-descriptions-item label="累计提现">¥ {{ fmtMoney(detail.totalWithdraw) }}</a-descriptions-item>
        </a-descriptions>
        <a-descriptions :column="2" size="medium" bordered title="登录与注册" style="margin-top:12px">
          <a-descriptions-item label="最后登录 IP">{{ detail.user?.lastLoginIP || '—' }}</a-descriptions-item>
          <a-descriptions-item label="最后登录时间">{{ detail.user?.lastLoginAt ? fmt(detail.user.lastLoginAt) : '从未登录' }}</a-descriptions-item>
          <a-descriptions-item label="注册 IP">{{ detail.user?.registerIP || '—' }}</a-descriptions-item>
          <a-descriptions-item label="注册设备">{{ detail.user?.registerDevice || '—' }}</a-descriptions-item>
        </a-descriptions>

        <!-- 登录设备列表 + 信任管理（账户安全验证保底展示） -->
        <div class="device-block" style="margin-top:12px">
          <div class="device-block-head">
            <span class="device-block-title">登录设备（{{ devices.length ? devices.length + ' 台' : '—' }}）</span>
            <a-button size="mini" status="danger" :loading="clearingTrust" @click="clearTrust">清除信任设备</a-button>
          </div>
          <a-table v-if="devices.length" :data="devices" :pagination="false" size="small" :loading="devicesLoading" :scroll="{ x: 640 }">
            <template #columns>
              <a-table-column title="设备" :width="170">
                <template #cell="{ record }">
                  <div class="dev-cell">
                    <span class="dev-name">{{ record.deviceName || deviceTypeLabel(record.deviceType) }}</span>
                    <a-tag v-if="record.isCurrent" size="small" color="arcoblue">本机</a-tag>
                  </div>
                </template>
              </a-table-column>
              <a-table-column title="类型" :width="90">
                <template #cell="{ record }">{{ deviceTypeLabel(record.deviceType) }}</template>
              </a-table-column>
              <a-table-column title="IP" :width="140">
                <template #cell="{ record }">
                  <span v-if="record.lastIp" class="ip-chip"><IconLocation />{{ record.lastIp }}</span>
                  <span v-else class="muted">—</span>
                </template>
              </a-table-column>
              <a-table-column title="最后活跃" :width="150">
                <template #cell="{ record }">
                  <span v-if="record.lastActiveAt">{{ fmt(record.lastActiveAt) }}</span>
                  <span v-else class="muted">—</span>
                </template>
              </a-table-column>
              <a-table-column title="在线" :width="76" align="center">
                <template #cell="{ record }">
                  <a-badge :status="record.online ? 'success' : 'normal'" :text="record.online ? '在线' : '离线'" />
                </template>
              </a-table-column>
              <a-table-column title="信任" :width="90" align="center">
                <template #cell="{ record }">
                  <a-tag v-if="record.trusted" color="green">已信任</a-tag>
                  <a-tag v-else color="red">未信任</a-tag>
                </template>
              </a-table-column>
            </template>
          </a-table>
          <a-empty v-else-if="!devicesLoading" description="暂无登录设备" />
          <div class="device-tip muted">
            清除信任后，该用户下次登录任意设备都需重新通过设备批准验证；不会锁死账号（无可信设备时首登即自动信任）。
          </div>
        </div>
        <a-descriptions :column="2" size="medium" bordered title="统计" style="margin-top:12px">
          <a-descriptions-item label="好友数">{{ detail.friendCount ?? 0 }}</a-descriptions-item>
          <a-descriptions-item label="累计发送消息">{{ detail.msgCount ?? 0 }}</a-descriptions-item>
        </a-descriptions>
      </template>
    </a-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { Message, Modal } from '@arco-design/web-vue'
import { IconLocation, IconGift } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'
import ImageUpload from './ImageUpload.vue'

const AVATAR_COLORS = ['#4E8CFF', '#7B61FF', '#FF7D00', '#00B42A', '#F53F3F', '#14C9C9']
function avatarColor(id: number) {
  return { backgroundColor: AVATAR_COLORS[id % AVATAR_COLORS.length] }
}
function fmt(v: string | number | Date) {
  if (!v) return ''
  const d = new Date(v)
  if (isNaN(+d)) return String(v)
  const p = (n: number) => n.toString().padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function fmtMoney(v: any) {
  const n = Number(v) || 0
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}
// 设备类型 → 中文标签（与后端 DeviceSession.deviceType 映射一致）
const DEVICE_TYPE_LABELS: Record<number, string> = { 1: 'Android', 2: 'iOS', 3: 'Web', 4: 'Windows', 5: 'macOS' }
function deviceTypeLabel(t: any) {
  return DEVICE_TYPE_LABELS[Number(t)] || '未知设备'
}

const list = ref<Array<Record<string, any>>>([])
const loading = ref(false)
const query = reactive({ kw: '', status: undefined as number | undefined, role: 0 as number | undefined, inviteCode: '' })
const pagination = reactive({ current: 1, pageSize: 10, total: 0, showTotal: true })

const showUser = ref(false)
const saving = ref(false)
const editing = reactive<{
  id?: number; account: string; phone: string; email: string; password: string; nickname: string;
  avatar: string; role: number; shortId: string
}>({ account: '', phone: '', email: '', password: '', nickname: '', avatar: '', role: 1, shortId: '' })

// 三选一校验：账号 / 手机号 / 邮箱至少填一项且只填一项（与后端口径一致）
const loginTypeError = computed(() => {
  if (editing.id) return ''
  const filled = [editing.account.trim(), editing.phone.trim(), editing.email.trim()].filter(Boolean).length
  if (filled === 0) return '账号 / 手机号 / 邮箱至少填写一项'
  if (filled > 1) return '三选一，只能填写其中一项'
  return ''
})

// ===== 批量生成账号（2026-09-25 需求） =====
const showBatch = ref(false)
const batchLoading = ref(false)
const batchCount = ref<number>(10)
const batchResult = ref<Array<Record<string, string>>>([])

function openBatch() {
  batchCount.value = 10
  batchResult.value = []
  showBatch.value = true
}

async function doBatchCreate() {
  const n = Number(batchCount.value) || 0
  if (n < 1 || n > 500) return Message.error('生成数量需为 1-500')
  batchLoading.value = true
  try {
    const { data } = await adminApi.userBatchCreate(n)
    if (data?.code === 0) {
      batchResult.value = (data.data?.list || []) as Array<Record<string, string>>
      Message.success(`已生成 ${batchResult.value.length} 个账号`)
    } else {
      Message.error(data?.message || '生成失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '生成失败（网络错误）')
  } finally {
    batchLoading.value = false
  }
}

async function copyBatchResult() {
  const text = batchResult.value.map((r) => `${r.account} 123456`).join('\n')
  try {
    await navigator.clipboard.writeText(text)
    Message.success(`已复制 ${batchResult.value.length} 个账号（每行：账号 密码）`)
  } catch {
    Message.error('复制失败，请手动从列表复制')
  }
}

function closeBatch() {
  showBatch.value = false
  load(1)
}

const showRecharge = ref(false)
const recharging = ref(false)
const rechargeTarget = reactive({ account: '', balance: '' })
const rechargeRecord = ref<Record<string, any> | null>(null)
const rechargeAmount = ref<number>(100)
const rechargeRemark = ref('')
const rechargeInputRef = ref<any>(null)
function commitAmount() {
  // 强制让 a-input-number 失焦 → 触发内部 precision/format，提交到 v-model
  try {
    const ae = document.activeElement as HTMLElement | null
    if (ae && (ae.tagName === 'INPUT' || ae.closest?.('.arco-input-number'))) {
      ae.blur()
    }
  } catch { /* ignore */ }
  // 从组件 ref 兜底读取（部分 Arco 版本暴露 inputValue）
  try {
    const el = rechargeInputRef.value
    if (el && typeof el.$el === 'object') {
      const input = el.$el.querySelector?.('input')
      if (input && typeof input.value === 'string' && input.value !== '') {
        const v = Number(input.value.replace(/,/g, ''))
        if (isFinite(v)) rechargeAmount.value = v
      }
    }
  } catch { /* ignore */ }
}
function handleRechargeSubmit() {
  commitAmount()
  // 再给一个微任务，确保 v-model 完成
  Promise.resolve().then(() => doRecharge())
}

onMounted(() => load(1))

// ===== 用户详情（查看详情） =====
const showDetail = ref(false)
const detailLoading = ref(false)
const detail = ref<Record<string, any> | null>(null)
// 登录设备列表（账户安全验证保底展示：含 IP + 信任状态 + 清除信任按钮）
const devices = ref<Array<Record<string, any>>>([])
const devicesLoading = ref(false)
const clearingTrust = ref(false)

async function openDetail(r: Record<string, any>) {
  showDetail.value = true
  detailLoading.value = true
  detail.value = null
  devices.value = []
  try {
    const { data } = await adminApi.userDetail(r.id)
    if (data.code === 0) {
      detail.value = data.data
      // 详情与设备列表并行加载，互不影响；设备列表失败不阻塞详情展示
      loadDevices(r.id)
    } else {
      Message.error(data.message || '读取详情失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '读取详情失败（网络错误）')
  } finally {
    detailLoading.value = false
  }
}

// 拉取该用户最近登录设备列表（含 IP / 信任状态）
async function loadDevices(uid: number | string) {
  devicesLoading.value = true
  try {
    const { data } = await adminApi.userDevices(uid)
    if (data.code === 0) {
      devices.value = (data.data?.devices || []) as Array<Record<string, any>>
    } else {
      devices.value = []
    }
  } catch (e: any) {
    devices.value = []
  } finally {
    devicesLoading.value = false
  }
}

// 清除该用户全部设备的信任标记：强制下次登录重新验证，不会锁死账号
async function clearTrust() {
  if (!detail.value?.user?.id) return
  const name = detail.value.user?.nickname || detail.value.user?.account || '该用户'
  const ok = await new Promise<boolean>((r) => {
    Modal.confirm({
      title: `清除 ${name} 的信任设备`,
      content: '将清除该用户所有登录设备的信任标记，下次登录任意设备都需重新通过设备批准验证。不会锁死账号（无可信设备时首登即自动信任）。',
      okText: '确认清除',
      okButtonProps: { status: 'danger' },
      cancelText: '取消',
      onOk: () => r(true),
      onCancel: () => r(false)
    })
  })
  if (!ok) return
  clearingTrust.value = true
  try {
    const { data } = await adminApi.clearTrust(detail.value.user.id)
    if (data && data.code === 0) {
      Message.success(`已清除 ${data.data?.cleared ?? 0} 台设备的信任标记`)
      await loadDevices(detail.value.user.id)
    } else {
      Message.error(data?.message || '清除失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '清除失败（网络错误）')
  } finally {
    clearingTrust.value = false
  }
}

// 角色「全部」= 0/undefined，统一归一成 undefined，避免把 role=0 发给后端
function resetQuery() {
  query.kw = ''
  query.status = undefined
  query.role = 0
  query.inviteCode = ''
  load(1)
}

async function load(page = pagination.current) {
  loading.value = true
  try {
    const { data } = await adminApi.users({
      ...query,
      role: query.role || undefined,
      // 空串归一成 undefined，不把 inviteCode: '' 发给后端（与 role 处理方式一致）
      inviteCode: query.inviteCode?.trim() || undefined,
      page,
      size: pagination.pageSize
    })
    if (data.code === 0) {
      list.value = (data.data?.list || []) as Array<Record<string, any>>
      pagination.total = data.data?.total ?? 0
      pagination.current = page
      return
    }
    Message.error(data?.message || '加载失败')
    list.value = []
    pagination.total = 0
  } catch (e: any) {
    Message.error(e?.message || '加载失败（网络错误）')
    list.value = []
    pagination.total = 0
  } finally {
    loading.value = false
  }
}

// ===== 注意：
// 1. 后台列表只以真实接口为准，不再用本地 meta 覆盖数据库值。
//    之前的 fallback 会把 meta 中的 nickname/avatar/shortId/status/role 盖掉真实接口返回，
//    导致 App 端改了资料后台看不到、后台改了资料 App 端也不生效（因为根本没写库）。
// 2. 不再生成 mock 用户；接口失败就显示空列表并报错。
// 3. 财务流水也只以 /admin/finances 和 wallet_transaction 为准，不再本地 appendFinanceRecord。


function openCreate() {
  Object.assign(editing, { id: undefined, account: '', phone: '', email: '', password: '123456', nickname: '', avatar: '', role: 1, shortId: '' })
  showUser.value = true
}
function openEdit(r: Record<string, any>) {
  Object.assign(editing, {
    id: r.id, account: r.account || '', phone: '', email: '', password: '',
    nickname: r.nickname || '', avatar: r.avatar || '',
    role: r.role ?? 1, shortId: r.shortId ? String(r.shortId) : ''
  })
  showUser.value = true
}

async function saveUser() {
  saving.value = true
  try {
    if (!editing.id) {
      // 创建：账号/手机号/邮箱三选一（后端同样校验）
      if (loginTypeError.value) return Message.error(loginTypeError.value)
      if (!editing.password) return Message.error('请填写初始密码（留空可默认 123456）')
      const payload = {
        account: editing.account.trim() || undefined,
        phone: editing.phone.trim() || undefined,
        email: editing.email.trim() || undefined,
        password: editing.password,
        nickname: editing.nickname || undefined,
        avatar: editing.avatar || undefined,
        role: editing.role,
        shortId: editing.shortId || undefined
      }
      const { data } = await adminApi.userCreate(payload)
      if (!data || data.code !== 0) {
        return Message.error(data?.message || '创建失败，请检查账号是否已存在')
      }
      Message.success('创建成功')
    } else {
      // 更新
      const payload: any = {}
      if (editing.nickname !== undefined) payload.nickname = editing.nickname
      if (editing.avatar !== undefined) payload.avatar = editing.avatar
      if (editing.role !== undefined) payload.role = editing.role
      // 空字符串表示清空 shortId（置 null）；有值就原样传递；未填写不更新
      if (editing.shortId !== '') payload.shortId = editing.shortId || null
      // 并行：更新资料 + （若填了密码则改密）
      const tasks: Promise<any>[] = [adminApi.userUpdate(editing.id, payload)]
      if (editing.password) tasks.push(adminApi.userResetPwd(editing.id, editing.password))
      const results = await Promise.all(tasks)
      const failed = results.find((r) => r.data && r.data.code !== 0)
      if (failed) {
        return Message.error(failed.data.message || '保存失败')
      }
      Message.success('已保存')
    }
    showUser.value = false
    load(1)
  } catch (e: any) {
    Message.error(e?.message || '保存失败（网络错误）')
  } finally {
    saving.value = false
  }
}

async function toggleStatus(record: Record<string, any>, enabled: boolean) {
  try {
    const { data } = await adminApi.userStatus(record.id, enabled ? 1 : 2)
    if (data && data.code === 0) {
      record.status = enabled ? 1 : 2
      Message.success(enabled ? '已启用' : '已禁用')
      return
    }
    Message.error(data?.message || (enabled ? '启用失败' : '禁用失败'))
  } catch (e: any) {
    Message.error(e?.message || (enabled ? '启用失败' : '禁用失败'))
  } finally {
    // 无论成功失败都刷新一次列表（避免本地显示和 DB 不一致）
    load(pagination.current)
  }
}

async function resetPwd(record: Record<string, any>) {
  const name = record.nickname || record.account || '该用户'
  const ok = await new Promise<boolean>((r) => {
    Modal.confirm({
      title: `重置 ${name} 的密码`,
      content: `将把密码重置为默认值 123456。请在用户登录后提示其修改。`,
      okText: '重置为 123456',
      cancelText: '取消',
      onOk: () => r(true),
      onCancel: () => r(false)
    })
  })
  if (!ok) return
  try {
    const { data } = await adminApi.userResetPwd(record.id, '123456')
    if (data && data.code === 0) {
      Message.success(`已重置为 123456`)
    } else {
      Message.error(data?.message || '重置失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '重置失败（网络错误）')
  }
}

// 充值
async function openRecharge(r: Record<string, any>) {
  rechargeRecord.value = r
  rechargeTarget.account = `${r.nickname || ''} (${r.account})`
  try {
    const { data } = await adminApi.userWallet(r.id)
    if (data.code === 0) {
      const bal = data.data?.balance
      rechargeTarget.balance = `¥ ${fmtMoney(bal ?? 0)}`
    } else {
      rechargeTarget.balance = `¥ ${fmtMoney(r.balance ?? 0)}`
      Message.warning(data?.message || '读取钱包失败，显示列表余额')
    }
  } catch (e: any) {
    rechargeTarget.balance = `¥ ${fmtMoney(r.balance ?? 0)}`
    Message.warning(e?.message || '读取钱包失败，显示列表余额')
  }
  rechargeAmount.value = 100
  rechargeRemark.value = '管理员手动充值'
  showRecharge.value = true
}
// 充值 / 扣款（B-24）
//
// 以前这里的写法有个致命问题：/admin/users/:id/recharge 后端**根本没注册**，
// 请求必然失败，然后被下面的 try/catch 吞掉，转而走「本地记账 fallback」——
// 把金额写进用户 meta 并追加一条前端自造的财务记录，界面提示"充值成功"。
// 结果：后台看着钱加上了，user.balance 列纹丝不动，App 端 `/wallet/me`
// 读的是真实余额，于是永远是老数字；财务页也多出一条对不上的假账。
//
// 现在：充值/扣款一律走服务端原子入账（写 user.balance + wallet_transaction），
// 失败就**明确报错**，绝不静默 fallback。余额以服务端返回值为准。
// 负数（如 -100）代表扣款；余额不足时后端直接返回 4101「余额不足」。
async function doRecharge() {
  if (!rechargeRecord.value) return
  commitAmount()
  if (!rechargeAmount.value || Number(rechargeAmount.value) === 0) return Message.error('请输入有效金额（正数=充值 / 负数=扣款，0 无效）')
  const amt = Number(rechargeAmount.value) || 0
  const isRecharge = amt > 0
  recharging.value = true
  try {
    const r = rechargeRecord.value
    const { data } = await adminApi.userRecharge(r.id, amt, rechargeRemark.value || undefined)
    if (!data || data.code !== 0) {
      // 不再 fallback：失败必须让管理员看见，否则又是一笔"后台说加了、用户没收到"的糊涂账
      const tip = isRecharge ? '充值失败，请稍后重试' : '扣款失败，请稍后重试'
      return Message.error(data?.message || tip)
    }
    // 余额一律以服务端为准（WalletApply 返回的是落库后的真实值）
    const next = Number(data.data?.balance ?? NaN)
    if (!isNaN(next) && isFinite(next)) {
      r.balance = next
      if (r.wallet) r.wallet.balance = next
    }
    const action = isRecharge ? `已充值 ¥${fmtMoney(amt)}` : `已扣款 ¥${fmtMoney(-amt)}`
    Message.success(`${action}，当前余额 ¥${fmtMoney(isNaN(next) ? 0 : next)}`)
    showRecharge.value = false
    load(1)
  } catch (e: any) {
    const tip = isRecharge ? '充值失败，请检查网络' : '扣款失败，请检查网络'
    Message.error(e?.message || tip)
  } finally {
    recharging.value = false
  }
}
</script>

<style scoped>
.toolbar { display: flex; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.muted { color: var(--app-text-3); }

.user-cell { display: flex; align-items: center; gap: 10px; }
.avatar {
  width: 36px; height: 36px;
  border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  color: #fff; font-size: 14px; font-weight: 600;
  overflow: hidden; flex-shrink: 0;
}
.avatar img { width: 100%; height: 100%; object-fit: cover; }
.user-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.nickname { font-size: var(--app-font-size-base); color: var(--app-text-1); font-weight: var(--app-font-weight-medium); }
.account { font-size: var(--app-font-size-xs); color: var(--app-text-3); }

.short-id {
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 13px;
  color: var(--app-primary);
  background: var(--app-primary-bg);
  border-radius: var(--app-radius-xs);
  padding: 2px 8px;
  font-weight: 600;
}

.ip-chip {
  display: inline-flex; align-items: center; gap: 4px;
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 12px;
  color: var(--app-text-2);
  background: var(--app-border-2);
  border-radius: var(--app-radius-xs);
  padding: 2px 8px;
}
.ip-chip :deep(svg) { width: 12px; height: 12px; color: var(--app-primary); }

.balance {
  display: inline-flex; align-items: center; gap: 4px;
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-weight: 600; color: #c73110;
}
.balance :deep(svg) { width: 14px; height: 14px; color: #d48806; }
.hint { margin-top: 6px; font-size: 12px; }

/* 编辑 / 创建用户弹窗 */
.user-form { padding-top: 4px; }
.user-form :deep(.arco-form-item) { margin-bottom: 18px; }
.user-form :deep(.arco-form-item:last-child) { margin-bottom: 4px; }
.user-form :deep(.field-error) { color: #f53f3f; }
.avatar-row { display: flex; align-items: flex-start; gap: 16px; width: 100%; }
.avatar-main { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.avatar-main .hint { margin-top: 6px; line-height: 1.6; }

/* 用户详情弹窗 */
.detail-head { display: flex; align-items: center; gap: 14px; }
.detail-avatar { width: 56px; height: 56px; font-size: 20px; }
.detail-head-info { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.detail-name { font-size: 16px; font-weight: var(--app-font-weight-medium); color: var(--app-text-1); }
.detail-sub { font-size: 12px; color: var(--app-text-3); }
.detail-tags { display: flex; gap: 6px; }

/* 登录设备列表（账户安全验证） */
.device-block { }
.device-block-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 10px; }
.device-block-title { font-size: 14px; font-weight: var(--app-font-weight-medium); color: var(--app-text-1); }
.dev-cell { display: flex; align-items: center; gap: 6px; min-width: 0; }
.dev-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 130px; }
.device-tip { margin-top: 8px; font-size: 12px; line-height: 1.6; }
</style>
