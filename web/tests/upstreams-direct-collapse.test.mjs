import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { after, before, test } from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { createSSRApp, effectScope, nextTick, reactive, ssrContextKey } from 'vue'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const require = createRequire(import.meta.url)
let server
let UpstreamsTab
let messageKey

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

before(async () => {
  const el = () => ({ style: {}, dataset: {}, setAttribute() {}, appendChild: (child) => child, classList: { add() {}, remove() {} }, textContent: '' })
  stubGlobal('window', { addEventListener() {}, matchMedia: () => ({ matches: false, addEventListener() {} }) })
  stubGlobal('Image', class {})
  stubGlobal('document', { baseURI: 'http://localhost/', documentElement: el(), head: el(), createElement: el, createTextNode: el })
  server = await createServer({ configFile: false, plugins: [vue()], server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ default: UpstreamsTab } = await server.ssrLoadModule('/src/views/stack/UpstreamsTab.vue'))
  ;({ messageApiInjectionKey: messageKey } = require('naive-ui/lib/message/src/context'))
})

after(async () => {
  await server?.close()
  restoreGlobals()
})

function makeStack(id = 1, overrides = {}) {
  return {
    id,
    name: `stack-${id}`,
    direct_users: [],
    direct_suffix: ' [DRT]',
    direct_overrides: [],
    mihomo: { upstreams: [], groups: [], rules: { final: 'DIRECT', extra: [] } },
    ...overrides,
  }
}

function setup(t, { stack = makeStack(), users = [], errors = { fields: {}, global: [] } } = {}) {
  const scope = effectScope()
  t.after(() => scope.stop())
  const props = reactive({ stack, summaries: [], users, errors })
  const app = createSSRApp({ render: () => null })
  app.provide(ssrContextKey, {})
  app.provide(messageKey, { success() {}, warning() {}, error() {}, info() {} })
  const state = app.runWithContext(() => scope.run(() => UpstreamsTab.setup(props, { expose() {} })))
  return { state, props }
}

test('直连上游订阅默认折叠并安全显示摘要', (t) => {
  const empty = setup(t, {
    stack: makeStack(1, { direct_users: undefined, direct_overrides: undefined }),
  })
  assert.deepEqual(empty.state.directExpanded.value, [])
  assert.equal(empty.state.directHasErrors.value, false)
  assert.equal(empty.state.directSummary.value, '未启用')

  const { state } = setup(t, {
    stack: makeStack(2, { direct_users: ['alice', 'bob'], direct_overrides: [{ user: 'alice', upstream: 'u1', config: { uuid: 'x' } }] }),
    users: [{ name: 'alice' }, { name: 'bob' }],
  })
  assert.deepEqual(state.directExpanded.value, [])
  assert.equal(state.directHasErrors.value, false)
  assert.equal(state.directSummary.value, '已授权 2 人 · 1 条覆盖')
})

test('挂载时已有直连错误直接展开，且同 Stack 保存不重置手动展开状态', async (t) => {
  const { state, props } = setup(t, {
    errors: { fields: { 'direct_users[0]': 'unknown user' }, global: [] },
  })
  assert.equal(state.directHasErrors.value, true)
  assert.deepEqual(state.directExpanded.value, ['direct'])

  props.errors = { fields: {}, global: [] }
  props.stack = makeStack(1, { direct_users: ['alice'] })
  await nextTick()
  await setImmediate()
  assert.deepEqual(state.directExpanded.value, ['direct'])
})

test('直连校验错误触发展开（含手动收起后再次保存报错），切换 Stack 时恢复折叠', async (t) => {
  const { state, props } = setup(t)
  assert.deepEqual(state.directExpanded.value, [])

  props.errors = { fields: { direct_suffix: 'invalid' }, global: [] }
  await nextTick()
  assert.equal(state.directHasErrors.value, true)
  assert.deepEqual(state.directExpanded.value, ['direct'])

  // 用户手动收起后若再次保存仍报直连错误，应重新展开以暴露错误字段。
  state.directExpanded.value = []
  props.errors = { fields: { 'direct_overrides[0].config': 'bad' }, global: [] }
  await nextTick()
  assert.deepEqual(state.directExpanded.value, ['direct'])

  props.errors = { fields: {}, global: [] }
  props.stack = makeStack(2)
  await nextTick()
  assert.deepEqual(state.directExpanded.value, [])
})
