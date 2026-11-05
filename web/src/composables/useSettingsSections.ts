import { ref, watch } from 'vue'
import { panelBase } from '../api/client'

export const settingsSections = ['basic', 'port_pools', 'defaults', 'security', 'subscription', 'node_export', 'traffic', 'notify', 'deps', 'download_proxy', 'policy']

/** 分组的生效方式。 */
export interface SectionEffect {
  label: string
  type: 'success' | 'warning' | 'info' | 'default'
  tip: string
}

/** 各分组保存后的生效方式，显示在分组标题右侧。 */
export const sectionEffects: Record<string, SectionEffect> = {
  basic: { label: '部分随订阅包生效', type: 'info', tip: '对外地址写入订阅节点，重新发布订阅包后生效；面板地址和时区保存后立即生效。' },
  port_pools: { label: '立即生效', type: 'success', tip: '只影响之后自动分配的端口，已有 stack 的端口保持不变。' },
  defaults: { label: '需要应用', type: 'warning', tip: '作为各 stack 的默认值写入生成的 xray / mihomo 配置，保存后进入「待应用变更」，应用后生效。' },
  security: { label: '影响校验', type: 'info', tip: '保存后，stack 的保存和应用按新规则校验；不符合规则的 stack 在「待应用变更」中报错。' },
  subscription: { label: '随订阅包生效', type: 'info', tip: '随订阅包下发，在「发布订阅包」签发并送到订阅端后生效。' },
  node_export: { label: '立即生效', type: 'success', tip: '保存后其他实例立即按新设置拉取本机节点。' },
  traffic: { label: '部分需要应用', type: 'warning', tip: '开启或关闭流量监控会改动生成的配置，需要应用；采集间隔、保留天数和断开连接设置保存后立即生效。' },
  notify: { label: '立即生效', type: 'success', tip: '保存后之后的通知按新设置发送。' },
  deps: { label: '立即生效', type: 'success', tip: '保存后之后的安装、升级和自动升级按新设置执行。' },
  download_proxy: { label: '立即生效', type: 'success', tip: '保存后依赖管理和面板自更新的对外请求立即使用新设置。' },
}

/** 默认隐藏的高级分组；「显示高级设置」打开后才出现。 */
export const advancedSections = ['port_pools', 'node_export', 'deps']

/** 字段是否属于高级设置：高级分组内的字段，以及默认值中的 Xray API、stats 和 policy。 */
export function isAdvancedField(path: string): boolean {
  const section = path.split('.')[0]!
  if (advancedSections.includes(section)) return true
  return /^defaults\.xray\.(api_enabled|api_services|stats|policy)(\.|$)/.test(path)
}

/** 折叠状态只存浏览器本地，按面板路径区分；存储不可用时仍可正常展开。 */
export function useSettingsSections() {
  const key = `psw-settings-expanded:${panelBase}`
  const advancedKey = `psw-settings-advanced:${panelBase}`
  const showAdvanced = ref(false)
  try {
    showAdvanced.value = localStorage.getItem(advancedKey) === 'shown'
  } catch {
    // 浏览器禁止存储时默认隐藏高级设置。
  }
  watch(showAdvanced, (show) => {
    try {
      localStorage.setItem(advancedKey, show ? 'shown' : 'hidden')
    } catch {
      // 浏览器禁止存储时只在当前页面保留状态。
    }
  })
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
      if (isAdvancedField(path)) showAdvanced.value = true
    }
    expanded.value = [...names]
  }
  /** 展开或收起全部顶层分组；嵌套的 policy 保持原状。 */
  function setAll(open: boolean) {
    const nested = expanded.value.filter((name) => name === 'policy')
    expanded.value = open ? [...settingsSections.filter((name) => name !== 'policy'), ...nested] : nested
  }
  return { expanded, showAdvanced, revealErrors, setAll }
}
