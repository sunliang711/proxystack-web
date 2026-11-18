import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { after, before, test } from 'node:test'
import { createSSRApp, effectScope, reactive, ssrContextKey } from 'vue'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const require = createRequire(import.meta.url)
let server
let UpstreamsTab
let RulesTab
let DirectOverrides
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
  ;({ default: RulesTab } = await server.ssrLoadModule('/src/views/stack/RulesTab.vue'))
  ;({ default: DirectOverrides } = await server.ssrLoadModule('/src/views/stack/DirectOverrides.vue'))
  ;({ messageApiInjectionKey: messageKey } = require('naive-ui/lib/message/src/context'))
})

after(async () => {
  await server?.close()
  restoreGlobals()
})

function setupComponent(t, Component, propsData) {
  const scope = effectScope()
  t.after(() => scope.stop())
  const props = reactive(propsData)
  const app = createSSRApp({ render: () => null })
  app.provide(ssrContextKey, {})
  app.provide(messageKey, { success() {}, warning() {}, error() {}, info() {} })
  return app.runWithContext(() => scope.run(() => Component.setup(props, { expose() {} })))
}

function makeStack() {
  return {
    id: 1,
    name: 'edge1',
    direct_users: ['alice'],
    direct_suffix: ' [DRT]',
    direct_overrides: [],
    mihomo: {
      upstreams: [
        { name: 'node-a', enabled: true, type: 'raw', stack: '', inbound: '', config: { type: 'vmess', server: 'a.example.com', port: 443 } },
        { name: 'node-b', enabled: false, type: 'raw', stack: '', inbound: '', config: { type: 'socks5', server: 'b.example.com', port: 1080 } },
      ],
      groups: [{ name: 'AllProxy', type: 'select', proxies: ['node-a', 'node-b', 'DIRECT'] }],
      rules: { profile: 'default', final: 'AllProxy', extra: [] },
    },
  }
}

test('上游列表支持切换启用状态，新建上游默认启用，直连统计排除已停用上游', (t) => {
  const stack = makeStack()
  const state = setupComponent(t, UpstreamsTab, { stack, summaries: [], users: [{ name: 'alice' }], errors: { fields: {}, global: [] } })

  assert.equal(state.externalCount.value, 1)
  assert.equal(state.directNameExample.value, 'edge1 vmess [DRT]')

  state.setEnabled(1, true)
  assert.equal(stack.mihomo.upstreams[1].enabled, true)
  assert.equal(state.externalCount.value, 2)
  assert.equal(state.directNameExample.value, 'edge1 vmess · 上游名 [DRT]')

  state.setEnabled(0, false)
  assert.equal(stack.mihomo.upstreams[0].enabled, false)
  assert.equal(state.externalCount.value, 1)

  // 新增上游默认 enabled=true，编辑旧数据时也规范化为 boolean。
  state.open(stack.mihomo.upstreams.length)
  assert.equal(state.editing.value.draft.enabled, true)
  state.open(0)
  assert.equal(state.editing.value.draft.enabled, false)
})

test('规则目标和直连覆盖下拉保留已停用上游并标明状态', (t) => {
  const stack = makeStack()
  const rules = setupComponent(t, RulesTab, { stack, errors: { fields: {}, global: [] } })
  const overrides = setupComponent(t, DirectOverrides, { stack, errors: { fields: {}, global: [] } })

  assert.deepEqual(rules.targets.value, [
    { label: 'node-a', value: 'node-a' },
    { label: 'node-b（已停用）', value: 'node-b' },
    { label: 'AllProxy', value: 'AllProxy' },
    { label: 'DIRECT', value: 'DIRECT' },
    { label: 'REJECT', value: 'REJECT' },
  ])
  assert.deepEqual(overrides.upstreamOptions.value, [
    { label: 'node-a', value: 'node-a' },
    { label: 'node-b（已停用）', value: 'node-b' },
  ])
})
