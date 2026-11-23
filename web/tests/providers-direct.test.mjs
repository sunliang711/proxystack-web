import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { before, after, test } from 'node:test'
import { createSSRApp, effectScope, ssrContextKey } from 'vue'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const require = createRequire(import.meta.url)
let server
let ProvidersPanel
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
  ;({ default: ProvidersPanel } = await server.ssrLoadModule('/src/views/stack/ProvidersPanel.vue'))
  ;({ messageApiInjectionKey: messageKey } = require('naive-ui/lib/message/src/context'))
})
after(async () => {
  await server?.close()
  restoreGlobals()
})

function setup(t, providers, errors = { fields: {}, global: [] }) {
  const scope = effectScope()
  t.after(() => scope.stop())
  const stack = {
    id: 1,
    name: 'edge1',
    mihomo: {
      providers,
      groups: [{ name: 'Proxy', type: 'select', proxies: [], use: [] }],
      rules: { final: 'Proxy' },
    },
  }
  const app = createSSRApp({ render: () => null })
  app.provide(ssrContextKey, {})
  app.provide(messageKey, { warning() {}, error() {}, success() {}, info() {} })
  const state = app.runWithContext(() =>
    scope.run(() =>
      ProvidersPanel.setup(
        { stack, errors, dirty: false, savedVersion: 1, reveal: async () => {} },
        { expose() {} },
      ),
    ),
  )
  return { state, stack }
}

test('勾选和取消勾选订阅直连节点不会重复添加，且支持清空', (t) => {
  const { state, stack } = setup(t, [{ id: 10, name: 'airport', url: '••••••', fetch_mode: 'direct', auto_update: false, interval: 3600 }])
  state.nodesFor.value = 10
  state.toggleDirectNode('HK-1', true)
  state.toggleDirectNode('HK-1', true)
  state.toggleDirectNode('JP-2', true)
  assert.deepEqual(stack.mihomo.providers[0].direct_nodes, [{ name: 'HK-1' }, { name: 'JP-2' }])
  assert.equal(state.directNodeNames.value.size, 2)
  state.toggleDirectNode('HK-1', false)
  assert.deepEqual(stack.mihomo.providers[0].direct_nodes, [{ name: 'JP-2' }])
  state.clearDirectNodes()
  assert.deepEqual(stack.mihomo.providers[0].direct_nodes, [])
})

test('编辑订阅并暂存修改时保留直连节点，且能正确过滤与收集 direct_nodes 错误', (t) => {
  const { state, stack } = setup(
    t,
    [{ id: 10, name: 'airport', url: '••••••', fetch_mode: 'direct', auto_update: false, interval: 3600, direct_nodes: [{ name: 'HK-1' }, { name: 'Gone-1' }] }],
    { fields: { 'mihomo.providers[0].direct_nodes[1]': 'node "Gone-1" is no longer available' }, global: [] },
  )
  state.nodesFor.value = 10
  assert.deepEqual(state.activeDirectErrors.value, ['node "Gone-1" is no longer available'])
  assert.deepEqual(state.allDirectErrors.value, ['airport：node "Gone-1" is no longer available'])

  state.open(0)
  state.editing.value.draft.interval = 1800
  state.confirm()
  assert.equal(stack.mihomo.providers[0].interval, 1800)
  assert.deepEqual(stack.mihomo.providers[0].direct_nodes, [{ name: 'HK-1' }, { name: 'Gone-1' }])

  state.nodesLoading.value = false
  state.nodesError.value = ''
  state.providerNodes.value = []
  state.status.data.value = [{ id: 10, name: 'airport', running: true, applied: true, pending: false, updating: false, node_count: 0, nodes: [], last_attempt_at: 0, last_success_at: 0, last_error: '' }]
  assert.deepEqual(state.staleDirectNodes.value, [])

  state.providerNodes.value = [
    { key: 'HK-1', name: 'airport | HK-1', type: 'Vmess', server: '1.1.1.1', port: '443' },
    { key: 'JP-2', name: 'airport | JP-2', type: 'Socks5', server: '2.2.2.2', port: '1080' },
  ]
  assert.deepEqual(state.staleDirectNodes.value, [{ name: 'Gone-1' }])
  state.nodeFilter.value = 'jp-2'
  assert.deepEqual(state.filteredProviderNodes.value.map((item) => item.key), ['JP-2'])
  state.removeStaleDirectNodes()
  assert.deepEqual(stack.mihomo.providers[0].direct_nodes, [{ name: 'HK-1' }])
})
