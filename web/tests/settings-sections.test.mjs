import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { effectScope, nextTick } from 'vue'
import { createServer } from 'vite'

let server
let useSettingsSections
before(async () => {
  globalThis.document = { baseURI: 'http://localhost/panel/' }
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ useSettingsSections } = await server.ssrLoadModule('/src/composables/useSettingsSections.ts'))
})
after(async () => {
  await server?.close()
  delete globalThis.document
  delete globalThis.localStorage
})

function setup(t, saved = null) {
  const storage = new Map(saved === null ? [] : [['psw-settings-expanded:/panel/', saved]])
  globalThis.localStorage = { getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) }
  const scope = effectScope()
  t.after(() => scope.stop())
  return { state: scope.run(useSettingsSections), storage }
}

test('首次全部折叠，展开状态按面板路径保存并可重新读取', async (t) => {
  const { state, storage } = setup(t)
  assert.deepEqual(state.expanded.value, [])
  state.expanded.value = ['basic', 'notify', 'policy']
  await nextTick()
  assert.equal(storage.get('psw-settings-expanded:/panel/'), '["basic","notify","policy"]')
  const scope = effectScope()
  t.after(() => scope.stop())
  assert.deepEqual(scope.run(useSettingsSections).expanded.value, ['basic', 'notify', 'policy'])
})

test('忽略损坏缓存和失效分组，浏览器禁止存储时仍可展开', async (t) => {
  assert.deepEqual(setup(t, '{bad json').state.expanded.value, [])
  assert.deepEqual(setup(t, '["basic","unknown",12,"basic"]').state.expanded.value, ['basic'])
  globalThis.localStorage = { getItem() { throw new Error('Storage blocked') }, setItem() { throw new Error('Storage blocked') } }
  const scope = effectScope()
  t.after(() => scope.stop())
  const state = scope.run(useSettingsSections)
  assert.deepEqual(state.expanded.value, [])
  state.expanded.value = ['subscription']
  await nextTick()
  assert.deepEqual(state.expanded.value, ['subscription'])
})

test('保存校验错误自动展开对应分组及嵌套 policy，保留已展开分组', async (t) => {
  const { state } = setup(t, '["basic"]')
  state.revealErrors({ 'notify.host': '必填', 'defaults.xray.policy.stats_user_uplink': '必须启用', 'download_proxy.port': '无效', unknown: '未知' })
  await nextTick()
  assert.deepEqual(state.expanded.value, ['basic', 'notify', 'defaults', 'policy', 'download_proxy'])
})
