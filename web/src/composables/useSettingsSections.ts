import { ref, watch } from 'vue'
import { panelBase } from '../api/client'

const sections = ['basic', 'port_pools', 'defaults', 'security', 'subscription', 'node_export', 'traffic', 'notify', 'deps', 'download_proxy', 'policy']

/** 折叠状态只存浏览器本地，按面板路径区分；存储不可用时仍可正常展开。 */
export function useSettingsSections() {
  const key = `psw-settings-expanded:${panelBase}`
  const expanded = ref<string[]>([])
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(key) ?? '[]')
    if (Array.isArray(saved)) expanded.value = sections.filter((name) => saved.includes(name))
  } catch {
    // 首次使用或缓存损坏时默认全部折叠。
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
      if (sections.includes(section)) names.add(section)
      if (path === 'defaults.xray.policy' || path.startsWith('defaults.xray.policy.')) names.add('policy')
    }
    expanded.value = [...names]
  }
  return { expanded, revealErrors }
}
