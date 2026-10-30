import { createRouter, createWebHistory } from 'vue-router'
import { panelBase } from './api/client'
import { loadSession, session } from './session'

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
        { path: '', name: 'dashboard', component: () => import('./views/DashboardView.vue'), meta: { title: '总览' } },
        { path: 'stacks', name: 'stacks', component: () => import('./views/StacksView.vue'), meta: { title: 'Stack' } },
        { path: 'stacks/:id', name: 'stack', component: () => import('./views/stack/StackEditView.vue'), meta: { title: 'Stack 编辑' } },
        { path: 'users', name: 'users', component: () => import('./views/UsersView.vue'), meta: { title: '用户' } },
        { path: 'users/:id', name: 'user', component: () => import('./views/UserDetailView.vue'), meta: { title: '用户详情' } },
        { path: 'deps', name: 'deps', component: () => import('./views/DepsView.vue'), meta: { title: '依赖' } },
        { path: 'history', name: 'history', component: () => import('./views/HistoryView.vue'), meta: { title: '配置历史' } },
        { path: 'backup', name: 'backup', component: () => import('./views/BackupView.vue'), meta: { title: '备份' } },
        { path: 'settings', name: 'settings', component: () => import('./views/SettingsView.vue'), meta: { title: '系统设置' } },
        { path: 'system', name: 'system', component: () => import('./views/SystemView.vue'), meta: { title: '系统' } },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
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
