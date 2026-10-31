import { reactive } from 'vue'
import { api, panelBase, type Schemas } from './api/client'

type StackRuntime = Schemas['StackRuntime']
type TaskEvent = Schemas['TaskEvent']

/** 运行状态与待应用变更，由 /api/events 事件流实时更新。 */
export const runtime = reactive({
  stacks: {} as Record<string, StackRuntime>,
  pendingCount: 0,
  pendingError: '',
  connected: false,
})

const taskListeners = new Set<(event: TaskEvent) => void>()

/** 订阅作业进度事件，返回取消函数。 */
export function onTaskEvent(listener: (event: TaskEvent) => void): () => void {
  taskListeners.add(listener)
  return () => taskListeners.delete(listener)
}

const trafficListeners = new Set<(point: Schemas['LivePoint']) => void>()

/** 订阅实时流量事件，返回取消函数。 */
export function onTrafficEvent(listener: (point: Schemas['LivePoint']) => void): () => void {
  trafficListeners.add(listener)
  return () => trafficListeners.delete(listener)
}

let source: EventSource | null = null

export function connectEvents() {
  if (source) return
  source = new EventSource(panelBase + 'api/events')
  source.onopen = () => (runtime.connected = true)
  source.onerror = () => (runtime.connected = false)
  source.addEventListener('runtime', (message) => {
    const data = JSON.parse((message as MessageEvent).data) as Schemas['RuntimeEvent']
    runtime.stacks = Object.fromEntries(data.stacks.map((stack) => [stack.name, stack]))
  })
  source.addEventListener('traffic', (message) => {
    const data = JSON.parse((message as MessageEvent).data) as Schemas['LivePoint']
    for (const listener of trafficListeners) listener(data)
  })
  source.addEventListener('task', (message) => {
    const data = JSON.parse((message as MessageEvent).data) as TaskEvent
    for (const listener of taskListeners) listener(data)
    if (data.status !== 'running') refreshPending()
  })
}

export function disconnectEvents() {
  source?.close()
  source = null
  runtime.connected = false
}

export async function refreshPending() {
  const { data } = await api.GET('/api/runtime/pending')
  runtime.pendingCount = data?.changes.length ?? 0
  runtime.pendingError = data?.error ?? ''
}

/** unit 状态的显示文字和颜色。 */
export function unitLabel(unit: Schemas['UnitView']): { text: string; type: 'success' | 'error' | 'warning' | 'default' | 'info' } {
  if (!unit.present) return { text: '未生成', type: 'default' }
  switch (unit.active) {
    case 'active':
      return { text: '运行中', type: 'success' }
    case 'activating':
    case 'reloading':
      return { text: '启动中', type: 'info' }
    case 'failed':
      return { text: '失败', type: 'error' }
    case 'deactivating':
      return { text: '停止中', type: 'warning' }
    default:
      return { text: '已停止', type: 'default' }
  }
}
