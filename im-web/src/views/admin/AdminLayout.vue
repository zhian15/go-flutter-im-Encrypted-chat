<template>
  <a-config-provider :locale="arcoLocale">
    <div class="layout">
      <!-- 侧边栏 -->
      <aside class="sider" :class="{ collapsed }">
        <div class="brand">
          <span class="brand-mark" v-html="brandIcon"></span>
          <span v-show="!collapsed" class="brand-name">IM Admin</span>
        </div>

        <nav class="menu">
          <template v-for="g in menuGroups" :key="g.title">
            <!-- 分组标题可点击展开/折叠（侧栏收起时只显示图标，隐藏标题） -->
            <button
              v-show="!collapsed"
              type="button"
              class="menu-group-title"
              :class="{ open: openGroups[g.title] }"
              :title="openGroups[g.title] ? '收起该分组' : '展开该分组'"
              @click="toggleGroup(g.title)"
            >
              <span class="menu-group-left">
                <span class="menu-group-icon"><component :is="g.icon" /></span>
                <span class="menu-group-text">{{ g.title }}</span>
              </span>
              <IconRight class="menu-group-arrow" />
            </button>
            <div v-show="collapsed || openGroups[g.title]">
              <router-link
                v-for="m in g.items"
                :key="m.path"
                :to="m.path"
                class="menu-item"
                :class="{ active: activeKey === m.path }"
                :title="collapsed ? m.label : undefined"
              >
                <span class="menu-icon"><component :is="m.icon" /></span>
                <span v-show="!collapsed" class="menu-label">{{ m.label }}</span>
              </router-link>
            </div>
          </template>
        </nav>

        <div class="sider-footer">
          <button class="collapse-btn" @click="collapsed = !collapsed" :title="collapsed ? '展开' : '收起'">
            <IconMenuUnfold v-if="collapsed" />
            <IconMenuFold v-else />
          </button>
        </div>
      </aside>

      <!-- 主区 -->
      <div class="main">
        <header class="header">
          <div class="header-left">
            <span class="page-title">{{ currentTitle }}</span>
          </div>
          <div class="header-right">
            <button class="icon-btn" @click="toggleLang" :title="currentLang === 'zh-CN' ? '切换为英文' : 'Switch to Chinese'">
              <IconLanguage />
            </button>
            <span class="header-divider"></span>
            <div class="user">
              <span class="avatar">A</span>
            </div>
            <button class="icon-btn" @click="logout" title="退出登录">
              <IconExport />
            </button>
          </div>
        </header>
        <main class="content">
          <router-view />
        </main>
      </div>
    </div>
  </a-config-provider>
</template>

<script setup lang="ts">
import { computed, ref, markRaw, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import zhCN from '@arco-design/web-vue/es/locale/lang/zh-cn'
import enUS from '@arco-design/web-vue/es/locale/lang/en-us'
import {
  IconDashboard, IconUserGroup, IconRelation, IconMessage, IconBarChart, IconSettings,
  IconApps, IconLocation, IconRobot, IconFile, IconGift, IconLink,
  IconLanguage, IconExport, IconMenuFold, IconMenuUnfold, IconTrophy, IconExperiment,
  IconQrcode, IconCloudDownload, IconWechatpay, IconDelete, IconFire,
  IconExclamationCircle, IconRight, IconHome, IconUser, IconTag, IconEye
} from '@arco-design/web-vue/es/icon'
import { setLocale } from '@/i18n'

const route = useRoute()
const router = useRouter()
const { locale: i18nLocale } = useI18n()

const collapsed = ref(false)

const currentLang = computed(() => i18nLocale.value as string)
const arcoLocale = computed(() => (currentLang.value === 'zh-CN' ? zhCN : enUS))

const menuGroups = [
  {
    title: '首页',
    icon: markRaw(IconHome),
    items: [
      { path: '/admin/dashboard', label: '仪表台', icon: markRaw(IconDashboard) }
    ]
  },
  {
    title: '用户与社交',
    icon: markRaw(IconUser),
    items: [
      { path: '/admin/users', label: '用户管理', icon: markRaw(IconUserGroup) },
      { path: '/admin/groups', label: '群组管理', icon: markRaw(IconRelation) },
      { path: '/admin/moments', label: '朋友圈', icon: markRaw(IconFire) },
      { path: '/admin/messages', label: '消息记录', icon: markRaw(IconMessage) }
    ]
  },
  {
    title: '运营与内容',
    icon: markRaw(IconTag),
    items: [
      { path: '/admin/reports', label: '投诉管理', icon: markRaw(IconExclamationCircle) },
      { path: '/admin/vip-ids', label: '靓号管理', icon: markRaw(IconTrophy) },
      { path: '/admin/invite-codes', label: '邀请码管理', icon: markRaw(IconGift) },
      { path: '/admin/apps', label: '小程序管理', icon: markRaw(IconApps) },
      { path: '/admin/web-whitelist', label: '网页白名单', icon: markRaw(IconLink) }
    ]
  },
  {
    title: '财务管理',
    icon: markRaw(IconWechatpay),
    items: [
      { path: '/admin/finance', label: '财务管理', icon: markRaw(IconWechatpay) },
      { path: '/admin/recharge-orders', label: '充值订单', icon: markRaw(IconCloudDownload) },
      { path: '/admin/withdraw-orders', label: '提现订单', icon: markRaw(IconExport) }
    ]
  },
  {
    title: '数据与监控',
    icon: markRaw(IconEye),
    items: [
      { path: '/admin/stats', label: '数据统计', icon: markRaw(IconBarChart) },
      { path: '/admin/health', label: '系统检测', icon: markRaw(IconExperiment) },
      { path: '/admin/logs', label: '日志', icon: markRaw(IconFile) }
    ]
  },
  {
    title: '系统配置',
    icon: markRaw(IconSettings),
    items: [
      { path: '/admin/configs', label: '系统设置', icon: markRaw(IconSettings) },
      { path: '/admin/nodes', label: '节点管理', icon: markRaw(IconLocation) },
      { path: '/admin/assistant', label: '智能助手', icon: markRaw(IconRobot) },
      { path: '/admin/data-clear', label: '清空数据', icon: markRaw(IconDelete) }
    ]
  }
]

// 菜单分组展开状态：默认只展开当前路由所在分组，其余折叠（菜单太长 → 聚合）。
// 手动展开/折叠存 localStorage，下次进入保持。
// 【注意】此块必须放在 const menuGroups 声明之后 —— 初始化时要 find 它，
// 放前面会踩 const TDZ（Cannot access 'p' before initialization，后台整页白屏）。
const OPEN_KEY = 'im_admin_menu_open'
function loadOpenGroups(): Record<string, boolean> {
  const saved = localStorage.getItem(OPEN_KEY)
  if (saved) {
    try { return JSON.parse(saved) } catch { /* 脏值走默认 */ }
  }
  return {}
}
const openGroups = ref<Record<string, boolean>>(loadOpenGroups())
// 初始进入时：localStorage 没记过且当前分组没展开 → 自动展开当前路由所在分组
const initGroup = menuGroups.find((g) => g.items.some((m) => m.path === route.path))
if (initGroup && openGroups.value[initGroup.title] === undefined) {
  openGroups.value[initGroup.title] = true
}
function persistOpenGroups() {
  localStorage.setItem(OPEN_KEY, JSON.stringify(openGroups.value))
}
function toggleGroup(title: string) {
  openGroups.value[title] = !openGroups.value[title]
  persistOpenGroups()
}
// 切到别的分组页面时自动展开对应分组（只展开，不折叠用户手动收起的）
watch(() => route.path, (p) => {
  const g = menuGroups.find((g) => g.items.some((m) => m.path === p))
  if (g && openGroups.value[g.title] !== false) openGroups.value[g.title] = true
})

const allMenus = computed(() => menuGroups.flatMap((g) => g.items))

const activeKey = computed(() => {
  const m = route.path.match(/^\/admin\/[a-z-]*/)
  return m ? m[0] : '/admin/dashboard'
})

const currentTitle = computed(() => {
  return allMenus.value.find((m) => m.path === activeKey.value)?.label || ''
})

const brandIcon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 5h16a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H9l-5 4V6a1 1 0 0 1 1-1z"/><circle cx="9" cy="10.5" r="0.6" fill="currentColor"/><circle cx="12.5" cy="10.5" r="0.6" fill="currentColor"/><circle cx="16" cy="10.5" r="0.6" fill="currentColor"/></svg>`

function toggleLang() {
  setLocale(currentLang.value === 'zh-CN' ? 'en-US' : 'zh-CN')
}

function logout() {
  localStorage.removeItem('im-token')
  localStorage.removeItem('im-refresh')
  router.push('/login')
}
</script>

<style scoped>
.layout { display: flex; height: 100vh; overflow: hidden; }

/* ===== 侧边栏 ===== */
.sider {
  width: var(--app-sider-width);
  background: #14161c;
  background-image:
    radial-gradient(120px 120px at 20% -10%, rgba(22,93,255,.12), transparent 60%),
    radial-gradient(80px 80px at 80% 10%, rgba(64,128,255,.08), transparent 60%);
  display: flex;
  flex-direction: column;
  transition: width var(--app-transition-smooth);
  box-shadow: 2px 0 12px rgba(0, 0, 0, 0.25);
  z-index: 10;
  flex-shrink: 0;
  border-right: 1px solid #22252d;
}
.sider.collapsed { width: var(--app-sider-collapsed-width); }

.brand {
  height: var(--app-header-height);
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 20px;
  border-bottom: 1px solid #22252d;
  flex-shrink: 0;
}
.sider.collapsed .brand { justify-content: center; padding: 0; gap: 0; }
.brand-mark {
  width: 32px; height: 32px;
  border-radius: 9px;
  background: linear-gradient(135deg, #165dff, #4080ff);
  display: flex; align-items: center; justify-content: center;
  flex-shrink: 0;
  color: #fff;
  box-shadow: 0 4px 14px rgba(22, 93, 255, 0.5), inset 0 1px 0 rgba(255,255,255,.15);
}
.brand-mark :deep(svg) { width: 18px; height: 18px; }
.brand-name {
  color: #f0f2f5;
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.3px;
  white-space: nowrap;
}

/* 菜单 */
.menu { flex: 1; padding: 10px 10px; overflow-y: auto; overflow-x: hidden; }
.menu::-webkit-scrollbar { width: 5px; }
.menu::-webkit-scrollbar-track { background: transparent; }
.menu::-webkit-scrollbar-thumb { background: rgba(255,255,255,.08); border-radius: 5px; }
.menu::-webkit-scrollbar-thumb:hover { background: rgba(255,255,255,.15); }

/* 菜单分组标题 */
.menu-group-title {
  display: flex; align-items: center; justify-content: space-between;
  width: 100%;
  height: 32px;
  padding: 0 12px;
  margin-top: 14px;
  font-size: 13px;
  color: #e5e7eb;
  letter-spacing: 0.3px;
  background: transparent; border: none;
  border-radius: 6px;
  cursor: pointer; text-align: left;
  transition: color .2s ease;
}
.menu-group-title:first-child { margin-top: 6px; }
.menu-group-title:hover { color: #ffffff; }
.menu-group-text { font-weight: 600; }
.menu-group-left { display: flex; align-items: center; gap: 8px; min-width: 0; }
.menu-group-icon {
  display: flex; align-items: center; justify-content: center;
  width: 16px; height: 16px; flex-shrink: 0;
  opacity: 0.85;
}
.menu-group-icon :deep(svg) { width: 16px; height: 16px; }
.menu-group-arrow {
  width: 12px; height: 12px;
  opacity: 0.6;
  transition: transform 0.2s ease;
}
.menu-group-title.open .menu-group-arrow { transform: rotate(90deg); }
.menu-group-title.open { color: #9ca3af; }

/* 菜单条目 */
.menu-item {
  position: relative;
  display: flex; align-items: center; gap: 10px;
  height: 36px;
  padding: 0 12px;
  margin-bottom: 2px;
  margin-left: 4px;
  border-radius: 8px;
  color: #8b9199;
  text-decoration: none;
  font-size: 13px;
  cursor: pointer;
  transition: all .18s ease;
  border-left: 2px solid transparent;
}
.menu-item:hover {
  background: rgba(255,255,255,.04);
  color: #d4d7dd;
}
.menu-item.active {
  background: linear-gradient(90deg, rgba(22,93,255,.22), rgba(22,93,255,.08));
  color: #ffffff;
  border-left-color: #4080ff;
  box-shadow: inset 0 0 0 1px rgba(64,128,255,.15);
}
.menu-item.active::before {
  content: '';
  position: absolute;
  left: -2px;
  top: 50%;
  transform: translateY(-50%);
  width: 3px; height: 20px;
  background: linear-gradient(180deg, #165dff, #4080ff);
  border-radius: 0 3px 3px 0;
  box-shadow: 0 0 8px rgba(64,128,255,.6);
}
.menu-icon {
  display: flex; align-items: center; justify-content: center;
  width: 18px; height: 18px; flex-shrink: 0;
  color: inherit;
}
.menu-icon :deep(svg) { width: 16px; height: 16px; }
.menu-item.active .menu-icon { color: #7ea9ff; }
.menu-label { white-space: nowrap; font-weight: 400; }
.menu-item.active .menu-label { font-weight: 500; }

/* 收起态 */
.sider.collapsed .menu-item {
  justify-content: center;
  padding: 0;
  margin-bottom: 4px;
  border-left: none;
}
.sider.collapsed .menu-item.active {
  background: rgba(22,93,255,.22);
  box-shadow: 0 0 0 1px rgba(64,128,255,.25);
}
.sider.collapsed .menu-item.active::before {
  left: 0;
  width: 100%;
  height: 3px;
  top: auto;
  bottom: -2px;
  transform: none;
  border-radius: 3px 3px 0 0;
}
.sider.collapsed .menu-label { display: none; }

/* 侧栏底部 */
.sider-footer { padding: 10px; border-top: 1px solid #22252d; flex-shrink: 0; }
.collapse-btn {
  width: 100%; height: 34px;
  display: flex; align-items: center; justify-content: center;
  background: transparent; border: none; color: #6b7280;
  border-radius: 8px; cursor: pointer;
  transition: all .2s ease;
}
.collapse-btn:hover { background: rgba(255,255,255,.06); color: #fff; }
.collapse-btn :deep(svg) { width: 18px; height: 18px; }

/* ===== 主区 ===== */
.main { flex: 1; display: flex; flex-direction: column; min-width: 0; }
.header {
  height: var(--app-header-height);
  background: #ffffff;
  border-bottom: 1px solid var(--app-border-1);
  display: flex; align-items: center; justify-content: space-between;
  padding: 0 var(--app-content-padding);
  flex-shrink: 0;
  z-index: 5;
}
.page-title { font-size: 15px; font-weight: 600; color: #1d2129; }
.header-right { display: flex; align-items: center; gap: 2px; }
.icon-btn {
  width: 36px; height: 36px;
  display: flex; align-items: center; justify-content: center;
  background: transparent; border: none; color: #4e5969;
  border-radius: 8px; cursor: pointer;
  transition: background var(--app-transition-base), color var(--app-transition-base);
}
.icon-btn:hover { background: var(--app-border-2); color: #1d2129; }
.icon-btn :deep(svg) { width: 18px; height: 18px; }
.header-divider { width: 1px; height: 20px; background: #e5e6eb; margin: 0 8px; }
.user { display: flex; align-items: center; gap: 8px; padding: 0 6px; }
.avatar {
  width: 30px; height: 30px;
  border-radius: 50%;
  background: linear-gradient(135deg, #165dff, #4080ff);
  color: #fff; display: flex; align-items: center; justify-content: center;
  font-size: 13px; font-weight: 600;
  box-shadow: 0 2px 6px rgba(22, 93, 255, 0.35);
}

.content { flex: 1; overflow-y: auto; padding: var(--app-content-padding); background: var(--app-bg-page); }
</style>
