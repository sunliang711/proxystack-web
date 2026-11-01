import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { effectScope, nextTick, ref } from 'vue'
import { createServer } from 'vite'

let server
let usePolling
before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ usePolling } = await server.ssrLoadModule('/src/composables/usePolling.ts'))
})
after(async () => { await server?.close() })

function harness(t) {
  const source = ref(1)
  const requests = []
  const scope = effectScope()
  const state = scope.run(() => usePolling(source, (signal) => new Promise((resolve, reject) => {
    requests.push({ id: source.value, signal, resolve, reject })
  })))
  t.after(() => scope.stop())
  return { source, requests, scope, state }
}

test('立即获取统计，此后每 10 秒更新，慢请求期间不叠加请求', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  const { requests, state } = harness(t)
  assert.equal(requests.length, 1)
  requests[0].resolve({ total: 1 })
  await setImmediate()
  t.mock.timers.tick(9999)
  assert.equal(requests.length, 1)
  t.mock.timers.tick(1)
  assert.equal(requests.length, 2)
  t.mock.timers.tick(30_000)
  await state.refresh()
  assert.equal(requests.length, 2)
  requests[1].resolve({ total: 2 })
  await setImmediate()
  assert.equal(state.data.value.total, 2)
  assert.equal(state.loading.value, false)
  assert.ok(state.updatedAt.value > 0)
})

test('切换用户或 Stack 后取消旧请求，迟到的旧结果不能覆盖新对象', async (t) => {
  const { requests, state, source } = harness(t)
  source.value = 2
  await nextTick()
  assert.equal(requests[0].signal.aborted, true)
  assert.equal(requests[1].id, 2)
  requests[1].resolve({ total: 20 })
  await setImmediate()
  requests[0].resolve({ total: 10 })
  await setImmediate()
  assert.equal(state.data.value.total, 20)
  assert.equal(state.error.value, '')
  assert.equal(state.loading.value, false)
})

test('离开页面清理轮询并取消请求，迟到的结果不再更新组件', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  const { requests, scope, state } = harness(t)
  scope.stop()
  assert.equal(requests[0].signal.aborted, true)
  t.mock.timers.tick(30_000)
  assert.equal(requests.length, 1)
  requests[0].resolve({ total: 99 })
  await setImmediate()
  assert.equal(state.data.value, null)
})

test('刷新失败保留上次用量并显示错误，下一次成功后恢复', async (t) => {
  const { requests, state } = harness(t)
  requests[0].resolve({ total: 10 })
  await setImmediate()
  const failed = state.refresh()
  requests[1].reject(new Error('Network unavailable'))
  await failed
  assert.equal(state.data.value.total, 10)
  assert.equal(state.error.value, 'Network unavailable')
  assert.equal(state.loading.value, false)
  const recovered = state.refresh()
  requests[2].resolve({ total: 20 })
  await recovered
  assert.equal(state.data.value.total, 20)
  assert.equal(state.error.value, '')
})
