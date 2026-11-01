import assert from 'node:assert/strict'
import { before, after, afterEach, test } from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { createServer } from 'vite'
import { effectScope } from 'vue'

let server
let createTaskCenter
let taskCenter
let useBackgroundTask
const centers = []
const originalDocument = globalThis.document
const originalStorage = globalThis.localStorage
const storage = new Map()
before(async () => {
  globalThis.document = { baseURI: 'http://localhost/' }
  globalThis.localStorage = { getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) }
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ createTaskCenter, taskCenter } = await server.ssrLoadModule('/src/tasks.ts'))
  ;({ useBackgroundTask } = await server.ssrLoadModule('/src/composables/useBackgroundTask.ts'))
})
afterEach(() => { for (const center of centers.splice(0)) center.stop(); storage.clear() })
after(async () => {
  await server?.close()
  if (originalDocument === undefined) delete globalThis.document
  else globalThis.document = originalDocument
  if (originalStorage === undefined) delete globalThis.localStorage
  else globalThis.localStorage = originalStorage
})

const item = (id, status = 'running', source = 'task') => ({ id, source, title: `任务 ${id}`, kind: 'apply', actor: 'admin', status,
  started_at: '2026-11-01T00:00:00Z', finished_at: '2026-11-01T00:01:00Z', log: [], error: '', job_id: 0 })
function harness(request) {
  const center = createTaskCenter(request)
  centers.push(center)
  center.start('admin')
  return center
}

test('多个来源的同号任务独立追踪，完成通知仅发送一次，原页面仍收到完成回调', async () => {
  let entries = [item(1), item(1, 'running', 'job')]
  const center = harness({ list: async () => ({ items: entries, running: 2, total: 2 }), detail: async ({ id }) => item(id) })
  const finished = []
  const notices = []
  center.onFinished((task) => finished.push(`${task.source}:${task.id}`))
  center.onNotice((notice) => notices.push(notice.kind))
  await setImmediate()
  entries = [item(1, 'succeeded'), item(1, 'failed', 'job')]
  await center.refresh()
  await center.refresh()
  assert.deepEqual(finished, ['task:1', 'job:1'])
  assert.deepEqual(notices, ['completed', 'completed'])
  assert.deepEqual([...center.state.unread], ['job:1'])
})

test('首次加载历史任务不弹完成提示，失败红点可标记已读并按账号保存', async () => {
  const request = { list: async () => ({ items: [item(4, 'failed')], running: 0, total: 1 }), detail: async () => item(4, 'failed') }
  const center = harness(request)
  const notices = []
  center.onNotice((notice) => notices.push(notice))
  await setImmediate()
  assert.equal(notices.length, 0)
  assert.equal(center.state.unread.length, 1)
  center.markRead({ source: 'task', id: 4 })
  center.start('admin')
  await setImmediate()
  assert.equal(center.state.unread.length, 0)
  center.start('other-admin')
  await setImmediate()
  assert.equal(center.state.unread.length, 1)
})

test('提交后瞬间完成的任务仍能通知，且任务不在当前列表页时继续单独追踪', async () => {
  let task = item(9)
  const center = harness({ list: async () => ({ items: [], running: 1, total: 60 }), detail: async () => task })
  await setImmediate()
  const finished = []
  center.onFinished((value) => finished.push(value.id))
  await center.track(9)
  await setImmediate()
  task = item(9, 'succeeded')
  await center.refresh()
  assert.deepEqual(finished, [9])
  task = item(10, 'succeeded')
  await center.track(10)
  assert.deepEqual(finished, [9, 10])
  assert.equal(center.state.drawer, false)
})

test('断线补偿轮询与事件刷新合并，慢请求期间不重复请求', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval', 'setTimeout'] })
  let calls = 0
  let resolve
  const center = harness({ list: () => { calls++; return new Promise((done) => { resolve = done }) }, detail: async () => item(1) })
  t.mock.timers.tick(20_000)
  center.handleEvent('task', { id: 1, status: 'running' })
  t.mock.timers.tick(300)
  assert.equal(calls, 1)
  resolve({ items: [item(1)], running: 1, total: 1 })
  await setImmediate()
  t.mock.timers.tick(5000)
  assert.equal(calls, 2)
  resolve({ items: [item(1, 'succeeded')], running: 0, total: 1 })
  await setImmediate()
  assert.equal(center.state.running, 0)
})

test('切换账号和离开布局后取消请求，旧会话的迟到响应不能覆盖新任务', async () => {
  const pending = []
  const center = harness({ list: (signal) => new Promise((resolve) => pending.push({ signal, resolve })), detail: async () => item(1) })
  center.start('other-admin')
  assert.equal(pending[0].signal.aborted, true)
  pending[1].resolve({ items: [item(2)], running: 1, total: 1 })
  await setImmediate()
  pending[0].resolve({ items: [item(1)], running: 9, total: 9 })
  await setImmediate()
  assert.equal(center.state.items[0].id, 2)
  assert.equal(center.state.running, 1)
  center.stop()
  assert.equal(center.state.items.length, 0)
})

test('刷新失败保留上次成功结果，恢复后清除错误；常规定时任务成功不弹消息', async () => {
  let failed = false
  let status = 'running'
  const center = harness({ list: async () => {
    if (failed) throw new Error('Network unavailable')
    return { items: [{ ...item(1, status, 'job'), actor: 'schedule' }], running: status === 'running' ? 1 : 0, total: 1 }
  }, detail: async () => item(1) })
  const notices = []
  center.onNotice((notice) => notices.push(notice))
  await setImmediate()
  failed = true
  await center.refresh()
  assert.equal(center.state.items.length, 1)
  assert.equal(center.state.error, 'Network unavailable')
  failed = false
  status = 'succeeded'
  await center.refresh()
  assert.equal(center.state.error, '')
  assert.equal(notices.length, 0)
})

test('已看到的运行任务离开最近列表后，仍能追踪完成和失败提醒', async () => {
  let entries = [item(12)]
  const center = harness({ list: async () => ({ items: entries, running: entries.length, total: 60 }), detail: async () => item(12, 'failed') })
  const finished = []
  center.onFinished((task) => finished.push(task.id))
  await setImmediate()
  center.open({ source: 'task', id: 12 })
  assert.equal(center.state.drawer, true)
  entries = []
  await setImmediate()
  await center.refresh()
  assert.deepEqual(finished, [12])
  assert.deepEqual([...center.state.unread], ['task:12'])
})

test('详情暂不可用时提交提示立即显示，后续刷新仍可通知完成', async () => {
  let entries = []
  const center = harness({ list: async () => ({ items: entries, running: 0, total: entries.length }), detail: async () => { throw new Error('Network unavailable') } })
  await setImmediate()
  const notices = []
  center.onNotice((notice) => notices.push(notice.kind))
  await center.track(13)
  assert.deepEqual(notices, ['started'])
  await setImmediate()
  entries = [item(13, 'succeeded')]
  await center.refresh()
  assert.deepEqual(notices, ['started', 'completed'])
})

test('单条任务详情读取失败不会阻断其他分页外任务的完成回调', async () => {
  let entries = [item(20), item(21)]
  const center = harness({ list: async () => ({ items: entries, running: 0, total: 60 }), detail: async ({ id }) => {
    if (id === 20) throw new Error('Task unavailable')
    return item(id, 'succeeded')
  } })
  await setImmediate()
  const finished = []
  center.onFinished((task) => finished.push(task.id))
  entries = []
  await center.refresh()
  assert.deepEqual(finished, [21])
  assert.equal(center.state.error, 'Task unavailable')
})

test('提交响应到达前已经完成的任务，原页面仍刷新一次', async (t) => {
  const result = item(30, 'succeeded')
  t.mock.method(taskCenter, 'track', async () => result)
  const statuses = []
  const scope = effectScope()
  const track = scope.run(() => useBackgroundTask((status) => statuses.push(status)))
  track(30)
  await setImmediate()
  assert.deepEqual(statuses, ['succeeded'])
  scope.stop()
})

test('完成事件和提交详情同时返回时不重复刷新，离开页面后也不刷新', async (t) => {
  let listener
  t.mock.method(taskCenter, 'onFinished', (callback) => { listener = callback; return () => {} })
  t.mock.method(taskCenter, 'track', async () => { listener(item(31, 'succeeded')); return item(31, 'succeeded') })
  const statuses = []
  const scope = effectScope()
  const track = scope.run(() => useBackgroundTask((status) => statuses.push(status)))
  track(31)
  await setImmediate()
  assert.deepEqual(statuses, ['succeeded'])
  let resolve
  t.mock.method(taskCenter, 'track', () => new Promise((done) => { resolve = done }))
  track(32)
  scope.stop()
  resolve(item(32, 'succeeded'))
  await setImmediate()
  assert.deepEqual(statuses, ['succeeded'])
})
