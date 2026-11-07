/** 系统设置的分组、所在标签页和生效方式。 */

export const settingsSections = ['basic', 'security', 'subscription', 'traffic', 'notify', 'download_proxy', 'deps', 'defaults', 'port_pools', 'node_export'] as const
export type SettingsSectionName = (typeof settingsSections)[number]

export type SettingsTab = 'basic' | 'subscription' | 'monitor' | 'download' | 'advanced'

/** 标签页按用途划分；「高级」放平时很少调整、需要了解 xray / mihomo 细节的设置。 */
export const settingsTabs: { name: SettingsTab; label: string; sections: SettingsSectionName[] }[] = [
  { name: 'basic', label: '基本', sections: ['basic', 'security'] },
  { name: 'subscription', label: '订阅', sections: ['subscription'] },
  { name: 'monitor', label: '流量与通知', sections: ['traffic', 'notify'] },
  { name: 'download', label: '下载与依赖', sections: ['download_proxy', 'deps'] },
  { name: 'advanced', label: '高级', sections: ['defaults', 'port_pools', 'node_export'] },
]

/** 分组所在的标签页；未知分组归入「基本」。 */
export function tabOfSection(section: string): SettingsTab {
  return settingsTabs.find((tab) => (tab.sections as string[]).includes(section))?.name ?? 'basic'
}

/** 校验错误的字段路径（例如 notify.host）所在的标签页。 */
export function tabForField(path: string): SettingsTab {
  return tabOfSection(path.split('.')[0]!)
}

/** 分组的生效方式。 */
export interface SectionEffect {
  label: string
  type: 'success' | 'warning' | 'info' | 'default'
  tip: string
}

/** 各分组保存后的生效方式，显示在分组标题右侧。 */
export const sectionEffects: Record<SettingsSectionName, SectionEffect> = {
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

/** 各分组在页面上的标题，以及全局搜索按其匹配的字段名和常用说法。 */
export const sectionSearch: Record<SettingsSectionName, { title: string; keywords: string[] }> = {
  basic: { title: '基本', keywords: ['对外地址', '面板地址', '时区', 'external host', 'panel url', 'timezone'] },
  security: { title: '安全', keywords: ['公开入站必须认证', '无认证入站', '认证'] },
  subscription: { title: '订阅', keywords: ['来源名', '订阅端对外地址', '展示名模板', 'surge managed', '更新间隔', 'strict', '停用或到期用户'] },
  traffic: { title: '流量', keywords: ['流量监控', '采集间隔', '明细保留', '保留天数', '断开连接'] },
  notify: { title: '通知', keywords: ['邮件通知', 'smtp', '发件人', '收件人', '通知事件', '用量预警', '到期提醒', 'email'] },
  download_proxy: { title: '下载代理', keywords: ['代理', 'http', 'socks5', '测试连接', 'proxy'] },
  deps: { title: '依赖源', keywords: ['xray 版本', 'mihomo 版本', 'geo', 'sha256', '下载地址', '构建变体', '自动升级', '维护时间窗'] },
  defaults: { title: '默认值', keywords: ['mihomo 模式', '日志级别', '访问日志', 'access log', 'xray api', 'stats', 'policy'] },
  port_pools: { title: '端口池', keywords: ['端口', '入站端口', 'socks 监听', 'http 监听', 'controller', 'port'] },
  node_export: { title: '节点导出', keywords: ['只读导出', 'token', '导出地址', '远端节点源'] },
}
