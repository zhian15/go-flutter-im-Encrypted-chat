import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  // 相对路径 base './'：资源路径随 index.html 所在目录解析，产物扔进任意目录都能跑
  // （站点根 / im-admin / admin ...），配合 hash 路由（见 src/admin-main.ts）不需要
  // nginx 的 SPA 兜底/伪静态——这是「编译完直接上传」的最省心组合。
  //
  // ⚠️ 历史教训：之前用 './' + createWebHistory 曾白屏（./assets 相对多级地址栏解析错），
  //    根因在 history 路由，不在 base；路由已改 hash，这个组合就不会再踩。
  //    若未来要换回 createWebHistory，base 必须同时改回 '/' 并配 nginx 兜底。
  base: './',
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url))
    }
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/ws': { target: 'ws://localhost:9090', ws: true }
    }
  }
})
