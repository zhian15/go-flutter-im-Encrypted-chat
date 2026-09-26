<template>
  <!-- 无值：明文录入态（初次填写 / 覆盖重填后录入新值） -->
  <a-input
    v-if="isEmpty"
    :type="visible ? 'text' : 'password'"
    :model-value="real"
    :placeholder="placeholder"
    :allow-clear="allowClear"
    @input="(v: string) => onInput(v)"
    @clear="onClear"
  >
    <template #suffix>
      <span class="secret-ops">
        <IconEye v-if="!visible" class="op" title="显示" @click="visible = true" />
        <IconEyeInvisible v-else class="op" title="隐藏" @click="visible = false" />
      </span>
    </template>
  </a-input>

  <!-- 有值：脱敏展示态（只读，不可编辑；只能「重填」整体覆盖） -->
  <a-input
    v-else
    type="text"
    :model-value="display"
    :placeholder="placeholder"
    readonly
  >
    <template #suffix>
      <span class="secret-ops">
        <IconEye v-if="!visible" class="op" title="显示（中间脱敏）" @click="visible = true" />
        <IconEyeInvisible v-else class="op" title="隐藏" @click="visible = false" />
        <IconRefresh class="op" title="重填（整体覆盖，不修改旧值）" @click="onRefill" />
      </span>
    </template>
  </a-input>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { IconEye, IconEyeInvisible, IconRefresh } from '@arco-design/web-vue/es/icon'

defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{ modelValue?: string; placeholder?: string; allowClear?: boolean }>(),
  { modelValue: '', placeholder: '', allowClear: false }
)
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()

// 纯展示脱敏组件：
// - 有值 => 只读展示，不提供任何「编辑旧值」入口；点「显示」只切到首尾+*** 的脱敏预览，仍不暴露完整密钥
// - 仅「重填」按钮可整体覆盖（清空后由空态录入新值），不存在「修改」语义
const visible = ref(false)

const real = computed<string>({
  get: () => props.modelValue ?? '',
  set: (v) => emit('update:modelValue', v)
})
const isEmpty = computed(() => !real.value)

// 脱敏预览：保留首尾各 2 位，中间固定 ***（不反推真实密钥长度）
function mask(s: string): string {
  if (!s) return ''
  if (s.length <= 2) return '***'
  return s.slice(0, 2) + '***' + s.slice(-2)
}

// 展示值：隐藏态恒为 ***；显示态为脱敏预览；空态为真实录入值
const display = computed(() => {
  if (isEmpty.value) return real.value
  return visible.value ? mask(real.value) : '***'
})

function onInput(v: string) {
  // 仅空态录入时写回真实值
  if (isEmpty.value) real.value = v
}
function onClear() {
  // 空态录入时的清除按钮：直接清空密钥值（覆盖起点）
  emit('update:modelValue', '')
}
function onRefill() {
  // 只能覆盖：清空当前值，回到明文录入态（整体填新值，而非修改旧值）
  visible.value = false
  emit('update:modelValue', '')
}
</script>

<style scoped>
.secret-ops {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.secret-ops .op {
  cursor: pointer;
  color: var(--color-text-3);
  font-size: 16px;
  transition: color 0.15s ease;
}
.secret-ops .op:hover {
  color: rgb(var(--primary-6));
}
</style>
