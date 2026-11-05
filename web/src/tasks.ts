import { reactive } from 'vue'
import { api, panelBase, problemMessage, type Schemas } from './api/client'
import { onJobTaskEvent, onTaskEvent } from './runtime'

export type CenterTask = Schemas['CenterTask']
export type TaskSource = CenterTask['source']
export type TaskReference = Pick<CenterTask, 'source' | 'id'>
type Notice = { kind: 'started' | 'completed'; task: CenterTask }
interface Requests {
  list(signal: AbortSignal): Promise<Schemas['TaskCenterPage']>
  detail(reference: TaskReference, signal?: AbortSignal): Promise<CenterTask>
}

export const taskKey = (task: TaskReference) => `${task.source}:${task.id}`
export const taskFailed = (task: CenterTask) => task.status === 'failed' || task.status === 'timeout'
export const taskRoute = (task: TaskReference) => ({ name: 'task-detail', params: { source: task.source, id: task.id } })

const requests: Requests = {
  async list(signal) {
    const { data, error } = await api.GET('/api/task-center', { params: { query: { limit: 50 } }, signal })
    if (!data) throw new Error(problemMessage(error, '任务列表加载失败'))
    return data
  },
  async detail(reference, signal) {
    const { data, error } = await api.GET('/api/task-center/{source}/{id}', { params: { path: reference }, signal })
    if (!data) throw new Error(problemMessage(error, '任务详情加载失败'))
    return data
  },
}

/** 全局任务状态随登录会话启动和清理；事件用于及时刷新，轮询用于断线补偿。 */
export function createTaskCenter(request: Requests = requests) {
  const state = reactive({ items: [] as CenterTask[], running: 0, loading: false, error: '', drawer: false,
    selected: null as TaskReference | null, unread: [] as string[] })
  const notices = new Set<(notice: Notice) => void>()
  const completed = new Set<(task: CenterTask) => void>()
  const known = new Map<string, CenterTask['status']>()
  const tracked = new Set<string>()
  const observed = new Set<string>()
  const delivered = new Set<string>()
  /** 最近一次按计划执行失败的定时任务；连续失败只提示第一次，成功后重新计数。 */
  const failingJobs = new Set<number>()
  let read = new Set<string>()
  let storageKey = ''
  let generation = 0
  let active = false
  let timer: ReturnType<typeof setInterval> | undefined
  let queued: ReturnType<typeof setTimeout> | undefined
  let controller: AbortController | null = null
  let pending: Promise<void> | null = null
  let unsubscribe: (() => void)[] = []

  function persistRead() {
    try { localStorage.setItem(storageKey, JSON.stringify([...read].slice(-500))) } catch { /* 存储不可用时仍可使用后台任务。 */ }
  }
  function markRead(reference?: TaskReference) {
    const keys = reference ? [taskKey(reference)] : state.unread
    for (const key of keys) read.add(key)
    state.unread = state.unread.filter((key) => !read.has(key))
    persistRead()
  }
  function accept(task: CenterTask) {
    const key = taskKey(task)
    const previous = known.get(key)
    known.set(key, task.status)
    if (task.status === 'running') observed.add(key)
    if (taskFailed(task) && !read.has(key) && !state.unread.includes(key)) state.unread.push(key)
    if (task.status !== 'running' && !delivered.has(key) && (tracked.has(key) || observed.has(key) || previous === 'running')) {
      delivered.add(key)
      tracked.delete(key)
      observed.delete(key)
      for (const listener of completed) listener(task)
      // 常规定时执行成功不逐条打扰；手动任务与失败仍给出提示。
      let notify = task.source === 'task' || task.actor === 'manual' || taskFailed(task)
      if (task.source === 'job' && task.actor === 'schedule') {
        if (!taskFailed(task)) failingJobs.delete(task.job_id)
        else if (failingJobs.has(task.job_id)) notify = false
        else failingJobs.add(task.job_id)
      }
      if (notify) {
        for (const listener of notices) listener({ kind: 'completed', task })
      }
    }
  }
  function refresh(): Promise<void> {
    if (!active) return Promise.resolve()
    if (pending) return pending
    const version = generation
    const current = new AbortController()
    controller = current
    state.loading = true
    pending = (async () => {
      try {
        const page = await request.list(current.signal)
        if (version !== generation) return
        state.items = page.items
        state.running = page.running
        state.error = ''
        for (const task of page.items) accept(task)
        // 列表分页之外的已提交任务也要追踪完成，避免大量并发时遗漏回调。
        for (const key of new Set([...tracked, ...observed])) {
          if (page.items.some((task) => taskKey(task) === key)) continue
          const [source, id] = key.split(':')
          try {
            const task = await request.detail({ source: source as TaskSource, id: Number(id) }, current.signal)
            if (version !== generation) return
            accept(task)
          } catch (cause) {
            if (version !== generation) return
            // 单条记录暂时不可用时，其他任务仍需继续追踪完成。
            state.error = cause instanceof Error ? cause.message : '任务状态刷新失败'
          }
        }
      } catch (cause) {
        if (version === generation) state.error = cause instanceof Error ? cause.message : '任务状态刷新失败'
      } finally {
        if (version === generation) {
          state.loading = false
          controller = null
          pending = null
        }
      }
    })()
    return pending
  }
  function handleEvent(source: TaskSource, event: { id: number; status: string; line?: string; progress?: unknown }) {
    if (!active) return
    // 只有阶段进度的事件不改变任务列表，不触发刷新。
    if (event.status === 'running' && event.progress && !event.line) return
    observed.add(taskKey({ source, id: event.id }))
    if (!queued) queued = setTimeout(() => { queued = undefined; void refresh() }, 300)
  }
  async function track(id: number) {
    if (!active) return
    const version = generation
    const reference: TaskReference = { source: 'task', id }
    tracked.add(taskKey(reference))
    const submitted: CenterTask = { ...reference, title: `后台任务 #${id}`, kind: '', actor: '', status: 'running', started_at: new Date().toISOString(), finished_at: '', log: [], error: '', job_id: 0 }
    for (const listener of notices) listener({ kind: 'started', task: submitted })
    let result: CenterTask | undefined
    try {
      const task = await request.detail(reference)
      if (version !== generation || !active) return
      accept(task)
      result = task
    } catch {
      // 提交已成功，暂时读不到详情时由下一次列表刷新继续追踪。
    }
    if (version === generation) void refresh()
    return result
  }
  function open(reference?: TaskReference) {
    state.selected = reference ?? null
    state.drawer = true
    if (reference && state.items.some((task) => taskKey(task) === taskKey(reference) && taskFailed(task))) markRead(reference)
    void refresh()
  }
  function stop() {
    active = false
    generation++
    clearInterval(timer)
    clearTimeout(queued)
    queued = undefined
    controller?.abort()
    controller = null
    pending = null
    for (const release of unsubscribe) release()
    unsubscribe = []
    known.clear()
    tracked.clear()
    observed.clear()
    delivered.clear()
    failingJobs.clear()
    state.items = []
    state.running = 0
    state.loading = false
    state.error = ''
    state.drawer = false
    state.selected = null
    state.unread = []
  }
  function start(username: string) {
    stop()
    active = true
    storageKey = `${panelBase}task-center-read:${username}`
    try {
      const value = JSON.parse(localStorage.getItem(storageKey) ?? '[]')
      read = new Set(Array.isArray(value) ? value.filter((key) => typeof key === 'string') : [])
    } catch { read = new Set() }
    unsubscribe = [onTaskEvent((event) => handleEvent('task', event)), onJobTaskEvent((event) => handleEvent('job', event))]
    timer = setInterval(() => { void refresh() }, 5000)
    void refresh()
  }
  return { state, start, stop, refresh, track, open, markRead, handleEvent,
    onNotice(listener: (notice: Notice) => void) { notices.add(listener); return () => notices.delete(listener) },
    onFinished(listener: (task: CenterTask) => void) { completed.add(listener); return () => completed.delete(listener) },
  }
}

export const taskCenter = createTaskCenter()
