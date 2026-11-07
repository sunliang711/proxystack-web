import createClient, { type Middleware } from 'openapi-fetch'
import type { components, paths } from './schema'

export type Schemas = components['schemas']

/** 面板的根地址，取自后端注入的 <base href>。 */
export const panelBase = new URL('.', document.baseURI).pathname

let csrfToken = ''
let unauthorizedHandler: ((schemaPath: string) => void) | null = null

export function setCSRFToken(token: string) {
  csrfToken = token
}

/** 不经过 openapi-fetch 的请求（例如文件上传）需要手工带上 CSRF token。 */
export function getCSRFToken(): string {
  return csrfToken
}

/** 注册会话失效处理，由路由层负责跳转并保留当前地址。 */
export function setUnauthorizedHandler(handler: (schemaPath: string) => void) {
  unauthorizedHandler = handler
}

/** 统一处理 openapi-fetch 之外的请求返回。 */
export function handleUnauthorizedResponse(response: Response, schemaPath = ''): boolean {
  if (response.status !== 401) return false
  unauthorizedHandler?.(schemaPath)
  return true
}

/** 最近一次键盘或鼠标操作的时间。 */
let lastActivity = 0
const activityListeners = new Set<() => void>()
/** 距最近一次操作不超过这个时长的请求算作管理员实际操作，用于续期会话。 */
const ACTIVITY_WINDOW = 60_000

// 单元测试在没有 window 的环境里加载本模块。
for (const type of typeof window === 'undefined' ? [] : ['pointerdown', 'keydown', 'wheel', 'touchstart']) {
  window.addEventListener(type, () => {
    lastActivity = Date.now()
    for (const listener of activityListeners) listener()
  }, { capture: true, passive: true })
}

/** 订阅键盘或鼠标操作，返回取消函数。 */
export function onUserActivity(listener: () => void): () => void {
  activityListeners.add(listener)
  return () => activityListeners.delete(listener)
}

/** 管理员最近是否在操作页面。后台定时刷新在无人操作时不续期会话。 */
export function userActive(): boolean {
  return Date.now() - lastActivity <= ACTIVITY_WINDOW
}

const csrf: Middleware = {
  onRequest({ request }) {
    if (csrfToken && !['GET', 'HEAD', 'OPTIONS'].includes(request.method)) {
      request.headers.set('X-CSRF-Token', csrfToken)
    }
    if (userActive()) request.headers.set('X-Session-Activity', '1')
    return request
  },
}

const unauthorized: Middleware = {
  onResponse({ response, schemaPath }) {
    handleUnauthorizedResponse(response, schemaPath)
    return response
  },
}

let mutationHandler: (() => void) | null = null

/** 注册写操作成功后的回调，用于刷新待应用变更这类依赖数据库内容的状态。 */
export function setMutationHandler(handler: () => void) {
  mutationHandler = handler
}

const mutation: Middleware = {
  onResponse({ request, response, schemaPath }) {
    if (response.ok && !['GET', 'HEAD', 'OPTIONS'].includes(request.method) && !schemaPath.startsWith('/api/auth/')) mutationHandler?.()
    return response
  },
}

export const api = createClient<paths>({
  baseUrl: new URL('.', document.baseURI).href.replace(/\/$/, ''),
  credentials: 'same-origin',
})
api.use(csrf, unauthorized, mutation)

/** Huma 返回的 RFC 9457 错误体。 */
export type ProblemDetail = Schemas['ErrorModel']

/** 把后端字段错误转换为 { 字段名: 提示 }，location 形如 body.new_password。 */
export function fieldErrors(problem: ProblemDetail | undefined): Record<string, string> {
  const out: Record<string, string> = {}
  for (const e of problem?.errors ?? []) {
    if (e.location?.startsWith('body.')) {
      out[e.location.slice(5)] = e.message ?? ''
    }
  }
  return out
}

export function problemMessage(problem: ProblemDetail | undefined, fallback = '请求失败'): string {
  return problem?.detail || problem?.title || fallback
}

/** 后端返回的凭据占位，保存时原样提交表示保持原值。 */
export const MASKED = '••••••'

/** 422 响应拆成字段错误（路径相对请求体）和无法定位到字段的错误。 */
export interface FormErrors {
  fields: Record<string, string>
  global: string[]
}

export function splitProblem(problem: ProblemDetail | undefined, fallback = '保存失败'): FormErrors {
  const out: FormErrors = { fields: {}, global: [] }
  for (const e of problem?.errors ?? []) {
    if (e.location?.startsWith('body.')) {
      const path = e.location.slice(5)
      out.fields[path] = out.fields[path] ? `${out.fields[path]}；${e.message ?? ''}` : (e.message ?? '')
    } else if (e.message) {
      out.global.push(e.message)
    }
  }
  if (!problem?.errors?.length) {
    out.global.push(problemMessage(problem, fallback))
  }
  return out
}

export function emptyErrors(): FormErrors {
  return { fields: {}, global: [] }
}

/** NFormItem 的错误状态与提示。 */
export function fieldState(errors: FormErrors, path: string, hint?: string) {
  const message = errors.fields[path]
  return {
    validationStatus: message ? ('error' as const) : undefined,
    feedback: message || hint,
  }
}

/** 某个路径前缀下是否有错误（用于标签页和表格行标记）。 */
export function hasErrorsUnder(errors: FormErrors, prefix: string): boolean {
  return Object.keys(errors.fields).some((path) => path === prefix || path.startsWith(prefix + '.') || path.startsWith(prefix + '['))
}

/** 下载 Blob 为文件。 */
export function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
