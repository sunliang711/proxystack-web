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
