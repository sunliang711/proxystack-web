import { createRouter, createWebHistory } from 'vue-router'
import { panelBase, setUnauthorizedHandler } from './api/client'
import { clearSession, loadSession, session, sessionExpired } from './session'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    // 页面标题和正文顶部的页面说明。
    title?: string
    description?: string
  }
}

const router = createRouter({
  history: createWebHistory(panelBase),
  routes: [
    { path: '/login', name: 'login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
    { path: '/reset', name: 'reset', component: () => import('./views/ResetPasswordView.vue'), meta: { public: true } },
    { path: '/change-password', name: 'change-password', component: () => import('./views/ChangePasswordView.vue') },
    {
      path: '/',
      component: () => import('./layouts/MainLayout.vue'),
      children: [
        { path: '', name: 'dashboard', component: () => import('./views/DashboardView.vue'), meta: { title: '总览', description: '今日和本月的流量、实时速率和各 Stack 用量；需要处理的事项会在顶部提示。' } },
        { path: 'traffic', name: 'traffic', component: () => import('./views/TrafficView.vue'), meta: { title: '流量明细', description: '按用户或按 Stack 查看任意时间段的流量，可对比上一期并导出 CSV。' } },
        { path: 'records', name: 'records', component: () => import('./views/RecordsView.vue'), meta: { title: '日志与历史', description: '管理员操作、配置版本和邮件通知的历史记录。' } },
        // 三个页面合并到「日志与历史」后，旧地址跳到对应标签，书签和邮件里的旧链接仍然可用。
        { path: 'notifications', redirect: { name: 'records', query: { tab: 'notifications' } } },
        { path: 'audit', redirect: { name: 'records', query: { tab: 'audit' } } },
        { path: 'history', redirect: { name: 'records', query: { tab: 'history' } } },
        { path: 'stacks', name: 'stacks', component: () => import('./views/StacksView.vue'), meta: { title: 'Stack', description: '每个 Stack 是一组独立运行的 xray / mihomo 进程，即一个代理实例。' } },
        { path: 'stacks/:id', name: 'stack', component: () => import('./views/stack/StackEditView.vue'), meta: { title: 'Stack 编辑', description: '改动先保存，点「应用」后才生成配置并重启服务。' } },
        { path: 'users', name: 'users', component: () => import('./views/UsersView.vue'), meta: { title: '用户', description: '代理用户及其凭据、额度与到期时间。' } },
        { path: 'users/:id', name: 'user', component: () => import('./views/UserDetailView.vue'), meta: { title: '用户详情', description: '改动点「保存」后写入；标着「当场生效」的开关立即生效。' } },
        { path: 'extra-nodes', name: 'extra-nodes', component: () => import('./views/ExtraNodesView.vue'), meta: { title: '额外节点', description: '本机 Stack 之外进入订阅的节点：手工录入，或从另一台 proxystack-web 拉取。' } },
        { path: 'manual-nodes', redirect: { name: 'extra-nodes' } },
        { path: 'remote-sources', redirect: { name: 'extra-nodes', query: { tab: 'remote' } } },
        { path: 'sub-publish', name: 'sub-publish', component: () => import('./views/SubPublishView.vue'), meta: { title: '发布订阅包', description: '把订阅内容签名后通过 SSH 发送到订阅端，或下载文件离线导入。' } },
        { path: 'sub-publish-settings', name: 'sub-publish-settings', component: () => import('./views/SubPublishSettingsView.vue'), meta: { title: 'SSH 发布与部署', description: '通过 SSH 管理订阅端的安装、更新、回滚和在线发布。' } },
        { path: 'templates', name: 'templates', component: () => import('./views/TemplatesView.vue'), meta: { title: '订阅模板', description: 'Clash、Premium Clash 和 Surge 订阅文件的模板，可在内置模板基础上自定义。' } },
        { path: 'deps', name: 'deps', component: () => import('./views/DepsView.vue'), meta: { title: '依赖', description: 'xray、mihomo 和 geo 数据文件的安装、升级与回滚；新版本先通过校验再替换。' } },
        { path: 'jobs', name: 'jobs', component: () => import('./views/JobsView.vue'), meta: { title: '定时任务', description: '内置定时任务的执行计划和最近结果，可以手动立即执行。' } },
        { path: 'tasks', name: 'tasks', component: () => import('./views/TasksView.vue'), meta: { title: '后台任务', description: '每次任务执行的状态、耗时与日志；任务在后台运行，可以继续操作其他页面。' } },
        { path: 'tasks/:source(task|job)/:id(\\d+)', name: 'task-detail', component: () => import('./views/TaskDetailView.vue'), meta: { title: '任务详情', description: '任务状态与日志自动更新，关闭页面不会中止任务。' } },
        { path: 'health', name: 'health', component: () => import('./views/HealthView.vue'), meta: { title: '健康检查', description: '依赖、服务、配置漂移、端口、采集、磁盘与备份等检查项及处理建议。' } },
        { path: 'backup', name: 'backup', component: () => import('./views/BackupView.vue'), meta: { title: '备份与还原', description: '导出整机迁移包、下载自动备份，或从迁移包恢复整个实例。' } },
        { path: 'settings', name: 'settings', component: () => import('./views/SettingsView.vue'), meta: { title: '系统设置', description: '全局设置；分组标题旁的标签说明保存后何时生效，悬停查看详情。' } },
        { path: 'security', name: 'security', component: () => import('./views/SecurityView.vue'), meta: { title: '账号安全', description: '管理员账号的两步验证、登录失败锁定和设备丢失时的恢复方法。' } },
        { path: 'system', name: 'system', component: () => import('./views/SystemView.vue'), meta: { title: '版本与升级', description: '管理服务的版本、升级和面板监听地址。' } },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

setUnauthorizedHandler((schemaPath) => {
  // 首次进入受保护页面时由路由守卫使用目标地址生成回跳参数。
  if (schemaPath === '/api/auth/session' && !session.loaded) return
  const current = router.currentRoute.value
  if (current.meta.public || current.name === 'login') return
  // 已登录的页面上会话失效时原地弹出重新登录对话框，保留当前页面和未保存的修改。
  if (session.admin) {
    sessionExpired()
    return
  }
  const redirect = current.fullPath
  clearSession()
  void router.replace({
    name: 'login',
    query: { expired: '1', ...(redirect !== '/' ? { redirect } : {}) },
  })
})

router.beforeEach(async (to) => {
  if (to.meta.public) {
    return true
  }
  if (!session.loaded) {
    await loadSession()
  }
  if (!session.admin) {
    return { name: 'login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : {} }
  }
  if (session.admin.must_change_password && to.name !== 'change-password') {
    return { name: 'change-password' }
  }
  return true
})

export default router
