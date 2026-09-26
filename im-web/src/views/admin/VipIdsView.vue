<template>
  <div class="vip-page">
    <a-card>
      <div class="toolbar">
        <a-input-search
          v-model="query.kw"
          placeholder="搜索靓号 / 备注 / 使用者"
          style="width: 240px"
          allow-clear
          @search="load(1)"
        />
        <a-select v-model="query.status" placeholder="状态" style="width: 120px" allow-clear @change="load(1)">
          <a-option :value="1">未分配</a-option>
          <a-option :value="2">已冻结</a-option>
          <a-option :value="3">已分配</a-option>
        </a-select>
        <a-button type="primary" @click="showGen = true">
          <template #icon><IconPlus /></template>批量生成
        </a-button>
        <a-button @click="exportCsv">
          <template #icon><IconExport /></template>导出 CSV
        </a-button>
      </div>

      <a-table
        :data="list"
        row-key="id"
        :loading="loading"
        :pagination="pagination"
        @page-change="load"
      >
        <template #columns>
          <a-table-column title="靓号" :width="160">
            <template #cell="{ record }">
              <span class="vip-id" :class="statusClass(record.status)">
                <IconTrophy />
                {{ record.shortId }}
              </span>
            </template>
          </a-table-column>
          <a-table-column title="类型" :width="100">
            <template #cell="{ record }">
              <a-tag :color="typeColor(record.type)">{{ typeMap[record.type] || '普通' }}</a-tag>
            </template>
          </a-table-column>
          <a-table-column title="状态" :width="100">
            <template #cell="{ record }">
              <a-tag :color="statusColor(record.status)">{{ statusMap[record.status] || '未知' }}</a-tag>
            </template>
          </a-table-column>
          <a-table-column title="绑定账号" :width="180">
            <template #cell="{ record }">
              <span v-if="record.userAccount">{{ record.userAccount }}</span>
              <span v-else-if="record.userNickname" class="muted">{{ record.userNickname }}</span>
              <span v-else-if="record.usedBy > 0" class="muted">UID:{{ record.usedBy }}</span>
              <span v-else class="muted">—</span>
            </template>
          </a-table-column>
          <a-table-column title="备注" data-index="remark" ellipsis />
          <a-table-column title="创建时间" :width="170">
            <template #cell="{ record }">{{ fmt(record.createdAt) }}</template>
          </a-table-column>
          <a-table-column title="分配时间" :width="170">
            <template #cell="{ record }">
              <span v-if="record.usedAt">{{ fmt(record.usedAt) }}</span>
              <span v-else class="muted">—</span>
            </template>
          </a-table-column>
          <a-table-column title="操作" :width="260">
            <template #cell="{ record }">
              <a-button size="mini" @click="openRemark(record)">备注</a-button>
              <!-- 未分配：冻结 / 分配 / 删除 -->
              <template v-if="record.status === 1">
                <a-popconfirm content="确认冻结？冻结后不可再分配" @ok="setFrozen(record, true)">
                  <a-button size="mini" style="margin-left: 6px">冻结</a-button>
                </a-popconfirm>
                <a-button size="mini" type="outline" style="margin-left: 6px" @click="openAssign(record)">分配</a-button>
                <a-popconfirm content="确认删除？" @ok="del(record)">
                  <a-button size="mini" status="danger" style="margin-left: 6px">删除</a-button>
                </a-popconfirm>
              </template>
              <!-- 冻结：解冻 / 删除 -->
              <template v-else-if="record.status === 2">
                <a-popconfirm content="确认解冻？" @ok="setFrozen(record, false)">
                  <a-button size="mini" type="outline" style="margin-left: 6px">解冻</a-button>
                </a-popconfirm>
                <a-popconfirm content="确认删除？" @ok="del(record)">
                  <a-button size="mini" status="danger" style="margin-left: 6px">删除</a-button>
                </a-popconfirm>
              </template>
              <!-- 已分配：解除绑定，不允许冻结/删除 -->
              <template v-else-if="record.status === 3">
                <a-popconfirm content="确认解除绑定？该用户的ID会被清空，靓号回收为未分配" @ok="doRelieve(record)">
                  <a-button size="mini" status="warning" style="margin-left: 6px">解除</a-button>
                </a-popconfirm>
              </template>
            </template>
          </a-table-column>
        </template>
      </a-table>
    </a-card>

    <!-- 批量生成弹窗 -->
    <a-modal v-model:visible="showGen" title="批量生成靓号" @ok="doGen" :confirm-loading="genLoading" width="520">
      <a-form :model="gen" layout="vertical">
        <a-form-item label="生成方式">
          <a-radio-group v-model="gen.mode">
            <a-radio value="range">范围区间</a-radio>
            <a-radio value="manual">手动输入</a-radio>
            <a-radio value="rule">按规则生成</a-radio>
          </a-radio-group>
        </a-form-item>
        <template v-if="gen.mode === 'range'">
          <a-form-item label="前缀（可选）">
            <a-input v-model="gen.prefix" placeholder="如 86，留空则不加前缀" />
          </a-form-item>
          <a-form-item label="起始数字">
            <a-input v-model="gen.start" placeholder="如 10000" />
          </a-form-item>
          <a-form-item label="结束数字">
            <a-input v-model="gen.end" placeholder="如 10099" />
          </a-form-item>
        </template>
        <template v-else-if="gen.mode === 'manual'">
          <a-form-item label="靓号列表（每行一个，或用英文逗号分隔）">
            <a-textarea v-model="gen.list" :rows="6" placeholder="8600000001&#10;8600000088&#10;8600000888" />
          </a-form-item>
        </template>
        <template v-else-if="gen.mode === 'rule'">
          <a-form-item label="前缀（可选）">
            <a-input v-model="gen.prefix" placeholder="如 86" />
          </a-form-item>
          <a-form-item label="规则类型">
              <a-radio-group v-model="gen.rule" @change="onRuleChange">
                <a-radio value="AAAAA">豹子号（AAAAA）</a-radio>
                <a-radio value="ABCDEF">顺子号（递增）</a-radio>
                <a-radio value="FEDCBA">倒顺号（递减）</a-radio>
                <a-radio value="ABABAB">交替号（ABABAB）</a-radio>
                <a-radio value="AABBAA">AABBAA 对称号</a-radio>
                <a-radio value="ABBA">ABBA 回文号</a-radio>
              </a-radio-group>
            </a-form-item>
          <div class="rule-grid">
            <a-form-item label="号码位数">
              <a-input-number v-model="gen.ruleLen" :min="4" :max="11" :step="1" style="width:100%" @change="() => gen.preview = buildRulePreview()" />
            </a-form-item>
            <a-form-item label="起始数字 A">
              <a-input-number v-model="gen.ruleFrom" :min="0" :max="9" :step="1" style="width:100%" @change="() => gen.preview = buildRulePreview()" />
            </a-form-item>
            <a-form-item label="结束数字 A">
              <a-input-number v-model="gen.ruleTo" :min="0" :max="9" :step="1" style="width:100%" @change="() => gen.preview = buildRulePreview()" />
            </a-form-item>
            <a-form-item label="起始数字 B">
              <a-input-number v-model="gen.ruleFrom2" :min="0" :max="9" :step="1" style="width:100%" @change="() => gen.preview = buildRulePreview()" />
            </a-form-item>
            <a-form-item label="结束数字 B">
              <a-input-number v-model="gen.ruleTo2" :min="0" :max="9" :step="1" style="width:100%" @change="() => gen.preview = buildRulePreview()" />
            </a-form-item>
          </div>
          <a-form-item label="预览">
            <div class="rule-preview">
              <template v-if="gen.preview.length">
                <span v-for="n in gen.preview" :key="n" class="preview-chip">
                  <IconTrophy /> {{ gen.prefix }}{{ n }}
                </span>
                <span v-if="gen.preview.length === 8" class="preview-more">…（共 {{ rulePreviewCount }} 组）</span>
              </template>
              <span v-else class="muted">请选择规则与参数</span>
            </div>
          </a-form-item>
        </template>
        <a-form-item label="类型">
          <a-select v-model="gen.type" style="width: 100%">
            <a-option :value="1">普通靓号</a-option>
            <a-option :value="2">豹子号</a-option>
            <a-option :value="3">顺子号</a-option>
            <a-option :value="4">VIP 专属</a-option>
          </a-select>
        </a-form-item>
        <a-form-item label="初始备注">
          <a-input v-model="gen.remark" placeholder="可选" />
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 备注弹窗 -->
    <a-modal v-model:visible="showRemarkEdit" title="编辑靓号信息" @ok="saveRemark">
      <a-form :model="{ type: typeValue, remark: remarkValue }" layout="vertical">
        <a-form-item label="类型">
          <a-select v-model="typeValue" style="width:100%">
            <a-option :value="1">普通靓号</a-option>
            <a-option :value="2">豹子号</a-option>
            <a-option :value="3">顺子号</a-option>
            <a-option :value="4">VIP 专属</a-option>
          </a-select>
        </a-form-item>
        <a-form-item label="备注">
          <a-textarea v-model="remarkValue" :rows="3" placeholder="描述这个靓号的用途或归属" />
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 分配弹窗：搜索用户 -->
    <a-modal v-model:visible="showAssign" title="给靓号分配用户" @ok="saveAssign"
             :ok-loading="assignLoading" width="680">
      <div style="margin-bottom:10px">
        将靓号 <b>{{ assignTarget?.shortId }}</b> 分配给指定用户；若该用户原本占用其他靓号，旧靓号会自动回收。
      </div>
      <div class="toolbar" style="margin-bottom: 12px">
        <a-input-search
          v-model="assignKw"
          placeholder="搜索用户 昵称/账号/手机号/ID"
          style="width: 380px"
          allow-clear
          @search="searchUsers(1)"
        />
        <a-button @click="searchUsers(1)">搜索</a-button>
      </div>
      <a-table
        :data="assignUserList"
        :loading="assignLoading"
        :pagination="{ current: assignPage, pageSize: 8, total: assignTotal, showTotal: true, simple: true }"
        row-key="id"
        :row-selection="{
          type: 'radio',
          selectedRowKeys: assignSelected ? [String(assignSelected.id)] : []
        }"
        @page-change="searchUsers"
        @select="(_keys, _rowKey, record) => assignSelected = record as any"
        @row-click="(_, record) => {
          assignSelected = record as any
          // 手动同步单选：通过 @select 走的会自动选；这里点行外区域也给回填
        }"
      >
        <template #columns>
          <a-table-column title="ID" data-index="id" :width="90" />
          <a-table-column title="账号" :width="160">
            <template #cell="{ record }">{{ record.account || '—' }}</template>
          </a-table-column>
          <a-table-column title="昵称" :width="160">
            <template #cell="{ record }">{{ record.nickname || '—' }}</template>
          </a-table-column>
          <a-table-column title="手机号" :width="140">
            <template #cell="{ record }">{{ record.phone || '—' }}</template>
          </a-table-column>
          <a-table-column title="现有ID" :width="140">
            <template #cell="{ record }">
              <span class="vip-id is-free" style="padding:2px 6px;font-size:12px" v-if="record.shortId">{{ record.shortId }}</span>
              <span v-else class="muted">（空）将被分配</span>
            </template>
          </a-table-column>
        </template>
      </a-table>
    </a-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, markRaw, computed, watch } from 'vue'
import { Message } from '@arco-design/web-vue'
import { IconPlus, IconExport, IconTrophy } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'

const typeMap: Record<number, string> = { 1: '普通', 2: '豹子号', 3: '顺子号', 4: 'VIP' }
function typeColor(t: number) {
  return ({ 1: 'gray', 2: 'orange', 3: 'purple', 4: 'gold' } as Record<number, string>)[t] || 'gray'
}
// 与后端 model.ReservedShortID 常量保持一致：
//   ReservedStatusFree=1 未分配, ReservedStatusFrozen=2 冻结, ReservedStatusUsed=3 已用
const statusMap: Record<number, string> = { 1: '未分配', 2: '已冻结', 3: '已分配' }
function statusColor(s: number) {
  return ({ 1: 'green', 2: 'red', 3: 'arcoblue' } as Record<number, string>)[s] || 'gray'
}
function statusClass(s: number) {
  return ({ 1: 'is-free', 2: 'is-frozen', 3: 'is-used' } as Record<number, string>)[s] || ''
}

function fmt(v: string | number | Date) {
  if (!v) return ''
  const d = new Date(v)
  if (isNaN(+d)) return String(v)
  const p = (n: number) => n.toString().padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

const list = ref<Array<Record<string, any>>>([])
const loading = ref(false)
const pagination = reactive({ current: 1, pageSize: 15, total: 0, showTotal: true })
const query = reactive({ kw: '', status: undefined as number | undefined })

// 生成弹窗
const showGen = ref(false)
const genLoading = ref(false)
const gen = reactive({
  mode: 'range' as 'range' | 'manual' | 'rule',
  prefix: '',
  start: '',
  end: '',
  list: '',
  rule: 'AAAAA' as string,
  ruleLen: 5,
  ruleFrom: 0,
  ruleTo: 9,
  ruleFrom2: 0,
  ruleTo2: 9,
  preview: [] as string[],
  type: 1,
  remark: ''
})

// 备注弹窗
const showRemarkEdit = ref(false)
const remarkValue = ref('')
const typeValue = ref<number>(1)
const remarkTarget = ref<Record<string, any> | null>(null)

// 分配弹窗
const showAssign = ref(false)
const assignLoading = ref(false)
const assignKw = ref('')
const assignPage = ref(1)
const assignTotal = ref(0)
const assignUserList = ref<Array<Record<string, any>>>([])
const assignSelected = ref<Record<string, any> | null>(null)
const assignTarget = ref<Record<string, any> | null>(null)

onMounted(() => load(1))

async function load(page = pagination.current) {
  loading.value = true
  try {
    // 后端接口：/admin/reserved-short-ids，先尝试，失败则降级到本地解析配置表
    try {
      const { data } = await adminApi.reservedShortIds({ ...query, page, size: pagination.pageSize })
      if (data.code === 0) {
        list.value = data.data.list || []
        pagination.total = data.data.total || 0
        pagination.current = page
        return
      }
    } catch { /* fallback */ }

    // 降级：从 /admin/configs/reserved_short_ids 解析
    // 元信息（类型/备注/创建时间）保存在 reserved_short_ids_meta（JSON 映射 key = shortId）
    const [cfgId, cfgMeta] = await Promise.all([
      adminApi.configGet('reserved_short_ids').catch(() => ({ data: { data: '' } })),
      adminApi.configGet('reserved_short_ids_meta').catch(() => ({ data: { data: '{}' } }))
    ])
    let meta: Record<string, any> = {}
    try {
      meta = cfgMeta.data && cfgMeta.data.data ? JSON.parse(String(cfgMeta.data.data)) : {}
    } catch { meta = {} }
    const ids = String(cfgId.data?.data || '')
      .split(/[,，\n\s]+/)
      .map((s) => s.trim())
      .filter(Boolean)
    const now = Date.now()
    const raw = ids.map((id, idx) => {
      const m = meta[id] || {}
      return {
        id: idx + 1,
        shortId: id,
        type: typeof m.type === 'number' ? m.type : 1,
        status: typeof m.status === 'number' ? m.status : 0,
        account: m.account || '',
        remark: m.remark || '',
        createdAt: m.createdAt || new Date(now - idx * 60000).toISOString(),
        usedAt: m.usedAt || ''
      }
    })
    const filtered = raw.filter((r) => {
      if (query.status !== undefined && r.status !== query.status) return false
      if (query.kw) return r.shortId.includes(query.kw) || (r.remark || '').includes(query.kw) || (r.account || '').includes(query.kw)
      return true
    })
    pagination.total = filtered.length
    pagination.current = page
    list.value = filtered.slice((page - 1) * pagination.pageSize, page * pagination.pageSize)
  } finally {
    loading.value = false
  }
}

async function doGen() {
  // 按模式组装 payload：
  //   range：把 from / to / prefix 直接传后端（由后端生成，避免 1w 条字符串在请求里往返）；
  //          如果有非空前缀，后端 range 模式不支持拼接前缀，所以退回前端生成列表 list
  //   manual / rule：前端已经生成字符串数组，统一用 list 模式送后端
  let payload: any = { mode: gen.mode, type: gen.type, remark: gen.remark || undefined }
  let generated: string[] = []
  if (gen.mode === 'range') {
    const s = Number(gen.start), e = Number(gen.end)
    if (!s || !e || s > e) return Message.error('请填写有效范围')
    if (e - s > 9999) return Message.error('一次最多生成 10000 个')
    if (!gen.prefix) {
      payload.mode = 'range'
      payload.from = s
      payload.to = e
    } else {
      // 有前缀：后端 range 模式不会拼接 prefix，改为前端生成列表走 list 模式
      payload.mode = 'list'
      generated = Array.from({ length: e - s + 1 }, (_, i) => `${gen.prefix}${s + i}`)
      payload.list = generated
    }
  } else if (gen.mode === 'manual') {
    generated = String(gen.list || '')
      .split(/[,，\n\s]+/)
      .map((s) => s.trim())
      .filter(Boolean)
    if (!generated.length) return Message.error('请输入至少一个靓号')
    if (generated.length > 10000) return Message.error('一次最多 10000 个')
    payload.mode = 'list'
    payload.list = generated
  } else {
    generated = buildRuleIds()
    if (!generated.length) return Message.error('参数无效或无结果，请调整规则/位数/起止')
    if (generated.length > 10000) return Message.error('一次最多 10000 个，请缩小范围')
    payload.mode = 'list'
    payload.list = generated
  }

  genLoading.value = true
  try {
    // 后端接口：POST /admin/reserved-short-ids/batch
    try {
      const { data } = await adminApi.reservedShortIdsBatch(payload)
      if (data.code === 0) {
        const added = Number(data.data?.added ?? data.data?.count ?? 0)
        const expect = generated.length > 0
          ? generated.length
          : ((Number(payload.to) && Number(payload.from) && Number(payload.to) >= Number(payload.from))
              ? (Number(payload.to) - Number(payload.from) + 1)
              : 0)
        Message.success(`成功生成 ${added || expect} 个靓号`)
        showGen.value = false
        resetGen()
        load(1)
        return
      }
      Message.error(data.message || '操作失败')
    } catch { /* fallback */ }

    // 降级：写回 reserved_short_ids 配置 + reserved_short_ids_meta（JSON 存类型/备注/创建时间/状态）
    // （只有 list/manual/rule 模式有 generated；range+无前缀 不会走到这里）
    if (gen.mode === 'range' && !gen.prefix) {
      const s = Number(gen.start), e = Number(gen.end)
      generated = Array.from({ length: e - s + 1 }, (_, i) => `${s + i}`)
    }
    if (!generated.length) return Message.error('操作失败且无 fallback 数据')
    const [cfgId, cfgMeta] = await Promise.all([
      adminApi.configGet('reserved_short_ids').catch(() => ({ data: { data: '' } })),
      adminApi.configGet('reserved_short_ids_meta').catch(() => ({ data: { data: '{}' } }))
    ])
    const existing = String(cfgId.data?.data || '').split(/[,，\n\s]+/).filter(Boolean)
    let meta: Record<string, any> = {}
    try { meta = cfgMeta.data && cfgMeta.data.data ? JSON.parse(String(cfgMeta.data.data)) : {} } catch { meta = {} }
    const nowStr = new Date().toISOString()
    const existingSet = new Set(existing)
    generated.forEach((id) => {
      if (!existingSet.has(id)) {
        meta[id] = { type: gen.type, remark: gen.remark || '', status: 1, createdAt: nowStr }
      }
    })
    const merged = Array.from(new Set([...existing, ...generated]))
    const [r1, r2] = await Promise.all([
      adminApi.configSet('reserved_short_ids', merged.join(',')),
      adminApi.configSet('reserved_short_ids_meta', JSON.stringify(meta))
    ])
    const ok = (resp: any) => resp && resp.data && resp.data.code === 0
    if (ok(r1)) {
      Message.success(`成功生成 ${generated.length} 个靓号（兼容模式：写入配置项）`)
      showGen.value = false
      resetGen()
      load(1)
    } else {
      Message.error((r1 && r1.data && r1.data.message) || '操作失败')
    }
    void r2
  } finally {
    genLoading.value = false
  }
}

function resetGen() {
  Object.assign(gen, {
    mode: 'range', prefix: '', start: '', end: '', list: '',
    rule: 'AAAAA', ruleLen: 5, ruleFrom: 0, ruleTo: 9, ruleFrom2: 0, ruleTo2: 9,
    preview: [], type: 1, remark: ''
  })
}

// ===== 规则生成 =====
watch(showGen, (v) => { if (v && gen.mode === 'rule' && !gen.preview.length) onRuleChange() })
watch(() => gen.mode, (m, prev) => {
  if (m === 'rule' && prev !== 'rule') onRuleChange()
})
function onRuleChange() {
  // 规则与类型联动：用户选了某个规则，自动把下方"类型"选择器切到匹配的类型
  const ruleTypeMap: Record<string, number> = {
    AAAAA: 2,            // 豹子号
    ABCDEF: 3,           // 顺子号
    FEDCBA: 3,           // 倒顺号 → 顺子号类型
    ABABAB: 4,           // 交替号 → VIP 专属
    AABBAA: 4,           // 对称号 → VIP 专属
    ABBA: 4              // 回文号 → VIP 专属
  }
  const t = ruleTypeMap[gen.rule]
  if (t != null) gen.type = t
  gen.preview = buildRulePreview()
}
function buildRulePreview(): string[] {
  const ids = buildRuleIds(8)
  return ids.slice(0, 8)
}
const rulePreviewCount = computed(() => buildRuleIds(10000).length)

function buildRuleIds(limit = 10000): string[] {
  const L = gen.ruleLen
  const Afrom = gen.ruleFrom, Ato = gen.ruleTo
  const Bfrom = gen.ruleFrom2, Bto = gen.ruleTo2
  if (L < 4) return []
  const out: string[] = []
  const add = (s: string) => { if (out.length < limit) out.push(`${gen.prefix || ''}${s}`) }

  if (gen.rule === 'AAAAA') {
    // 豹子：A 重复 L 位
    for (let a = Afrom; a <= Ato; a++) {
      add(String(a).repeat(L))
    }
  } else if (gen.rule === 'ABCDEF') {
    // 递增：从 a 开始连续 L 位
    for (let a = Afrom; a + L - 1 <= 9 && a <= Ato; a++) {
      let s = ''
      for (let i = 0; i < L; i++) s += a + i
      add(s)
    }
  } else if (gen.rule === 'FEDCBA') {
    // 递减
    for (let a = Math.max(Afrom, L - 1); a <= Ato && a <= 9; a++) {
      let s = ''
      for (let i = 0; i < L; i++) s += a - i
      add(s)
    }
  } else if (gen.rule === 'ABABAB') {
    for (let a = Afrom; a <= Ato; a++) {
      for (let b = Bfrom; b <= Bto; b++) {
        if (a === b) continue
        let s = ''
        for (let i = 0; i < L; i++) s += i % 2 === 0 ? a : b
        add(s)
      }
    }
  } else if (gen.rule === 'AABBAA') {
    // 以 6 位为基础结构 AABBAA，当 L 不是 6 倍数时按 floor(L/6)*6 或对称扩展
    for (let a = Afrom; a <= Ato; a++) {
      for (let b = Bfrom; b <= Bto; b++) {
        if (a === b) continue
        const base = `${a}${a}${b}${b}${a}${a}`
        // 截取到 L 位
        let s = base
        while (s.length < L) s += a
        s = s.slice(0, L)
        add(s)
      }
    }
  } else if (gen.rule === 'ABBA') {
    for (let a = Afrom; a <= Ato; a++) {
      for (let b = Bfrom; b <= Bto; b++) {
        if (a === b) continue
        // 先写半段 AB...A，然后镜像
        const half = Math.ceil(L / 2)
        const first = new Array(L).fill(0).map((_, i) => {
          // A B A B ...
          if (i === 0) return a
          if (i === L - 1) return a
          if (L === 4 && i === 1) return b
          if (L === 4 && i === 2) return b
          // 一般回文：symmetric[i] = symmetric[L-1-i]
          return i < half ? (i === 0 ? a : b) : 0 // placeholder
        })
        // 做回文
        const arr = new Array(L).fill(0)
        arr[0] = a
        arr[L - 1] = a
        if (L >= 4) { arr[1] = b; arr[L - 2] = b }
        // 中间剩余位用 b
        for (let i = 2; i < L - 2; i++) arr[i] = b
        add(arr.join(''))
      }
    }
  }
  return out
}

function openRemark(r: Record<string, any>) {
  remarkTarget.value = r
  remarkValue.value = r.remark || ''
  typeValue.value = Number(r.type) || 1
  showRemarkEdit.value = true
}

async function saveRemark() {
  const r = remarkTarget.value
  if (!r) return
  try {
    const resp = await adminApi.reservedShortIdRemark(r.id, { remark: remarkValue.value, type: Number(typeValue.value) || 1 })
    if (resp && resp.data && resp.data.code === 0) {
      r.remark = remarkValue.value
      r.type = Number(typeValue.value) || 1
      showRemarkEdit.value = false
      Message.success('已保存')
      await load(pagination.current)  // 刷新，确保 remark/type 与后端一致
      return
    }
    Message.error((resp && resp.data && resp.data.message) || '保存失败')
  } catch {
    // fallback：持久化到 meta
    await patchMeta(r.shortId, { remark: remarkValue.value, type: Number(typeValue.value) || 1 })
    r.remark = remarkValue.value
    r.type = Number(typeValue.value) || 1
    showRemarkEdit.value = false
    Message.success('已保存（本地）')
  }
}

async function setFrozen(r: Record<string, any>, frozen: boolean) {
  try {
    await adminApi.reservedShortIdFrozen(r.id, frozen)
    load(1)
    Message.success(frozen ? '已冻结' : '已解冻')
  } catch {
    // fallback：持久化到 meta
    await patchMeta(r.shortId, { status: frozen ? 2 : 1 })
    r.status = frozen ? 2 : 1
    Message.success(frozen ? '已冻结（本地）' : '已解冻（本地）')
  }
}

async function del(r: Record<string, any>) {
  try {
    await adminApi.reservedShortIdDelete(r.id)
    load(1)
    Message.success('已删除')
  } catch {
    // fallback：从配置字符串里移除 + 同步删除 meta
    const [cfgId, cfgMeta] = await Promise.all([
      adminApi.configGet('reserved_short_ids').catch(() => ({ data: { data: '' } })),
      adminApi.configGet('reserved_short_ids_meta').catch(() => ({ data: { data: '{}' } }))
    ])
    const existing = String(cfgId.data?.data || '').split(/[,，\n\s]+/).filter((s) => s && s !== r.shortId)
    let meta: Record<string, any> = {}
    try { meta = cfgMeta.data && cfgMeta.data.data ? JSON.parse(String(cfgMeta.data.data)) : {} } catch { meta = {} }
    delete meta[r.shortId]
    await Promise.all([
      adminApi.configSet('reserved_short_ids', existing.join(',')),
      adminApi.configSet('reserved_short_ids_meta', JSON.stringify(meta))
    ])
    load(1)
    Message.success('已删除')
  }
}

// ===== 分配 / 解除绑定 =====
function openAssign(r: Record<string, any>) {
  assignTarget.value = r
  assignKw.value = ''
  assignPage.value = 1
  assignTotal.value = 0
  assignUserList.value = []
  assignSelected.value = null
  showAssign.value = true
  // 自动先拉一页，避免用户打开后表格空着没提示
  void searchUsers(1)
}
async function searchUsers(page = assignPage.value) {
  assignLoading.value = true
  try {
    const { data } = await adminApi.users({ kw: assignKw.value || undefined, page, size: 8 })
    if (data.code === 0) {
      assignUserList.value = data.data.list || []
      assignTotal.value = data.data.total || 0
      assignPage.value = page
      assignSelected.value = null
    } else {
      assignUserList.value = []
      assignTotal.value = 0
      Message.error(data.message || '搜索用户失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '搜索用户失败')
  } finally {
    assignLoading.value = false
  }
}
async function saveAssign() {
  const t = assignTarget.value
  const u = assignSelected.value
  if (!t || !u) {
    Message.warning('请先在下方表格中选择目标用户')
    return
  }
  const warn = u.shortId ? `目标用户当前已占用ID ${u.shortId}，分配后旧靓号将自动回收为未分配。是否继续？` : `确认把 ${t.shortId} 分配给 ${u.account || u.nickname || '该用户'}？`
  assignLoading.value = true
  try {
    const resp = await adminApi.reservedShortIdAssign(t.id, String(u.id))
    if (resp?.data?.code === 0) {
      const d = resp.data.data || {}
      Message.success(`已分配成功：${t.shortId} → ${d.account || d.nickname || u.account || u.nickname}`)
      showAssign.value = false
      await load(pagination.current)
    } else {
      Message.error(resp?.data?.message || '分配失败')
    }
  } catch (e: any) {
    Message.warning(`${warn}\n\n错误：${e?.message || '网络异常'}`)
  } finally {
    assignLoading.value = false
  }
}
async function doRelieve(r: Record<string, any>) {
  try {
    const resp = await adminApi.reservedShortIdRelieve(r.id)
    if (resp?.data?.code === 0) {
      Message.success('已解除绑定，靓号回收为未分配，用户的ID已清空')
      await load(pagination.current)
    } else {
      Message.error(resp?.data?.message || '解除失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '解除失败')
  }
}

// 把某条靓号的字段变更写回 meta 配置
async function patchMeta(shortId: string, patch: Record<string, any>) {
  try {
    const cfgMeta = await adminApi.configGet('reserved_short_ids_meta').catch(() => ({ data: { data: '{}' } }))
    let meta: Record<string, any> = {}
    try { meta = cfgMeta.data && cfgMeta.data.data ? JSON.parse(String(cfgMeta.data.data)) : {} } catch { meta = {} }
    meta[shortId] = { ...(meta[shortId] || {}), ...patch }
    await adminApi.configSet('reserved_short_ids_meta', JSON.stringify(meta))
  } catch { /* ignore */ }
}

function exportCsv() {
  const rows = [['靓号', '类型', '状态', '绑定账号', '备注', '创建时间', '分配时间']]
  list.value.forEach((r) => {
    rows.push([
      r.shortId,
      typeMap[r.type] || '-',
      statusMap[r.status] || '-',
      r.userAccount || r.userNickname || (r.usedBy ? `UID:${r.usedBy}` : r.account || '-'),
      (r.remark || '').replace(/[\r\n,]/g, ' '),
      fmt(r.createdAt),
      fmt(r.usedAt) || '-'
    ])
  })
  const csv = '\ufeff' + rows.map((row) => row.map((c) => `"${String(c).replace(/"/g, '""')}"`).join(',')).join('\r\n')
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `reserved-ids-${new Date().toISOString().slice(0, 10)}.csv`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
  Message.success('已导出 CSV')
}

// 显式保留，避免未使用警告
void markRaw
</script>

<style scoped>
.toolbar { display: flex; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.muted { color: var(--app-text-3); }

.vip-id {
  display: inline-flex; align-items: center; gap: 6px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-weight: 600; font-size: 15px;
  padding: 4px 10px;
  border-radius: var(--app-radius-sm);
  background: #fff7e6;
  color: #b8860b;
  border: 1px solid #ffe4b5;
}
.vip-id :deep(svg) { width: 14px; height: 14px; }
.vip-id.is-free { background: #e8ffea; color: #00b42a; border-color: #c9f7cf; }
.vip-id.is-used { background: #e8f3ff; color: #165dff; border-color: #c7d8ff; }
.vip-id.is-frozen { background: #ffece8; color: #f53f3f; border-color: #ffd1c7; }

/* 分配弹窗：行点击选中高亮 */
:deep(.row-selected > td) { background: #e8f3ff !important; }

.rule-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 16px; }
.rule-preview { display: flex; flex-wrap: wrap; gap: 8px; min-height: 36px; align-items: center; }
.preview-chip { display: inline-flex; align-items: center; gap: 4px; padding: 2px 8px; background: var(--app-fill-2, #f5f7fa); border: 1px solid var(--app-border-2, #e5e7eb); border-radius: 4px; font-size: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
.preview-chip :deep(svg) { width: 12px; height: 12px; color: #d48806; }
.preview-more { color: var(--app-text-3); font-size: 12px; }
</style>
