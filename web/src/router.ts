import { createRouter, createWebHistory } from 'vue-router'
import { panelBase, setUnauthorizedHandler } from './api/client'
import { clearSession, loadSession, session } from './session'

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
        { path: '', name: 'dashboard', component: () => import('./views/DashboardView.vue'), meta: { title: '总览', description: '对比今日和本月的各 Stack 入站与用户流量，并展示最近一小时实时速率与日月用量排行。采集失败、通知发送失败等需要处理的事项会在顶部提示。' } },
        { path: 'traffic', name: 'traffic', component: () => import('./views/TrafficView.vue'), meta: { title: '流量明细', description: '按小时、日、月、年查看用户、入站或出站的流量趋势与明细，可按 stack 和对象筛选，并导出为 CSV。' } },
        { path: 'records', name: 'records', component: () => import('./views/RecordsView.vue'), meta: { title: '日志与历史', description: '管理员操作、配置版本和邮件通知的历史记录，用于查看谁在什么时候改了什么、通知是否发出。' } },
        // 三个页面合并到「日志与历史」后，旧地址跳到对应标签，书签和邮件里的旧链接仍然可用。
        { path: 'notifications', redirect: { name: 'records', query: { tab: 'notifications' } } },
        { path: 'audit', redirect: { name: 'records', query: { tab: 'audit' } } },
        { path: 'history', redirect: { name: 'records', query: { tab: 'history' } } },
        { path: 'stacks', name: 'stacks', component: () => import('./views/StacksView.vue'), meta: { title: 'Stack', description: '每个 stack 是一组独立运行的 xray / mihomo 进程及其配置。在这里新建、导入和导出 stack，进入后编辑入站、出站与规则。' } },
        { path: 'stacks/:id', name: 'stack', component: () => import('./views/stack/StackEditView.vue'), meta: { title: 'Stack 编辑', description: '编辑这个 stack 的入站、出站、上游、代理组和规则。保存只写入数据库，应用后才会生成配置并重启相关服务。' } },
        { path: 'users', name: 'users', component: () => import('./views/UsersView.vue'), meta: { title: '用户', description: '代理用户及其凭据、额度与到期时间。暂停会立即把用户从代理端移除，配置保留，恢复后即可继续使用。' } },
        { path: 'users/:id', name: 'user', component: () => import('./views/UserDetailView.vue'), meta: { title: '用户详情', description: '编辑用户的基本信息、额度与到期、订阅和凭据档案，并单独控制该用户在各个 stack 上的暂停状态。' } },
        { path: 'manual-nodes', name: 'manual-nodes', component: () => import('./views/ManualNodesView.vue'), meta: { title: '手工节点', description: '手工录入的订阅节点，按用户归属，与本机 stack 生成的节点一起进入该用户的订阅。' } },
        { path: 'remote-sources', name: 'remote-sources', component: () => import('./views/RemoteSourcesView.vue'), meta: { title: '远端节点源', description: '从另一台 proxystack-web 拉取其导出的节点，按用户名分给本机同名用户。「远端节点源同步」定时任务按计划自动同步，默认每 30 分钟一次。' } },
        { path: 'sub-publish', name: 'sub-publish', component: () => import('./views/SubPublishView.vue'), meta: { title: '发布订阅包', description: '把当前订阅内容签名后，通过 SSH 在线发送到一个或多个订阅端，或下载文件后离线导入。' } },
        { path: 'sub-publish-settings', name: 'sub-publish-settings', component: () => import('./views/SubPublishSettingsView.vue'), meta: { title: 'SSH 发布与部署', description: '使用同一 SSH 通道管理订阅端安装、更新、回滚、在线发布和连接配置。' } },
        { path: 'templates', name: 'templates', component: () => import('./views/TemplatesView.vue'), meta: { title: '订阅模板', description: 'Clash、Premium Clash 和 Surge 订阅文件的模板。默认使用内置模板，也可以在其基础上自定义。' } },
        { path: 'deps', name: 'deps', component: () => import('./views/DepsView.vue'), meta: { title: '依赖', description: '管理 xray、mihomo 和 geo 数据文件的安装、升级与回滚。新版本先通过当前配置的校验再替换，失败时自动回滚。' } },
        { path: 'jobs', name: 'jobs', component: () => import('./views/JobsView.vue'), meta: { title: '定时任务', description: '内置定时任务的执行计划、超时设置和最近运行结果，可以手动立即执行，每次执行的完整记录在「后台任务」页查看。流量汇总、阈值恢复和到期判定随采集实时进行，不在此列；时间按系统设置的时区计算。' } },
        { path: 'tasks', name: 'tasks', component: () => import('./views/TasksView.vue'), meta: { title: '后台任务', description: '查看每一次任务执行的状态、耗时与日志，包括应用配置、依赖安装、导出迁移包等一次性任务，以及定时任务的每次执行。任务在后台运行，可以继续操作其他页面；运行中的任务优先显示。定时任务的执行计划在「定时任务」页配置。' } },
        { path: 'tasks/:source(task|job)/:id(\\d+)', name: 'task-detail', component: () => import('./views/TaskDetailView.vue'), meta: { title: '任务详情', description: '任务状态与执行日志自动更新，关闭页面不会中止服务器上的任务。' } },
        { path: 'health', name: 'health', component: () => import('./views/HealthView.vue'), meta: { title: '健康检查', description: '汇总依赖、服务状态、配置漂移、端口占用、流量采集、磁盘与备份等检查项，每项给出结论和处理建议。' } },
        { path: 'backup', name: 'backup', component: () => import('./views/BackupView.vue'), meta: { title: '备份与还原', description: '导出整机迁移包、下载自动生成的备份文件，或从迁移包恢复整个实例。' } },
        { path: 'settings', name: 'settings', component: () => import('./views/SettingsView.vue'), meta: { title: '系统设置', description: '对外地址、端口池、默认值、安全、订阅、节点导出、流量、通知、依赖源和下载代理等全局设置。' } },
        { path: 'security', name: 'security', component: () => import('./views/SecurityView.vue'), meta: { title: '账号安全', description: '为管理员账号开启两步验证，并了解登录失败锁定和设备丢失时的恢复方法。' } },
        { path: 'system', name: 'system', component: () => import('./views/SystemView.vue'), meta: { title: '系统', description: '管理服务的版本与构建信息，以及面板的监听地址和端口。' } },
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
