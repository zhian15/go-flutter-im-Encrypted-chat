import { createI18n } from 'vue-i18n'
import messages from '@/locales'

// 跟随浏览器语言 + 手动切换（localStorage 记忆）
const saved = localStorage.getItem('im-lang')
const browserLang = navigator.language.startsWith('en') ? 'en-US' : 'zh-CN'

export const i18n = createI18n({
  legacy: false,
  locale: saved || browserLang,
  fallbackLocale: 'zh-CN',
  messages
})

export function setLocale(lang: string) {
  localStorage.setItem('im-lang', lang)
  i18n.global.locale.value = lang as never
}
