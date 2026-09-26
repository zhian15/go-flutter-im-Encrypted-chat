<template>
  <div class="login-wrap">
    <!-- 背景装饰 -->
    <div class="bg-decor">
      <span class="glow glow-1"></span>
      <span class="glow glow-2"></span>
      <span class="grid-overlay"></span>
    </div>

    <div class="login-card">
      <div class="brand">
        <span class="brand-mark" v-html="brandIcon"></span>
        <div class="brand-text">
          <h1>企业IM管理后台</h1>
          <p>Enterprise IM Console</p>
        </div>
      </div>

      <a-form :model="form" layout="vertical" @submit="onLogin" class="form">
        <a-form-item label="账号">
          <a-input v-model="form.account" placeholder="请输入管理员账号" allow-clear>
            <template #prefix><IconUser /></template>
          </a-input>
        </a-form-item>
        <a-form-item label="密码">
          <a-input-password v-model="form.password" placeholder="请输入密码" allow-clear>
            <template #prefix><IconLock /></template>
          </a-input-password>
        </a-form-item>
        <a-form-item label="图形验证码">
          <div class="code-row">
            <a-input v-model="form.captchaCode" placeholder="验证码" maxlength="4">
              <template #prefix><IconSafe /></template>
            </a-input>
            <img v-if="captchaImg" :src="'data:image/png;base64,' + captchaImg" class="captcha-img" @click="loadCaptcha" alt="captcha" />
            <div v-else class="captcha-placeholder" @click="loadCaptcha">点击刷新</div>
          </div>
        </a-form-item>
        <a-button type="primary" long html-type="submit" :loading="loading" class="submit-btn">登录</a-button>
      </a-form>

      <a-alert v-if="error" type="error" :message="error" class="error-alert" />

      <div class="login-footer">
        <button class="lang-btn" @click="toggleLang">{{ currentLang === 'zh-CN' ? 'English' : '中文' }}</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { Message } from '@arco-design/web-vue'
import { IconUser, IconLock, IconSafe } from '@arco-design/web-vue/es/icon'
import { useI18n } from 'vue-i18n'
import { authApi } from '@/api/auth'
import { setLocale } from '@/i18n'

const router = useRouter()
const { locale } = useI18n()
const currentLang = ref(locale.value as string)

const form = ref({ account: '', password: '', captchaCode: '' })
const captchaId = ref('')
const captchaImg = ref('')
const loading = ref(false)
const error = ref('')

const brandIcon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 5h16a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H9l-5 4V6a1 1 0 0 1 1-1z"/><circle cx="9" cy="10.5" r="0.6" fill="currentColor"/><circle cx="12.5" cy="10.5" r="0.6" fill="currentColor"/><circle cx="16" cy="10.5" r="0.6" fill="currentColor"/></svg>`

onMounted(loadCaptcha)

async function loadCaptcha() {
  const { data } = await authApi.captcha()
  captchaId.value = data.data.captchaId
  captchaImg.value = data.data.image
  form.value.captchaCode = ''
}

async function onLogin() {
  error.value = ''
  loading.value = true
  try {
    const { data } = await authApi.login({ account: form.value.account, password: form.value.password, deviceType: 3 })
    if (data.code !== 0) {
      error.value = data.message
      await loadCaptcha()
      return
    }
    const role = (data.data.user as { role?: number })?.role
    if (role !== 2) {
      error.value = '非管理员账号'
      await loadCaptcha()
      return
    }
    localStorage.setItem('im-token', data.data.accessToken)
    localStorage.setItem('im-refresh', data.data.refreshToken)
    Message.success('登录成功')
    router.push('/admin/users')
  } catch {
    error.value = '登录失败'
  } finally {
    loading.value = false
  }
}

function toggleLang() {
  const next = currentLang.value === 'zh-CN' ? 'en-US' : 'zh-CN'
  setLocale(next)
  currentLang.value = next
}
</script>

<style scoped>
.login-wrap {
  position: relative;
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: radial-gradient(circle at 20% 20%, #1f2a3a 0%, #0f1419 60%);
  overflow: hidden;
}

/* 背景装饰 */
.bg-decor { position: absolute; inset: 0; pointer-events: none; }
.glow {
  position: absolute;
  border-radius: 50%;
  filter: blur(80px);
  opacity: 0.5;
}
.glow-1 {
  width: 380px; height: 380px;
  background: #165dff;
  top: -80px; left: -60px;
  animation: float-glow 8s ease-in-out infinite;
}
.glow-2 {
  width: 320px; height: 320px;
  background: #4080ff;
  bottom: -80px; right: -40px;
  animation: float-glow 10s ease-in-out infinite reverse;
}
@keyframes float-glow {
  0%, 100% { transform: translate(0, 0); }
  50% { transform: translate(30px, -30px); }
}
.grid-overlay {
  position: absolute; inset: 0;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.03) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.03) 1px, transparent 1px);
  background-size: 40px 40px;
  mask-image: radial-gradient(circle at center, #000 30%, transparent 75%);
}

/* 登录卡片 */
.login-card {
  position: relative;
  width: 400px;
  background: var(--app-bg-card);
  border-radius: var(--app-radius-lg);
  padding: 36px 32px 28px;
  box-shadow: var(--app-shadow-lg);
  z-index: 1;
  animation: card-in 0.5s var(--app-transition-smooth);
}
@keyframes card-in {
  from { opacity: 0; transform: translateY(14px); }
  to { opacity: 1; transform: translateY(0); }
}

/* 品牌 */
.brand { display: flex; align-items: center; gap: 12px; margin-bottom: 28px; }
.brand-mark {
  width: 44px; height: 44px;
  border-radius: var(--app-radius-md);
  background: linear-gradient(135deg, #165dff, #4080ff);
  display: flex; align-items: center; justify-content: center;
  color: #fff;
  flex-shrink: 0;
  box-shadow: 0 4px 12px rgba(22, 93, 255, 0.4);
}
.brand-mark :deep(svg) { width: 24px; height: 24px; }
.brand-text h1 { margin: 0; font-size: var(--app-font-size-xl); font-weight: var(--app-font-weight-semibold); color: var(--app-text-1); }
.brand-text p { margin: 2px 0 0; font-size: var(--app-font-size-xs); color: var(--app-text-3); letter-spacing: 0.5px; }

.form :deep(.arco-form-item) { margin-bottom: 16px; }
.form :deep(.arco-input-wrapper) { border-radius: var(--app-radius-md); }

.code-row { display: flex; gap: 10px; width: 100%; }
.code-row :deep(.arco-input-wrapper) { flex: 1; }
.captcha-img {
  width: 110px; height: 32px;
  border-radius: var(--app-radius-sm);
  cursor: pointer;
  border: 1px solid var(--app-border-1);
}
.captcha-placeholder {
  width: 110px; height: 32px;
  border-radius: var(--app-radius-sm);
  border: 1px dashed var(--app-border-1);
  display: flex; align-items: center; justify-content: center;
  font-size: var(--app-font-size-xs); color: var(--app-text-3);
  cursor: pointer;
}

.submit-btn {
  height: 40px;
  font-size: var(--app-font-size-base);
  font-weight: var(--app-font-weight-medium);
  border-radius: var(--app-radius-md);
  transition: transform var(--app-transition-base), box-shadow var(--app-transition-base);
}
.submit-btn:hover { transform: translateY(-1px); box-shadow: 0 6px 16px rgba(22, 93, 255, 0.35); }

.error-alert { margin-top: 12px; }

.login-footer { margin-top: 16px; display: flex; justify-content: center; }
.lang-btn {
  background: none; border: none;
  color: var(--app-text-3); font-size: var(--app-font-size-sm);
  cursor: pointer; padding: 4px 8px; border-radius: var(--app-radius-sm);
  transition: color var(--app-transition-base);
}
.lang-btn:hover { color: var(--app-primary); }
</style>
