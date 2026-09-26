import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHashHistory } from 'vue-router'
import ArcoVue from '@arco-design/web-vue'
import ArcoVueIcon from '@arco-design/web-vue/es/icon'
import '@arco-design/web-vue/dist/arco.css'
import '@/styles/theme.css'
import AdminApp from '@/views/admin/AdminApp.vue'
import { i18n } from '@/i18n'

// 管理后台独立入口：只含后台路由与后台登录页
// 路由用 hash 模式（URL 形如 /#/admin/dashboard）：
// 页面永远只请求 index.html 所在目录的 ./assets/*，部署时把 dist 扔进任意目录
// （站点根 / im-admin / admin ...）都能直接跑，不需要 nginx SPA 兜底、不需要伪静态。
const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/admin/dashboard' },
    { path: '/login', name: 'admin-login', component: () => import('@/views/admin/AdminLogin.vue') },
    {
      path: '/admin',
      component: () => import('@/views/admin/AdminLayout.vue'),
      meta: { requiresAdmin: true },
      children: [
        { path: '', redirect: '/admin/dashboard' },
        { path: 'dashboard', component: () => import('@/views/admin/DashboardView.vue') },
        { path: 'users', component: () => import('@/views/admin/UserManage.vue') },
        { path: 'reports', component: () => import('@/views/admin/ReportManage.vue') },
        { path: 'groups', component: () => import('@/views/admin/GroupManage.vue') },
        { path: 'messages', component: () => import('@/views/admin/MessageQuery.vue') },
        { path: 'vip-ids', component: () => import('@/views/admin/VipIdsView.vue') },
        { path: 'invite-codes', component: () => import('@/views/admin/InviteCodeManage.vue') },
        { path: 'stats', component: () => import('@/views/admin/StatsView.vue') },
        { path: 'health', component: () => import('@/views/admin/HealthCheckView.vue') },
        { path: 'configs', component: () => import('@/views/admin/ConfigView.vue') },
        { path: 'data-clear', component: () => import('@/views/admin/DataClearView.vue') },
        { path: 'apps', component: () => import('@/views/admin/AppEntries.vue') },
        { path: 'web-whitelist', component: () => import('@/views/admin/WebWhitelist.vue') },
        { path: 'nodes', component: () => import('@/views/admin/NodeManage.vue') },
        { path: 'assistant', component: () => import('@/views/admin/AssistantManage.vue') },
        { path: 'logs', component: () => import('@/views/admin/LogView.vue') },
        { path: 'finance', component: () => import('@/views/admin/FinanceView.vue') },
        { path: 'recharge-orders', component: () => import('@/views/admin/RechargeOrdersView.vue') },
        { path: 'withdraw-orders', component: () => import('@/views/admin/WithdrawOrdersView.vue') },
        { path: 'moments', component: () => import('@/views/admin/MomentsView.vue') }
      ]
    }
  ]
})

// 后台守卫：未登录一律进后台登录页
router.beforeEach((to) => {
  if (to.path !== '/login' && !localStorage.getItem('im-token')) return '/login'
  return true
})

createApp(AdminApp)
  .use(createPinia())
  .use(router)
  .use(i18n)
  .use(ArcoVue)
  .use(ArcoVueIcon)
  .mount('#app')
