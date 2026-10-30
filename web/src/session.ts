import { reactive } from 'vue'
import { api, setCSRFToken, type Schemas } from './api/client'

interface SessionState {
  loaded: boolean
  admin: Schemas['AdminView'] | null
}

export const session = reactive<SessionState>({ loaded: false, admin: null })

export function applySession(body: Schemas['SessionBody']) {
  session.admin = body.admin
  setCSRFToken(body.csrf_token)
  session.loaded = true
}

export function clearSession() {
  session.admin = null
  setCSRFToken('')
  session.loaded = true
}

/** 加载当前会话；未登录时清空。 */
export async function loadSession() {
  const { data } = await api.GET('/api/auth/session')
  if (data) {
    applySession(data)
  } else {
    clearSession()
  }
}

export async function logout() {
  await api.POST('/api/auth/logout')
  clearSession()
}
