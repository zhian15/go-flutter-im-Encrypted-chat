<template>
  <div class="img-upload" :class="{ round, inline }">
    <!-- 预览 + 点击上传 -->
    <div class="preview" :class="{ round, filled: modelValue }" @click="trigger">
      <img v-if="modelValue" :src="modelValue" alt="preview" />
      <div v-else class="placeholder">
        <IconPlus />
        <span>{{ sizeLabel }}</span>
      </div>
      <div v-if="modelValue" class="mask">
        <IconCamera />
        <span>点击更换</span>
      </div>
      <div v-if="loading" class="loading"><a-spin /></div>
    </div>

    <div class="side">
      <!-- 清空按钮（有图片时显示） -->
      <button v-if="modelValue" type="button" class="clear-btn" @click.stop="clear">
        <IconClose />
        <span>清除</span>
      </button>
      <!-- 提示文字 -->
      <p v-if="hint" class="hint">{{ hint }}</p>
    </div>

    <!-- 隐藏上传触发器（组件私有模板 ref，避免多个 ImageUpload 互相抢 input） -->
    <a-upload
      ref="uploaderRef"
      :show-file-list="false"
      :custom-request="onUpload"
      accept="image/*"
      style="display: none"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick } from 'vue'
import { Message } from '@arco-design/web-vue'
import { IconCamera, IconPlus, IconClose } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'

const props = withDefaults(defineProps<{
  modelValue: string
  dir?: string
  round?: boolean
  hint?: string
  size?: number
  inline?: boolean
}>(), {
  dir: 'common/',
  size: 80
})
const emit = defineEmits<{
  'update:modelValue': [val: string]
}>()

const loading = ref(false)
// 本组件私有：a-upload 组件实例（里面的 <input type=file> 只归这一个 ImageUpload 管）
const uploaderRef = ref<any>(null)
const sizeLabel = computed(() => {
  const s = props.size
  return `${s}×${s}`
})

async function trigger() {
  await nextTick()
  // 优先走 a-upload 暴露的方法；兜底拿组件内部真实 input DOM
  if (uploaderRef.value?.$el) {
    const input: HTMLInputElement | null = uploaderRef.value.$el.querySelector?.('input[type=file]')
    if (input) { input.click(); return }
  }
  // 再兜底：父容器中就近找 input（绝不会跨到兄弟 ImageUpload）
  const rootEl = (uploaderRef.value?.$el?.parentElement as HTMLElement | undefined)
  const input2 = rootEl?.querySelector?.('input[type=file]') as HTMLInputElement | undefined
  input2?.click()
}

function clear() {
  emit('update:modelValue', '')
}

async function onUpload(opt: any) {
  // Arco a-upload custom-request 的几个兼容版本：
  //   Arco 2.54+: opt.fileItem.file 是原生 File；opt.file 是 FileItem 对象（不是 File）
  //   老版：opt.file = 原生 File
  //   另外可能出现 opt.fileOrigin = 原生 File
  const candidates: any[] = [opt?.fileItem?.file, opt?.file?.originFile, opt?.fileOrigin, opt?.file?.file, opt?.file]
  let file: File | null = null
  for (const c of candidates) {
    if (c && typeof window !== 'undefined' && typeof File !== 'undefined' && c instanceof File) {
      file = c as File
      break
    }
    // 兜底：如果 File 构造函数被沙盒替换，至少要求它有 name/size 且 slice/stream 方法存在
    if (c && typeof c === 'object' && typeof c.name === 'string' && typeof c.size === 'number' && (typeof c.slice === 'function' || typeof c.stream === 'function')) {
      file = c as File
      break
    }
  }
  if (!file) {
    Message.error('文件读取失败，请重试或换一张图')
    return
  }
  loading.value = true
  try {
    const fd = new FormData()
    fd.append('file', file, (file as any).name || 'upload.bin')
    fd.append('dir', props.dir || 'common/')
    // 后台页面：走 adminApi.uploadFile（axios）而非裸 fetch —— 拦截器统一带 Authorization
    // 且 401 会自动刷新重试；axios 检测到 FormData 时自动补 multipart boundary，
    // 规避反代环境下 body 被吞导致后端 FormFile("file") 拿不到 → 1001「缺少文件」。
    if (location.pathname.startsWith('/admin')) {
      const uploaded = await adminApi.uploadFile(file, props.dir || 'common/')
      emit('update:modelValue', uploaded)
      Message.success('上传成功')
      return
    }
    // 用户端上传（如充值凭证）继续走 /api/v1/upload
    const token = localStorage.getItem('im-token') || ''
    const resp = await fetch('/api/v1/upload', {
      method: 'POST',
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      body: fd
    })
    const json = await resp.json()
    if (json.code === 0) {
      emit('update:modelValue', json.data.url)
      Message.success('上传成功')
    } else {
      Message.error(json.message || '上传失败')
    }
  } catch (e: any) {
    Message.error('上传失败：' + (e?.message || ''))
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.img-upload {
  display: inline-flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.img-upload.inline {
  flex-direction: row;
  align-items: flex-start;
  align-content: flex-start;
  justify-content: flex-start;
  gap: 16px;
  width: 100%;
}

.preview {
  position: relative;
  width: v-bind('size + "px"');
  height: v-bind('size + "px"');
  min-width: 64px; min-height: 64px;
  border-radius: var(--app-radius-md);
  border: 2px dashed var(--app-border-1);
  background: var(--app-border-2);
  overflow: hidden;
  cursor: pointer;
  flex-shrink: 0;
  display: flex; align-items: center; justify-content: center;
  transition: border-color var(--app-transition-base), background var(--app-transition-base);
}
.preview.round { border-radius: 50%; }
.preview:hover { border-color: var(--app-primary); background: var(--app-primary-bg); }
.preview.filled { border-style: solid; border-color: var(--app-border-1); }
.preview img { width: 100%; height: 100%; object-fit: cover; }
.placeholder { display: flex; flex-direction: column; align-items: center; gap: 3px; color: var(--app-text-3); font-size: 11px; }
.placeholder :deep(svg) { width: 22px; height: 22px; }
.mask {
  position: absolute; inset: 0;
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 2px; font-size: 11px;
  opacity: 0; transition: opacity var(--app-transition-base);
}
.preview:hover .mask { opacity: 1; }
.mask :deep(svg) { width: 16px; height: 16px; }
.loading { position: absolute; inset: 0; background: rgba(255,255,255,0.6); display: flex; align-items: center; justify-content: center; }

.side {
  display: flex; flex-direction: column; align-items: flex-start; gap: 8px;
  min-width: 0; flex: 1; padding-top: 2px;
}
.img-upload:not(.inline) .side { align-items: center; flex: 0; }

.clear-btn {
  display: inline-flex; align-items: center; gap: 3px;
  padding: 3px 10px;
  font-size: var(--app-font-size-xs);
  color: var(--app-text-3);
  background: transparent;
  border: 1px solid var(--app-border-1);
  border-radius: var(--app-radius-sm);
  cursor: pointer;
  transition: color var(--app-transition-base), border-color var(--app-transition-base);
}
.clear-btn:hover { color: var(--color-danger); border-color: var(--color-danger); }
.clear-btn :deep(svg) { width: 12px; height: 12px; }

.hint { margin: 0; font-size: var(--app-font-size-xs); color: var(--app-text-3); }
.img-upload:not(.inline) .hint { white-space: nowrap; text-align: center; }
.img-upload.inline .hint { text-align: left; }
</style>
