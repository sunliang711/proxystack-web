import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { after, before, test } from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { createSSRApp, effectScope, nextTick, reactive, ssrContextKey } from 'vue'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const require = createRequire(import.meta.url)
let server
let UsageCard
let messageKey
let recordedRequests = []

const savedGlobals = {}
function stubGlobal(name, value) {
  savedGlobals[name] = Object.getOwnPropertyDescriptor(globalThis, name)
  globalThis[name] = value
}
function restoreGlobals() {
  for (const [name, desc] of Object.entries(savedGlobals)) {
    if (desc) Object.defineProperty(globalThis, name, desc)
    else delete globalThis[name]
  }
}

function emptyUsage() {
  return {
    today: { up: 0, down: 0 },
    period: { up: 0, down: 0 },
    period_start: '0001-01-01T00:00:00Z',
    next_reset: '0001-01-01T00:00:00Z',
    exempt: false,
    window_exempt_until: '0001-01-01T00:00:00Z',
    auto_pauses: [],
    by_stack: [{ stack_id: 3, stack: 'edge1', today: { up: 10, down: 20 }, recent: { up: 10, down: 20 }, daily: [] }],
    daily: [],
  }
}

before(async () => {
  const el = () => ({ style: {}, dataset: {}, setAttribute() {}, appendChild: (child) => child, classList: { add() {}, remove() {} }, textContent: '' })
  stubGlobal('window', { addEventListener() {}, matchMedia: () => ({ matches: false, addEventListener() {} }) })
  stubGlobal('Image', class {})
  stubGlobal('document', { baseURI: 'http://localhost/', documentElement: el(), head: el(), createElement: el, createTextNode: el })
  stubGlobal('fetch', async (input) => {
    const url = typeof input === 'string' ? input : input.url
    recordedRequests.push(url)
    let body
    if (url.includes('/api/config/settings')) body = { basic: { timezone: 'UTC' } }
    else if (url.includes('/usage')) body = emptyUsage()
    else body = { buckets: ['2026-11-18T00:00:00Z'], rows: [], timezone: 'UTC' }
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
  server = await createServer({ configFile: false, plugins: [vue()], server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ default: UsageCard } = await server.ssrLoadModule('/src/components/UsageCard.vue'))
  ;({ messageApiInjectionKey: messageKey } = require('naive-ui/lib/message/src/context'))
})

after(async () => {
  await server?.close()
  restoreGlobals()
})

test('用户详情用量柱状图默认按小时显示，支持切换条件并同步流量明细链接', async (t) => {
  recordedRequests = []
  const scope = effectScope()
  t.after(() => scope.stop())
  const props = reactive({ userId: 7, dailyLimit: 0, quota: 0 })
  const app = createSSRApp({ render: () => null })
  app.provide(ssrContextKey, {})
  app.provide(messageKey, { success() {}, warning() {}, error() {}, info() {} })
  const state = app.runWithContext(() => scope.run(() => UsageCard.setup(props, { expose() {}, emit() {} })))

  assert.equal(state.view.value, 'hourly')
  assert.equal(state.hours.value, 24)
  assert.deepEqual(state.trafficLink.value, { name: 'traffic', query: { level: 'hour', users: '7' } })

  // 等待初次 /api/users/{id}/usage、loadTimezone 与 /api/traffic/stats 异步完成，确认不会重复请求趋势。
  await setImmediate()
  await setImmediate()
  const statsCalls = () => recordedRequests.filter((url) => url.includes('/api/traffic/stats'))
  assert.ok(state.usage.value !== null)
  assert.equal(statsCalls().length, 1)
  const initialQuery = new URL(statsCalls()[0]).searchParams
  assert.equal(initialQuery.get('level'), 'hour')
  assert.equal(initialQuery.get('view'), 'stack')
  assert.equal(initialQuery.get('users'), '7')
  assert.equal(initialQuery.get('stacks'), '')

  // 后续刷新（如 10 秒轮询或手动刷新）得到新用量时同步刷新小时趋势。
  await state.load()
  await nextTick()
  await setImmediate()
  assert.equal(statsCalls().length, 2)

  // 筛选单个 Stack 后，趋势请求与流量明细链接都带上该 Stack。
  state.stackId.value = 3
  await nextTick()
  await setImmediate()
  assert.equal(statsCalls().length, 3)
  assert.equal(new URL(statsCalls()[2]).searchParams.get('stacks'), '3')
  assert.deepEqual(state.trafficLink.value, { name: 'traffic', query: { level: 'hour', users: '7', stacks: '3' } })

  // 切换到按天，链接粒度变为 day 且不再新发小时趋势请求。
  state.view.value = 'daily'
  await nextTick()
  await setImmediate()
  assert.equal(statsCalls().length, 3)
  assert.deepEqual(state.trafficLink.value, { name: 'traffic', query: { level: 'day', users: '7', stacks: '3' } })
})
