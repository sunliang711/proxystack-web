import { reactive } from 'vue'
import { api, onUserActivity, setCSRFToken, type Schemas } from './api/client'

interface SessionState {
  loaded: boolean
  admin: Schemas['AdminView'] | null
  /** 闲置到期时间与绝对到期时间（毫秒时间戳），会话在两者中较早的一个失效；0 表示未知。 */
  expiresAt: number
  maxExpiresAt: number
  /** 重新登录对话框：expired 表示会话已失效，renew 表示到期前主动重新登录，空串表示不显示。 */
  reauth: '' | 'expired' | 'renew'
}

export const session = reactive<SessionState>({ loaded: false, admin: null, expiresAt: 0, maxExpiresAt: 0, reauth: '' })

/** 最近一次读取会话的时间。 */
let lastCheck = 0
/** 管理员持续操作时，每隔这么久读取一次会话，续期并刷新到期时间。 */
const HEARTBEAT = 5 * 60_000

function applyExpiry(body: Pick<Schemas['SessionBody'], 'expires_at' | 'max_expires_at'>) {
  session.expiresAt = Date.parse(body.expires_at)
  session.maxExpiresAt = Date.parse(body.max_expires_at)
  lastCheck = Date.now()
}

export function applySession(body: Schemas['SessionBody']) {
  session.admin = body.admin
  setCSRFToken(body.csrf_token)
  applyExpiry(body)
  session.reauth = ''
  session.loaded = true
}

export function clearSession() {
  session.admin = null
  setCSRFToken('')
  session.expiresAt = 0
  session.maxExpiresAt = 0
  session.reauth = ''
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

/**
 * 重新读取会话的到期时间。管理员刚操作过时请求带操作标记，同时续期；
 * 会话已失效时由统一的 401 处理打开重新登录对话框。
 */
export async function checkSession() {
  if (!session.admin || session.reauth === 'expired') return
  lastCheck = Date.now()
  const { data } = await api.GET('/api/auth/session')
  if (data) applyExpiry(data)
}

/** 会话已失效：保留当前页面和未保存的修改，弹出重新登录对话框。 */
export function sessionExpired() {
  session.reauth = 'expired'
}

// 持续操作时定期续期；从睡眠或其他标签页回来时先确认会话是否仍然有效。
onUserActivity(() => {
  if (session.admin && session.reauth !== 'expired' && Date.now() - lastCheck > HEARTBEAT) void checkSession()
})
document.addEventListener?.('visibilitychange', () => {
  if (document.visibilityState === 'visible') void checkSession()
})

export async function logout() {
  await api.POST('/api/auth/logout')
  clearSession()
}
