import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { after, before, test } from 'node:test'
import { createSSRApp, effectScope, nextTick, ssrContextKey } from 'vue'
import { routeLocationKey, routerKey } from 'vue-router'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const require = createRequire(import.meta.url)
let server
let StackEditView
const savedGlobals = new Map()

/** Install only the browser surfaces needed to load the existing page. */
function stubGlobal(name, value) {
  savedGlobals.set(name, Object.getOwnPropertyDescriptor(globalThis, name))
  globalThis[name] = value
}

before(async () => {
  const el = () => ({ style: {}, dataset: {}, setAttribute() {}, appendChild: (child) => child, classList: { add() {}, remove() {} }, textContent: '' })
  stubGlobal('window', { addEventListener() {}, matchMedia: () => ({ matches: false, addEventListener() {} }) })
  stubGlobal('Image', class {})
  stubGlobal('document', { baseURI: 'http://localhost/', documentElement: el(), head: el(), createElement: el, createTextNode: el })
  server = await createServer({ configFile: false, plugins: [vue()], server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ default: StackEditView } = await server.ssrLoadModule('/src/views/stack/StackEditView.vue'))
})

after(async () => {
  await server?.close()
  for (const [name, desc] of savedGlobals) {
    if (desc) Object.defineProperty(globalThis, name, desc)
    else delete globalThis[name]
  }
})

test('聚合与成员识别兼容新旧引用，并保留没有 SOCKS5 relay 的现有成员', async (t) => {
  const app = createSSRApp({ render: () => null })
  app.provide(ssrContextKey, {})
  app.provide(routeLocationKey, { params: { id: '1' }, query: {} })
  app.provide(routerKey, { push() {}, replace() {} })
  app.provide(require('naive-ui/lib/message/src/context').messageApiInjectionKey, { success() {}, warning() {}, error() {} })
  app.provide(require('naive-ui/lib/dialog/src/context').dialogApiInjectionKey, { warning() {} })
  const scope = effectScope()
  t.after(() => scope.stop())
  const state = app.runWithContext(() => scope.run(() => StackEditView.setup({}, { expose() {} })))
  state.summaries.value = [
    { id: 2, name: 'legacy', role: 'edge', inbounds: [{ name: 'relay', protocol: 'socks5' }] },
    { id: 3, name: 'vmess-only', role: 'edge', inbounds: [{ name: 'main', protocol: 'vmess' }] },
    { id: 4, name: 'not-a-member', role: 'edge', inbounds: [{ name: 'main', protocol: 'vmess' }] },
  ]
  state.stack.value = {
    id: 1, name: 'auto', role: 'auto', xray: { enabled: true, outbound: { type: 'mihomo' }, inbounds: [] },
    mihomo: { enabled: true, providers: [], groups: [], upstreams: [
      { name: 'socks', type: 'xray-socks5', stack: 'legacy', inbound: 'relay' },
      { name: 'vmess', type: 'xray-inbound', stack: 'vmess-only', inbound: 'main', user: 'alice' },
    ] },
  }
  await nextTick()
  assert.equal(state.relaysOnly.value, true)
  assert.equal(state.mixedAuto.value, false)
  assert.deepEqual(state.members.value, ['legacy', 'vmess-only'])
  assert.deepEqual(state.memberOptions.value.map((option) => option.value), ['legacy', 'vmess-only'])
  // Keep configured references selectable while the membership draft is edited.
  state.members.value = ['legacy']
  assert.deepEqual(state.memberOptions.value.map((option) => option.value), ['legacy', 'vmess-only'])
  state.members.value = ['legacy', 'vmess-only']
  assert.deepEqual(state.members.value, ['legacy', 'vmess-only'])
  state.stack.value.mihomo.upstreams.push({ name: 'remote', type: 'raw', enabled: true })
  assert.equal(state.relaysOnly.value, false)
  assert.equal(state.mixedAuto.value, true)
})
