// 审计日志的动作代码、操作者和对象在界面上的中文展示。

const actionLabels: Record<string, string> = {
  'auth.login': '登录',
  'auth.login_failed': '登录失败',
  'auth.logout': '退出登录',
  'admin.password_change': '修改密码',
  'admin.password_reset': '重置密码',
  'admin.reset_link_create': '生成重置链接',
  'admin.totp_enable': '开启两步验证',
  'admin.totp_disable': '关闭两步验证',
  'system.init': '初始化实例',
  'system.panel_listen': '修改面板监听',
  'system.self_update': '升级管理服务',
  'config.settings': '修改系统设置',
  'config.user': '修改用户',
  'config.stack': '修改 Stack',
  'config.rollback': '回滚配置',
  'config.import': '导入配置',
  'config.export': '导出配置',
  'config.secret_view': '查看明文凭据',
  'config.manual_nodes': '修改手工节点',
  'config.remote_sources': '修改远端节点源',
  'config.templates': '修改订阅模板',
  'runtime.apply': '应用配置',
  'runtime.unit': '启停服务',
  'stack.auto_stop': 'Stack 自动停止',
  'user.pause': '暂停 / 恢复用户',
  'deps.install': '安装依赖',
  'deps.rollback': '回滚依赖',
  'backup.export': '导出备份',
  'backup.restore': '恢复备份',
  'backup.delete': '删除备份',
  'job.update': '修改定时任务',
  'access.icon_set': '设置网站图标',
  'access.icon_delete': '删除网站图标',
  'job.run': '执行定时任务',
  'subscription.publish': '签发订阅包',
  'subscription.deliver': '投递订阅包',
  'subscription.publish_target': '修改订阅端',
  'subscription.publish_test': '测试订阅端',
  'subscription.ssh_identity': '修改 SSH 密钥',
  'subscription.deploy_inspect': '检测订阅端部署',
  'subscription.deploy_apply': '部署订阅端',
  'subscription.deploy_rollback': '回滚订阅端',
}

const actorLabels: Record<string, string> = {
  'scheduled-upgrade': '预约升级',
  cli: '命令行',
}

export function auditActionLabel(action: string): string {
  return actionLabels[action] ?? action
}

export function auditActorLabel(actor: string): string {
  return actorLabels[actor] ?? actor
}

/** 筛选框里输入的中文名称（例如「预约升级」）换回记录中保存的原始操作者，其余输入原样使用。 */
export function auditActorQuery(input: string): string {
  const text = input.trim()
  return Object.entries(actorLabels).find(([, label]) => label === text)?.[0] ?? text
}

const objectLabels: Record<string, string> = {
  manual_nodes: '手工节点',
  remote_sources: '远端节点源',
  settings: '系统设置',
  templates: '订阅模板',
  runtime: '运行时',
  panel: '管理面板',
}

/** 订阅端相关动作的对象是订阅端编号，补上类型说明；固定的配置对象换成中文名；其他对象原样返回。 */
export function auditObjectLabel(action: string, object: string): string {
  if (action.startsWith('subscription.') && action !== 'subscription.publish' && /^\d+$/.test(object)) return `订阅端 #${object}`
  return objectLabels[object] ?? object
}
