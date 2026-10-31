import { computed, ref, watch, watchEffect } from 'vue'

/** 界面主题偏好：跟随系统、浅色或深色。 */
export type ThemePreference = 'system' | 'light' | 'dark'

const STORAGE_KEY = 'psw-theme'

function load(): ThemePreference {
  try {
    const value = localStorage.getItem(STORAGE_KEY)
    if (value === 'light' || value === 'dark') return value
  } catch {
    // 浏览器禁用存储时按跟随系统处理。
  }
  return 'system'
}

const media = window.matchMedia('(prefers-color-scheme: dark)')
const systemDark = ref(media.matches)
media.addEventListener('change', (event) => (systemDark.value = event.matches))

export const themePreference = ref<ThemePreference>(load())
export const isDark = computed(() => themePreference.value === 'dark' || (themePreference.value === 'system' && systemDark.value))

watch(themePreference, (value) => {
  try {
    if (value === 'system') localStorage.removeItem(STORAGE_KEY)
    else localStorage.setItem(STORAGE_KEY, value)
  } catch {
    // 存不下时只在本次会话生效。
  }
})

// 全局样式按 <html data-theme> 区分深浅色，不再依赖系统媒体查询。
watchEffect(() => {
  document.documentElement.dataset.theme = isDark.value ? 'dark' : 'light'
})
