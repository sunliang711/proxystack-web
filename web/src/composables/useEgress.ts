import { computed, reactive } from 'vue'
import { api, problemMessage, type Schemas } from '../api/client'
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

// 结果在本次打开面板期间保留：切换页面再回来不会重复查询，刷新浏览器后重新查询。
const states = reactive<Record<number, EgressState>>({})

const CONCURRENCY = 4
const inFlight = new Map<number, Promise<void>>()

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
  const request = runQuery(stack).finally(() => inFlight.delete(stack.id))
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

/** Stack 列表的出口 IP：ensure 只查询还没有结果的 stack，refresh 全部重新查询。 */
export function useEgress() {
  const refreshing = computed(() => Object.values(states).some((state) => state.status === 'loading'))
  return {
    states,
    refreshing,
    ensure: (stacks: StackRef[]) => queryAll(stacks.filter((stack) => !states[stack.id])),
    refresh: (stacks: StackRef[]) => queryAll(stacks),
    refreshOne: (stack: StackRef) => query(stack),
  }
}
