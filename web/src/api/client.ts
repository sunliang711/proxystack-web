import createClient, { type Middleware } from 'openapi-fetch'
import type { components, paths } from './schema'

export type Schemas = components['schemas']

/** 面板挂在随机路径下，后端通过 <base href> 告知前端。 */
export const panelBase = new URL('.', document.baseURI).pathname

let csrfToken = ''

export function setCSRFToken(token: string) {
  csrfToken = token
}

const csrf: Middleware = {
  onRequest({ request }) {
    if (csrfToken && !['GET', 'HEAD', 'OPTIONS'].includes(request.method)) {
      request.headers.set('X-CSRF-Token', csrfToken)
    }
    return request
  },
}

export const api = createClient<paths>({
  baseUrl: new URL('.', document.baseURI).href.replace(/\/$/, ''),
  credentials: 'same-origin',
})
api.use(csrf)

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
