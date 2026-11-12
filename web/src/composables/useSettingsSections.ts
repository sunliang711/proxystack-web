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

/**
 * 各分组在页面上的标题、表单字段名和常用说法，供全局搜索使用：每个字段单独成为一条结果，
 * 分组本身也可以按字段名和说法找到。fields 必须覆盖页面上的全部字段标签（由测试检查），
 * 动态生成的标签（依赖源、端口池）在这里展开列出。
 */
export const sectionSearch: Record<SettingsSectionName, { title: string; fields: string[]; keywords: string[] }> = {
  basic: { title: '基本', fields: ['对外地址', '面板地址', '时区'], keywords: ['external host', 'panel url', 'timezone'] },
  security: { title: '安全', fields: ['公开入站必须设密码'], keywords: ['公开入站必须认证', '无认证入站', '认证'] },
  subscription: {
    title: '订阅',
    fields: ['来源名', '订阅端对外地址', '全局展示名模板', 'Surge managed config', '更新间隔', 'strict', '自动发布订阅包', '停用或到期用户'],
    keywords: ['展示名模板', '自动发布'],
  },
  traffic: {
    title: '流量',
    fields: ['启用流量监控', '采集间隔', '分钟明细保留', '小时明细保留', '日明细保留', '月明细保留', '暂停时断开现有连接', '每日停服时段'],
    keywords: ['保留天数', '断开连接', '停服', '自动暂停'],
  },
  notify: {
    title: '通知',
    fields: ['启用邮件通知', 'SMTP 服务器', '端口', '加密方式', '用户名', '密码', '发件人', '收件人', '通知事件', '用量预警比例', '到期提前提醒', '测试'],
    keywords: ['邮件通知', 'smtp', 'email', '用量预警', '到期提醒'],
  },
  download_proxy: {
    title: '下载代理',
    fields: ['模式', 'stack', '监听', '协议', '主机', '端口', '用户名', '密码', '由代理解析域名', '测试连接'],
    keywords: ['代理', 'http', 'socks5', 'proxy'],
  },
  deps: {
    title: '依赖源',
    fields: ['xray 版本', 'xray 来源', 'mihomo 版本', 'mihomo 来源', 'geo 版本', 'geo 来源', '下载地址', 'sha256', '归档成员', 'mihomo 构建变体', '自动升级 xray', '自动升级 mihomo', '维护时间窗'],
    keywords: ['构建变体', '自动升级'],
  },
  defaults: {
    title: '默认值',
    fields: ['Mihomo 模式', 'Mihomo 日志级别', 'Xray 日志级别', 'Xray 记录访问日志', 'Xray API', 'API 服务', 'Xray stats', 'Xray policy'],
    keywords: ['日志级别', '访问日志', 'access log', 'stats', 'policy'],
  },
  port_pools: {
    title: '端口池',
    fields: ['Xray 入站', 'Mihomo SOCKS 监听', 'Mihomo HTTP 监听', 'Xray API', 'Mihomo controller'],
    keywords: ['端口', '入站端口', 'port'],
  },
  node_export: { title: '节点导出', fields: ['开启只读导出', 'token', '导出地址'], keywords: ['只读导出', '远端节点源'] },
}
