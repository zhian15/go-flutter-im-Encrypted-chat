import axios from 'axios'
import { useAuthStore } from '@/stores/auth'

// 统一 HTTP 客户端：携带 JWT，401/1002 自动刷新后重试
export const http = axios.create({ baseURL: '/api/v1', timeout: 15000 })

http.interceptors.request.use((config) => {
  const token = localStorage.getItem('im-token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

http.interceptors.response.use(
  (res) => res,
  async (error) => {
    const original = error.config
    const status = error.response?.status
    if (status === 401 && !original._retry) {
      original._retry = true
      const auth = useAuthStore()
      try {
        const { data } = await axios.post('/api/v1/auth/refresh', {
          refreshToken: auth.refreshToken
        })
        if (data.code === 0) {
          auth.setToken(data.data.accessToken)
          return http(original)
        }
      } catch {
        /* refresh failed */
      }
      auth.logout()
      // hash 路由下不能用绝对 '/login'（子目录部署会跳出目录 404），
      // 拼在当前目录路径后面：根部署 → /#/login，/admin/ 部署 → /admin/#/login
      window.location.href = `${window.location.pathname}#/login`
    }
    return Promise.reject(error)
  }
)
