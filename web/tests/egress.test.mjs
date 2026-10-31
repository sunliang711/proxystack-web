import assert from 'node:assert/strict'
import { before, after, afterEach, test } from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { createServer } from 'vite'
import 'vue'

let server
let api
let runtime
let egress
let originalGet
const originalDocument = globalThis.document
const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
const cachedValues = new Map()
const storage = {
  getItem: (key) => cachedValues.get(key) ?? null,
  setItem: (key, value) => cachedValues.set(key, value),
}

before(async () => {
  // 通过现有 Vite 加载真实 composable，仅替换 HTTP 边界，不增加测试框架。
  globalThis.document = { baseURI: 'http://localhost/' }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, writable: true, value: storage })
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ api } = await server.ssrLoadModule('/src/api/client.ts'))
  ;({ runtime } = await server.ssrLoadModule('/src/runtime.ts'))
  const { useEgress } = await server.ssrLoadModule('/src/composables/useEgress.ts')
  egress = useEgress()
  originalGet = api.GET
})

afterEach(() => {
  for (const id of Object.keys(egress.states)) delete egress.states[id]
  runtime.stacks = {}
  api.GET = originalGet
  cachedValues.clear()
  globalThis.localStorage = storage
})

after(async () => {
  await server?.close()
  if (originalDocument === undefined) delete globalThis.document
  else globalThis.document = originalDocument
  if (originalStorage === undefined) delete globalThis.localStorage
  else Object.defineProperty(globalThis, 'localStorage', originalStorage)
})

function controlledRequests() {
  const requests = []
  api.GET = (_path, { params }) => new Promise((resolve, reject) => {
    requests.push({ id: params.path.id, family: params.query.family, resolve, reject })
  })
  return requests
}

function result(family, ip) {
  return { data: [{ family, ip, region: '', city: '', country: '', org: '', sources: [] }] }
}

for (const [first, second] of [['ipv4', 'ipv6'], ['ipv6', 'ipv4']]) {
  test(`${first} 先返回时立即可见，${second} 继续加载`, async () => {
    const requests = controlledRequests()
    const done = egress.refreshOne({ id: 1, name: 'edge1' })
    assert.deepEqual(requests.map(request => request.family), ['ipv4', 'ipv6'])
    const key = first === 'ipv4' ? 'v4' : 'v6'
    const pendingKey = second === 'ipv4' ? 'v4' : 'v6'
    const address = first === 'ipv4' ? '203.0.113.1' : '2001:db8::1'
    requests.find(request => request.family === first).resolve(result(first, address))
    await setImmediate()
    assert.equal(egress.states[1][key].ip, address)
    assert.equal(egress.states[1].loading[key], false)
    assert.equal(egress.states[1].loading[pendingKey], true)
    assert.equal(egress.states[1].status, 'loading')
    assert.equal(egress.refreshing.value, true)
    requests.find(request => request.family === second).resolve(result(second, second === 'ipv4' ? '203.0.113.2' : '2001:db8::2'))
    await done
    assert.equal(egress.states[1].status, 'ok')
    assert.equal(egress.refreshing.value, false)
  })
}

test('一侧网络失败不清除另一侧已经返回的地址', async () => {
  const requests = controlledRequests()
  const done = egress.refreshOne({ id: 2, name: 'edge2' })
  requests[1].resolve(result('ipv6', '2001:db8::2'))
  await setImmediate()
  requests[0].reject(new Error('Network unavailable'))
  await done
  assert.equal(egress.states[2].status, 'ok')
  assert.equal(egress.states[2].v6.ip, '2001:db8::2')
  assert.equal(egress.states[2].errors.v4, 'Network unavailable')
  assert.equal(egress.states[2].loading.v4, false)
  assert.equal(egress.refreshing.value, false)
})

test('刷新时保留旧地址，单侧刷新失败后清除旧值', async () => {
  egress.states[3] = { status: 'ok', v4: result('ipv4', '203.0.113.3').data[0], v6: result('ipv6', '2001:db8::3').data[0] }
  const requests = controlledRequests()
  const done = egress.refreshOne({ id: 3, name: 'edge3' })
  assert.equal(egress.states[3].v4.ip, '203.0.113.3')
  requests[1].resolve(result('ipv6', '2001:db8::4'))
  await setImmediate()
  assert.equal(egress.states[3].v6.ip, '2001:db8::4')
  assert.equal(egress.states[3].loading.v4, true)
  requests[0].resolve({ error: { detail: 'IPv4 query failed' } })
  await done
  assert.equal(egress.states[3].v4, undefined)
  assert.equal(egress.states[3].v6.ip, '2001:db8::4')
  assert.equal(egress.states[3].status, 'ok')
})

test('同一 Stack 的重复刷新复用正在执行的两项请求', async () => {
  const requests = controlledRequests()
  const stack = { id: 4, name: 'edge4' }
  const first = egress.refreshOne(stack)
  const second = egress.refreshOne(stack)
  assert.equal(first, second)
  assert.equal(requests.length, 2)
  for (const request of requests) request.resolve(result(request.family, ''))
  await Promise.all([first, second])
  assert.equal(egress.states[4].status, 'empty')
})

test('批量最多并发四个 Stack，失败不会阻塞后续 Stack', async () => {
  const requests = controlledRequests()
  const stacks = Array.from({ length: 5 }, (_, index) => ({ id: index + 10, name: `edge${index + 10}` }))
  const done = egress.refresh(stacks)
  assert.equal(requests.length, 8)
  assert.deepEqual([...new Set(requests.map(request => request.id))], [10, 11, 12, 13])
  requests[0].reject(new Error('Network unavailable'))
  requests[1].resolve({ error: { detail: 'IPv6 query failed' } })
  await setImmediate()
  assert.equal(egress.states[10].status, 'error')
  assert.equal(requests.length, 10)
  assert.equal(requests[8].id, 14)
  for (const request of requests.slice(2)) request.resolve(result(request.family, ''))
  await done
  assert.equal(egress.refreshing.value, false)
})

test('Mihomo 停止时不发查询请求', async () => {
  const requests = controlledRequests()
  runtime.stacks.edge20 = { units: [{ unit: 'psw-mihomo@edge20.service', active: 'inactive' }] }
  await egress.refreshOne({ id: 20, name: 'edge20' })
  assert.equal(requests.length, 0)
  assert.equal(egress.states[20].status, 'stopped')
})

test('后端报告服务停止时结束加载并显示停止状态', async () => {
  const requests = controlledRequests()
  const done = egress.refreshOne({ id: 21, name: 'edge21' })
  requests[0].resolve({ error: { detail: 'mihomo edge21 is not running' } })
  requests[1].resolve({ error: { detail: 'mihomo edge21 is not running' } })
  await done
  assert.equal(egress.states[21].status, 'stopped')
  assert.equal(egress.refreshing.value, false)
})

test('首次查询写入本地缓存，清空内存后再次进入不请求后端', async () => {
  const stack = { id: 30, name: 'edge30' }
  const requests = controlledRequests()
  const done = egress.ensure([stack])
  assert.equal(requests.length, 2)
  for (const request of requests) request.resolve(result(request.family, request.family === 'ipv4' ? '203.0.113.30' : '2001:db8::30'))
  await done
  const at = egress.states[30].at
  assert.equal(cachedValues.size, 1)
  delete egress.states[30]
  await egress.ensure([stack])
  assert.equal(requests.length, 2)
  assert.equal(egress.states[30].v4.ip, '203.0.113.30')
  assert.equal(egress.states[30].v6.ip, '2001:db8::30')
  assert.equal(egress.states[30].at, at)
  assert.equal(egress.states[30].loading, undefined)
  assert.equal(egress.refreshing.value, false)
})

test('手动单行和批量刷新均覆盖缓存，新 Stack 单独查询', async () => {
  const stack = { id: 31, name: 'edge31' }
  let address = '203.0.113.31'
  let calls = 0
  api.GET = async (_path, { params }) => {
    calls++
    return result(params.query.family, params.query.family === 'ipv4' ? address : '')
  }
  await egress.ensure([stack])
  address = '203.0.113.32'
  await egress.refreshOne(stack)
  delete egress.states[31]
  await egress.ensure([stack])
  assert.equal(calls, 4)
  assert.equal(egress.states[31].v4.ip, address)
  address = '203.0.113.33'
  await egress.refresh([stack])
  delete egress.states[31]
  await egress.ensure([stack, { id: 32, name: 'edge32' }])
  assert.equal(calls, 8)
  assert.equal(egress.states[31].v4.ip, address)
  assert.equal(egress.states[32].v4.ip, address)
})

test('同一个 ID 的 Stack 名称变化后不复用旧本地缓存', async () => {
  api.GET = async (_path, { params }) => result(params.query.family, '203.0.113.34')
  await egress.ensure([{ id: 34, name: 'old' }])
  delete egress.states[34]
  const requests = controlledRequests()
  const done = egress.ensure([{ id: 34, name: 'new' }])
  assert.equal(requests.length, 2)
  for (const request of requests) request.resolve(result(request.family, ''))
  await done
})

for (const cached of ['{broken', JSON.stringify({ status: 'loading', at: 1 }), JSON.stringify({ status: 'ok', at: 1, v4: { family: 'ipv4', ip: '203.0.113.1' } })]) {
  test(`无效缓存降级为重新查询：${cached}`, async () => {
    cachedValues.set('psw-egress:v1:/:35:edge35', cached)
    const requests = controlledRequests()
    const done = egress.ensure([{ id: 35, name: 'edge35' }])
    assert.equal(requests.length, 2)
    for (const request of requests) request.resolve(result(request.family, ''))
    await done
    assert.equal(egress.states[35].status, 'empty')
  })
}

test('存储不可用时仍可查询并复用内存结果', async () => {
  globalThis.localStorage = {
    getItem: () => { throw new Error('Storage unavailable') },
    setItem: () => { throw new Error('Storage unavailable') },
  }
  const stack = { id: 36, name: 'edge36' }
  const requests = controlledRequests()
  const done = egress.ensure([stack])
  for (const request of requests) request.resolve(result(request.family, '203.0.113.36'))
  await done
  await egress.ensure([stack])
  assert.equal(requests.length, 2)
  assert.equal(egress.states[36].status, 'ok')
})

test('刷新失败后缓存新状态，重新进入不会显示旧地址或自动重试', async () => {
  const stack = { id: 37, name: 'edge37' }
  api.GET = async (_path, { params }) => result(params.query.family, '203.0.113.37')
  await egress.ensure([stack])
  const requests = controlledRequests()
  const done = egress.refreshOne(stack)
  for (const request of requests) request.reject(new Error('Network unavailable'))
  await done
  delete egress.states[37]
  await egress.ensure([stack])
  assert.equal(requests.length, 2)
  assert.equal(egress.states[37].status, 'error')
  assert.equal(egress.states[37].v4, undefined)
  assert.equal(egress.states[37].v6, undefined)
  assert.equal(egress.states[37].errors.v4, 'Network unavailable')
})
