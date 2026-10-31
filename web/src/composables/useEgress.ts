import { computed, reactive } from 'vue'
import { api, problemMessage, type Schemas } from '../api/client'
import { runtime } from '../runtime'

type Egress = Schemas['EgressResult']

/** 一个 stack 的出口查询状态。 */
export interface EgressState {
  status: 'loading' | 'ok' | 'empty' | 'stopped' | 'error'
  info?: Egress
  error?: string
  /** 查询完成的时间戳（毫秒）。 */
  at?: number
}

interface StackRef {
  id: number
  name: string
}

// 结果在本次打开面板期间保留：切换页面再回来不会重复查询，刷新浏览器后重新查询。
const states = reactive<Record<number, EgressState>>({})

const CONCURRENCY = 4

/** mihomo 是否在运行；运行状态还没推送到时返回 undefined。 */
function mihomoRunning(name: string): boolean | undefined {
  const unit = runtime.stacks[name]?.units.find((item) => item.unit.includes('mihomo'))
  return unit ? unit.active === 'active' : undefined
}

async function query(stack: StackRef) {
  if (mihomoRunning(stack.name) === false) {
    states[stack.id] = { status: 'stopped', at: Date.now() }
    return
  }
  states[stack.id] = { ...states[stack.id], status: 'loading' }
  const { data, error } = await api.GET('/api/stacks/{id}/egress', {
    params: { path: { id: stack.id }, query: { family: 'ipv4', timeout: 6 } },
  })
  const at = Date.now()
  if (error) {
    const reason = error.errors?.[0]?.message ?? problemMessage(error)
    // 运行状态推送到之前就发出的查询，由后端的「not running」判定 mihomo 没在运行。
    const stopped = mihomoRunning(stack.name) === false || reason.endsWith('is not running')
    states[stack.id] = stopped ? { status: 'stopped', at } : { status: 'error', error: reason, at }
    return
  }
  const info = data?.[0]
  if (!info?.ip) {
    const reasons = info?.sources.filter((source) => source.error).map((source) => `${source.url}：${source.error}`) ?? []
    states[stack.id] = { status: 'empty', info, error: reasons.join('\n') || '数据源没有返回地址', at }
    return
  }
  states[stack.id] = { status: 'ok', info, at }
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
