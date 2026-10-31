import { computed, reactive } from 'vue'
import { api, panelBase, problemMessage, type Schemas } from '../api/client'
import { runtime } from '../runtime'

type Egress = Schemas['EgressResult']
type FamilyKey = 'v4' | 'v6'

/** 一个 stack 的出口查询状态，IPv4 和 IPv6 同时查询。 */
export interface EgressState {
  status: 'loading' | 'ok' | 'empty' | 'stopped' | 'error'
  v4?: Egress
  v6?: Egress
  error?: string
  loading?: Record<FamilyKey, boolean>
  errors?: Partial<Record<FamilyKey, string>>
  /** 最近一次返回查询结果的时间戳（毫秒）。 */
  at?: number
}

interface StackRef {
  id: number
  name: string
}

// 查询结果同时保存在内存和浏览器本地，刷新页面后也可复用，由手动刷新更新。
const states = reactive<Record<number, EgressState>>({})

const CONCURRENCY = 4
const inFlight = new Map<number, Promise<void>>()

function cacheKey(stack: StackRef): string {
  // 同源下的不同面板、不同 stack 分别缓存，名称变化时重新查询。
  return `psw-egress:v1:${panelBase}:${stack.id}:${encodeURIComponent(stack.name)}`
}

function validResult(result: Egress | undefined, family: 'ipv4' | 'ipv6'): boolean {
  return result === undefined || !!result && result.family === family
    && ['ip', 'region', 'city', 'country', 'org'].every((key) => typeof result[key as keyof Egress] === 'string')
    && Array.isArray(result.sources)
    && result.sources.every((source) => source && ['ip', 'region', 'city', 'country', 'org', 'url', 'error']
      .every((key) => typeof source[key as keyof typeof source] === 'string'))
}

function readCache(stack: StackRef): EgressState | undefined {
  try {
    const state = JSON.parse(localStorage.getItem(cacheKey(stack)) ?? 'null') as EgressState | null
    if (!state || !['ok', 'empty', 'stopped', 'error'].includes(state.status)
      || typeof state.at !== 'number' || !Number.isFinite(state.at)
      || !validResult(state.v4, 'ipv4') || !validResult(state.v6, 'ipv6')
      || (state.error !== undefined && typeof state.error !== 'string')
      || (state.errors !== undefined && (!state.errors || typeof state.errors !== 'object'
        || Object.values(state.errors).some((error) => typeof error !== 'string')))) return
    // 只恢复已完成的结果，不恢复加载状态。
    return { status: state.status, v4: state.v4, v6: state.v6, error: state.error, errors: state.errors, at: state.at }
  } catch {
    // 缓存损坏或浏览器禁用存储时正常查询。
    return
  }
}

function writeCache(stack: StackRef) {
  try {
    const { loading, ...state } = states[stack.id]!
    localStorage.setItem(cacheKey(stack), JSON.stringify(state))
  } catch {
    // 浏览器禁用存储或空间不足时仍保留内存中的查询结果。
  }
}

/** mihomo 是否在运行；运行状态还没推送到时返回 undefined。 */
function mihomoRunning(name: string): boolean | undefined {
  const unit = runtime.stacks[name]?.units.find((item) => item.unit.includes('mihomo'))
  return unit ? unit.active === 'active' : undefined
}

async function runQuery(stack: StackRef) {
  if (mihomoRunning(stack.name) === false) {
    states[stack.id] = { status: 'stopped', at: Date.now() }
    return
  }
  states[stack.id] = { ...states[stack.id], status: 'loading', loading: { v4: true, v6: true }, errors: {}, error: undefined }
  const state = states[stack.id]!
  let stopped = false

  async function queryFamily(key: FamilyKey, family: 'ipv4' | 'ipv6') {
    try {
      const { data, error } = await api.GET('/api/stacks/{id}/egress', {
        params: { path: { id: stack.id }, query: { family, timeout: 6 } },
      })
      if (error) {
        const reason = error.errors?.[0]?.message ?? problemMessage(error)
        // 运行状态推送到之前就发出的查询，由后端的「not running」判定 mihomo 没在运行。
        stopped ||= mihomoRunning(stack.name) === false || reason.endsWith('is not running')
        state.errors![key] = reason
        state[key] = undefined
      } else {
        state[key] = data?.find((result) => result.family === family)
      }
    } catch (error) {
      state.errors![key] = error instanceof Error ? error.message : '网络请求失败'
      state[key] = undefined
    } finally {
      // 每个地址族独立更新；刷新失败时清掉它的旧地址，不影响另一项已经返回的结果。
      state.loading![key] = false
      state.at = Date.now()
    }
  }

  await Promise.all([queryFamily('v4', 'ipv4'), queryFamily('v6', 'ipv6')])
  if (stopped || mihomoRunning(stack.name) === false) {
    states[stack.id] = { status: 'stopped', at: state.at }
  } else if (state.v4?.ip || state.v6?.ip) {
    state.status = 'ok'
  } else {
    const reasons = [state.errors?.v4, state.errors?.v6, ...[...(state.v4?.sources ?? []), ...(state.v6?.sources ?? [])]
      .filter((source) => source.error)
      .map((source) => `${source.url}：${source.error}`)].filter(Boolean)
    state.status = state.errors?.v4 || state.errors?.v6 ? 'error' : 'empty'
    state.error = reasons.join('\n') || '数据源没有返回地址'
  }
}

function query(stack: StackRef): Promise<void> {
  const current = inFlight.get(stack.id)
  if (current) return current
  // 多个入口刷新同一个 stack 时复用查询，避免重复请求或旧结果覆盖新结果。
  const request = runQuery(stack).then(() => writeCache(stack)).finally(() => inFlight.delete(stack.id))
  inFlight.set(stack.id, request)
  return request
}

async function queryAll(stacks: StackRef[]) {
  const queue = [...stacks]
  const workers = Array.from({ length: Math.min(CONCURRENCY, queue.length) }, async () => {
    for (let stack = queue.shift(); stack; stack = queue.shift()) {
      await query(stack)
    }
  })
  await Promise.all(workers)
}

/** Stack 列表的出口 IP：ensure 优先读取本地缓存，refresh 重新查询并更新缓存。 */
export function useEgress() {
  const refreshing = computed(() => Object.values(states).some((state) => state.status === 'loading'))
  return {
    states,
    refreshing,
    ensure: (stacks: StackRef[]) => queryAll(stacks.filter((stack) => {
      if (!states[stack.id]) {
        const cached = readCache(stack)
        if (cached) states[stack.id] = cached
      }
      return !states[stack.id]
    })),
    refresh: (stacks: StackRef[]) => queryAll(stacks),
    refreshOne: (stack: StackRef) => query(stack),
  }
}
