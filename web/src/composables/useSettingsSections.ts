import { ref, watch } from 'vue'
import { panelBase } from '../api/client'

export const settingsSections = ['basic', 'port_pools', 'defaults', 'security', 'subscription', 'node_export', 'traffic', 'notify', 'deps', 'download_proxy', 'policy']

/** 折叠状态只存浏览器本地，按面板路径区分；存储不可用时仍可正常展开。 */
export function useSettingsSections() {
  const key = `psw-settings-expanded:${panelBase}`
  // 首次使用时展开「基本」，缓存损坏或存储不可用时全部折叠。
  const expanded = ref<string[]>([])
  try {
    const raw = localStorage.getItem(key)
    const saved: unknown = raw === null ? ['basic'] : JSON.parse(raw)
    if (Array.isArray(saved)) expanded.value = settingsSections.filter((name) => saved.includes(name))
  } catch {
    // 缓存损坏或浏览器禁止存储。
  }
  watch(expanded, (names) => {
    try {
      localStorage.setItem(key, JSON.stringify(names))
    } catch {
      // 浏览器禁止存储时只在当前页面保留状态。
    }
  })

  function revealErrors(fields: Record<string, string>) {
    const names = new Set(expanded.value)
    for (const path of Object.keys(fields)) {
      const section = path.split('.')[0]!
      if (settingsSections.includes(section)) names.add(section)
      if (path === 'defaults.xray.policy' || path.startsWith('defaults.xray.policy.')) names.add('policy')
    }
    expanded.value = [...names]
  }
  /** 展开或收起全部顶层分组；嵌套的 policy 保持原状。 */
  function setAll(open: boolean) {
    const nested = expanded.value.filter((name) => name === 'policy')
    expanded.value = open ? [...settingsSections.filter((name) => name !== 'policy'), ...nested] : nested
  }
  return { expanded, revealErrors, setAll }
}
