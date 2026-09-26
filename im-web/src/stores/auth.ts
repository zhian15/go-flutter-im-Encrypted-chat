import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useAuthStore = defineStore('auth', () => {
  const token = ref(localStorage.getItem('im-token') || '')
  const refreshToken = ref(localStorage.getItem('im-refresh') || '')

  function setToken(t: string) {
    token.value = t
    localStorage.setItem('im-token', t)
  }

  function setRefresh(t: string) {
    refreshToken.value = t
    localStorage.setItem('im-refresh', t)
  }

  function logout() {
    token.value = ''
    refreshToken.value = ''
    localStorage.removeItem('im-token')
    localStorage.removeItem('im-refresh')
  }

  return { token, refreshToken, setToken, setRefresh, logout }
})
