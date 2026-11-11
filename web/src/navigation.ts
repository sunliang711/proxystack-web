import type { Component } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import {
  ArchiveOutline,
  CubeOutline,
  DocumentTextOutline,
  GitNetworkOutline,
  GridOutline,
  InformationCircleOutline,
  LayersOutline,
  ListOutline,
  PaperPlaneOutline,
  PeopleOutline,
  PulseOutline,
  ReceiptOutline,
  ServerOutline,
  SettingsOutline,
  StatsChartOutline,
  TimerOutline,
} from '@vicons/ionicons5'

export interface NavItem {
  name: string
  label: string
  icon: Component
}

/** 侧栏导航的分组和页面；全局搜索的页面结果也取自这里。 */
export const navGroups: { key: string; label: string; items: NavItem[] }[] = [
  {
    key: 'overview',
    label: '概览',
    items: [
      { name: 'dashboard', label: '总览', icon: GridOutline },
      { name: 'traffic', label: '流量明细', icon: StatsChartOutline },
    ],
  },
  {
    key: 'proxy',
    label: '代理',
    items: [
      { name: 'stacks', label: 'Stack', icon: LayersOutline },
      { name: 'users', label: '用户', icon: PeopleOutline },
    ],
  },
  {
    key: 'subscription',
    label: '订阅',
    items: [
      { name: 'extra-nodes', label: '额外节点', icon: ServerOutline },
      { name: 'templates', label: '订阅模板', icon: DocumentTextOutline },
      { name: 'sub-publish', label: '发布订阅包', icon: PaperPlaneOutline },
      { name: 'sub-publish-settings', label: 'SSH 发布与部署', icon: GitNetworkOutline },
    ],
  },
  {
    key: 'ops',
    label: '运维',
    items: [
      { name: 'deps', label: '依赖', icon: CubeOutline },
      { name: 'tasks', label: '后台任务', icon: ListOutline },
      { name: 'jobs', label: '定时任务', icon: TimerOutline },
      { name: 'health', label: '健康检查', icon: PulseOutline },
      { name: 'records', label: '日志与历史', icon: ReceiptOutline },
      { name: 'backup', label: '备份与还原', icon: ArchiveOutline },
    ],
  },
  {
    key: 'system',
    label: '系统',
    items: [
      { name: 'settings', label: '系统设置', icon: SettingsOutline },
      { name: 'system', label: '版本与升级', icon: InformationCircleOutline },
    ],
  },
]

/** 不在侧栏、但可以从全局搜索直接打开的位置：页面内的标签页和账号菜单里的页面。 */
export const extraDestinations: { key: string; label: string; group: string; keywords?: string[]; to: RouteLocationRaw }[] = [
  { key: 'traffic/ranking', label: '用量排行', group: '流量明细', keywords: ['排行', 'top'], to: { name: 'traffic', query: { tab: 'ranking' } } },
  { key: 'extra-nodes/manual', label: '手工节点', group: '额外节点', to: { name: 'extra-nodes' } },
  { key: 'extra-nodes/remote', label: '远端节点源', group: '额外节点', keywords: ['同步', '拉取'], to: { name: 'extra-nodes', query: { tab: 'remote' } } },
  { key: 'records/audit', label: '审计日志', group: '日志与历史', keywords: ['操作记录', 'audit'], to: { name: 'records', query: { tab: 'audit' } } },
  { key: 'records/history', label: '配置历史', group: '日志与历史', keywords: ['快照', '回滚', '版本', 'diff'], to: { name: 'records', query: { tab: 'history' } } },
  { key: 'records/notifications', label: '通知记录', group: '日志与历史', keywords: ['邮件', '重发'], to: { name: 'records', query: { tab: 'notifications' } } },
  { key: 'security', label: '账号安全', group: '账号', keywords: ['两步验证', '2fa', 'totp', '锁定'], to: { name: 'security' } },
  { key: 'change-password', label: '修改密码', group: '账号', keywords: ['密码'], to: { name: 'change-password' } },
]
