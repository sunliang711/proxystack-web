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

test('通用引用列出四种入站协议，多用户明确选择档案后才能暂存', (t) => {
  const stack = makeStack()
  const summaries = [{ name: 'target', inbounds: [
    { name: 'relay', protocol: 'socks5', listen: '127.0.0.1', port: 10001, users: [] },
    { name: 'http', protocol: 'http', listen: '127.0.0.1', port: 10002, users: [] },
    { name: 'vmess', protocol: 'vmess', listen: '127.0.0.1', port: 10003, users: [{ user: 'alice', profile: '' }, { user: 'alice', profile: 'mobile' }] },
    { name: 'ss', protocol: 'shadowsocks', listen: '127.0.0.1', port: 10004, users: [{ user: 'bob', profile: '' }] },
  ] }]
  const state = setupComponent(t, UpstreamsTab, { stack, summaries, users: [], errors: { fields: {}, global: [] } })
  assert.equal(state.cascaderOptions.value[0].children.length, 4)
  assert.match(state.cascaderOptions.value[0].children[2].label, /vmess/)
  state.open(stack.mihomo.upstreams.length)
  state.editing.value.draft.type = 'xray-inbound'
  state.editing.value.draft.name = 'reference'
  state.refValue.value = 'target\u0000vmess'
  assert.equal(state.referenceMissing.value, true)
  state.confirm()
  assert.equal(stack.mihomo.upstreams.length, 2)
  state.referenceUser.value = 'alice\u0000mobile'
  assert.equal(state.referenceMissing.value, false)
  state.confirm()
  assert.equal(stack.mihomo.upstreams[2].type, 'xray-inbound')
  assert.equal(stack.mihomo.upstreams[2].user, 'alice')
  assert.equal(stack.mihomo.upstreams[2].profile, 'mobile')
  assert.equal(stack.mihomo.upstreams[2].config, null)
})

test('切换引用清除旧凭据选择，单用户自动选择，HTTP 不要求用户', (t) => {
  const stack = makeStack()
  stack.mihomo.upstreams[0] = { name: 'reference', type: 'xray-inbound', stack: 'target', inbound: 'vmess', user: 'alice', profile: 'mobile' }
  const summaries = [{ name: 'target', inbounds: [
    { name: 'vmess', protocol: 'vmess', users: [{ user: 'alice', profile: 'mobile' }] },
    { name: 'ss', protocol: 'shadowsocks', users: [{ user: 'bob', profile: '' }] },
    { name: 'multi', protocol: 'vmess', users: [{ user: 'bob', profile: '' }, { user: 'alice', profile: '' }] },
    { name: 'http', protocol: 'http', users: [{ user: 'alice', profile: '' }] },
  ] }]
  const state = setupComponent(t, UpstreamsTab, { stack, summaries, users: [], errors: { fields: {}, global: [] } })
  state.open(0)
  assert.equal(state.referenceUser.value, 'alice\u0000mobile')
  state.refValue.value = 'target\u0000ss'
  assert.equal(state.referenceUser.value, 'bob\u0000default')
  assert.equal(state.editing.value.draft.profile, '')
  state.refValue.value = 'target\u0000multi'
  assert.equal(state.referenceUser.value, null)
  assert.equal(state.referenceMissing.value, true)
  state.refValue.value = 'target\u0000http'
  assert.equal(state.referenceNeedsUser.value, false)
  assert.equal(state.referenceUser.value, null)
  assert.equal(state.referenceMissing.value, false)
})

test('旧 SOCKS5 引用普通编辑保留类型，切换协议时升级为通用引用', (t) => {
  const stack = makeStack()
  stack.mihomo.upstreams[0] = { name: 'legacy', type: 'xray-socks5', stack: 'target', inbound: 'relay', config: null }
  const summaries = [{ name: 'target', inbounds: [
    { name: 'relay', protocol: 'socks5', users: [] },
    { name: 'vmess', protocol: 'vmess', users: [{ user: 'alice', profile: 'mobile' }] },
  ] }]
  const state = setupComponent(t, UpstreamsTab, { stack, summaries, users: [], errors: { fields: {}, global: [] } })
  state.open(0)
  assert.equal(state.editing.value.draft.type, 'xray-socks5')
  assert.equal(state.upstreamType.value, 'xray-inbound')
  assert.equal(state.refValue.value, 'target\u0000relay')
  assert.equal(state.referenceMissing.value, false)
  state.editing.value.draft.name = 'renamed'
  state.editing.value.draft.enabled = false
  state.confirm()
  assert.equal(stack.mihomo.upstreams[0].type, 'xray-socks5')
  assert.equal(stack.mihomo.upstreams[0].name, 'renamed')
  assert.equal(stack.mihomo.upstreams[0].enabled, false)
  state.open(0)
  state.refValue.value = 'target\u0000vmess'
  assert.equal(state.editing.value.draft.type, 'xray-inbound')
  assert.equal(state.referenceUser.value, 'alice\u0000mobile')
  state.confirm()
  assert.equal(stack.mihomo.upstreams[0].type, 'xray-inbound')
  state.open(0)
  state.editing.value.draft.user = 'alice'
  state.editing.value.draft.profile = 'mobile'
  state.upstreamType.value = 'raw'
  state.confirm()
  assert.equal(stack.mihomo.upstreams[0].stack, '')
  assert.equal(stack.mihomo.upstreams[0].inbound, '')
  assert.equal(stack.mihomo.upstreams[0].user, '')
  assert.equal(stack.mihomo.upstreams[0].profile, '')
})

test('无用户的 VMess 引用显示添加用户提示并阻止暂存', (t) => {
  const stack = makeStack()
  const summaries = [{ name: 'target', inbounds: [{ name: 'vmess', protocol: 'vmess', users: [] }] }]
  const state = setupComponent(t, UpstreamsTab, { stack, summaries, users: [], errors: { fields: {}, global: [] } })
  state.open(stack.mihomo.upstreams.length)
  state.upstreamType.value = 'xray-inbound'
  state.editing.value.draft.name = 'empty-vmess'
  state.refValue.value = 'target\u0000vmess'
  assert.equal(state.referenceNeedsUser.value, false)
  assert.equal(state.referenceMissing.value, true)
  assert.match(state.referenceUnavailable.value, /目标 VMess 入站尚未配置用户.*添加用户/)
  state.confirm()
  assert.equal(stack.mihomo.upstreams.length, 2)
  state.referenceInbound.value.users.push({ user: 'alice', profile: '' })
  assert.equal(state.referenceUnavailable.value, '')
})

test('引用目标列表显示连接用户及默认或指定档案', (t) => {
  const state = setupComponent(t, UpstreamsTab, { stack: makeStack(), summaries: [], users: [], errors: { fields: {}, global: [] } })
  const target = state.columns.value.find((column) => column.key === 'target')
  for (const profile of ['', 'mobile']) {
    const row = { type: 'xray-inbound', stack: 'target', inbound: 'vmess', user: 'alice', profile }
    const children = target.render(row).children.default()
    assert.equal(children[0].props.label, 'target.vmess')
    assert.equal(children[1].children.default(), `alice / ${profile || 'default'}`)
  }
})
