import { h, reactive } from 'vue'
import { NButton, type MessageApi } from 'naive-ui'
import { api, panelBase, setMutationHandler, type Schemas } from './api/client'

type StackRuntime = Schemas['StackRuntime']
type TaskEvent = Schemas['TaskEvent']

/** 运行状态与待应用变更，由 /api/events 事件流实时更新。 */
export const runtime = reactive({
  stacks: {} as Record<string, StackRuntime>,
  pendingCount: 0,
  /** 有待应用变更的 stack 名称。 */
  pendingStacks: [] as string[],
  pendingError: '',
  /** 待应用变更抽屉是否打开，保存提示里的入口也用它打开抽屉。 */
  pendingDrawer: false,
  connected: false,
})

const taskListeners = new Set<(event: TaskEvent) => void>()
const jobTaskListeners = new Set<(event: Schemas['JobTaskEvent']) => void>()

/** 定时任务执行事件，与后台作业分开标识，避免 ID 冲突。 */
export function onJobTaskEvent(listener: (event: Schemas['JobTaskEvent']) => void): () => void {
  jobTaskListeners.add(listener)
  return () => jobTaskListeners.delete(listener)
}

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
  source.addEventListener('job', (message) => {
    const data = JSON.parse((message as MessageEvent).data) as Schemas['JobTaskEvent']
    for (const listener of jobTaskListeners) listener(data)
  })
}

export function disconnectEvents() {
  source?.close()
  source = null
  runtime.connected = false
}

let pendingRequest: Promise<void> | null = null
let pendingAgain = false

/** 重新读取待应用变更；并发调用合并为一次请求，期间再有调用时结束后补查一次。 */
export function refreshPending(): Promise<void> {
  if (pendingRequest) {
    pendingAgain = true
    return pendingRequest
  }
  pendingRequest = (async () => {
    do {
      pendingAgain = false
      const { data } = await api.GET('/api/runtime/pending')
      runtime.pendingCount = data?.changes.length ?? 0
      runtime.pendingStacks = [...new Set(data?.changes.map((change) => change.stack))]
      runtime.pendingError = data?.error ?? ''
    } while (pendingAgain)
  })().finally(() => (pendingRequest = null))
  return pendingRequest
}

// 任何写操作成功后都可能产生或消除待应用变更，徽标随之刷新。
setMutationHandler(() => void refreshPending())

/** 待应用变更的简短说明，例如“2 个 Stack 共 5 项待应用”。 */
export function pendingSummary(): string {
  if (runtime.pendingError) return '当前配置无法生成，打开查看原因'
  if (!runtime.pendingCount) return '没有待应用的变更'
  return `${runtime.pendingStacks.length} 个 Stack 共 ${runtime.pendingCount} 项待应用`
}

/**
 * 保存成功后的提示：等待应用的变更刷新完毕，有待应用变更时提醒需要应用并提供查看入口。
 * immediate 是没有待应用变更时的提示文字。
 */
export async function notifySaved(message: MessageApi, immediate = '已保存') {
  await refreshPending()
  if (!runtime.pendingCount) {
    message.success(immediate)
    return
  }
  message.warning(
    () => h('span', [
      `已保存。当前 ${pendingSummary()}，应用后才会生成配置并重启相关服务。`,
      h(NButton, { text: true, type: 'primary', style: 'margin-left: 12px', onClick: () => (runtime.pendingDrawer = true) }, () => '查看待应用变更'),
    ]),
    { duration: 8000, closable: true },
  )
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
