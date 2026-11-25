import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createRenderer, createSSRApp, h, nextTick, reactive, ssrContextKey } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

let server
let UserMultiPicker

before(async () => {
  server = await createServer({ configFile: false, plugins: [vue()], server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ default: UserMultiPicker } = await server.ssrLoadModule('/src/components/UserMultiPicker.vue'))
})
after(async () => { await server?.close() })

const sampleUsers = [
  { name: 'alice', remark: '开发组', profiles: [{ name: 'phone' }, { name: 'laptop' }] },
  { name: 'bob', remark: '测试', profiles: [] },
  { name: 'carol', remark: '运维', profiles: [{ name: 'office' }] },
  { name: 'dave', remark: '开发实习', profiles: [] },
]

function setup(t, initial = [], extraProps = {}) {
  const renderer = createRenderer({
    createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null,
  })
  const model = reactive({ value: [...initial] })
  const profileChanges = []
  const component = { ...UserMultiPicker, render: () => null }
  let node
  const app = renderer.createApp({
    render: () => (node = h(component, {
      users: sampleUsers,
      ...extraProps,
      value: model.value,
      'onUpdate:value': (value) => { model.value = value },
      'onUpdate:profile': (user, profile) => { profileChanges.push([user, profile]) },
    })),
  })
  app.provide(ssrContextKey, {})
  app.mount({})
  t.after(() => app.unmount())
  return { model, profileChanges, state: node.component.setupState }
}

test('支持点击切换、搜索过滤（用户名与备注）以及全部/已选/未选切换', async (t) => {
  const { model, state } = setup(t, ['alice'])
  assert.equal(state.selectedCount, 1)
  assert.deepEqual(state.visibleItems.map((u) => u.name), ['alice', 'bob', 'carol', 'dave'])

  state.toggle('carol')
  await nextTick()
  assert.deepEqual(model.value, ['alice', 'carol'])

  state.filter = 'selected'
  await nextTick()
  assert.deepEqual(state.visibleItems.map((u) => u.name), ['alice', 'carol'])

  state.filter = 'unselected'
  await nextTick()
  assert.deepEqual(state.visibleItems.map((u) => u.name), ['bob', 'dave'])

  // 按备注搜索并批量勾选/取消匹配项
  state.filter = 'all'
  state.query = '开发'
  await nextTick()
  assert.deepEqual(state.visibleItems.map((u) => u.name), ['alice', 'dave'])
  state.selectVisible()
  await nextTick()
  assert.deepEqual(model.value, ['alice', 'carol', 'dave'])

  state.clearVisible()
  await nextTick()
  assert.deepEqual(model.value, ['carol'])
})

test('支持全选、清空及自定义回调，并保留草稿中未知已选用户以便取消', async (t) => {
  let customAll = 0
  let customClear = 0
  const { model, state } = setup(t, ['alice', 'ghost'], {
    onSelectAll: () => { customAll++ },
    onClearAll: () => { customClear++ },
  })
  assert.ok(state.visibleItems.some((u) => u.name === 'ghost'))
  state.toggle('ghost')
  await nextTick()
  assert.deepEqual(model.value, ['alice'])

  state.selectAll()
  state.clearAll()
  assert.equal(customAll, 1)
  assert.equal(customClear, 1)

  const plain = setup(t, ['alice'])
  plain.state.selectAll()
  await nextTick()
  assert.deepEqual(plain.model.value, ['alice', 'bob', 'carol', 'dave'])
  plain.state.clearAll()
  await nextTick()
  assert.deepEqual(plain.model.value, [])
})

test('渲染多列勾选按钮与选中状态，并显示计数与操作区', async (t) => {
  const prevDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
  const el = () => ({ style: {}, dataset: {}, setAttribute() {}, appendChild: (child) => child, insertBefore: (child) => child, querySelector: () => null, classList: { add() {}, remove() {} }, textContent: '' })
  globalThis.document = { baseURI: 'http://localhost/', documentElement: el(), head: el(), createElement: el, createTextNode: el, querySelector: () => null }
  t.after(() => {
    if (prevDocument) Object.defineProperty(globalThis, 'document', prevDocument)
    else delete globalThis.document
  })

  const app = createSSRApp({
    render: () => h(UserMultiPicker, { value: ['alice', 'carol'], users: sampleUsers }),
  })
  const html = await renderToString(app)
  const items = [...html.matchAll(/<button\b([^>]*role="checkbox"[^>]*)>[\s\S]*?<span class="picker-name"[^>]*>([^<]+)<\/span>/g)].map((m) => ({
    name: m[2].trim(),
    checked: /aria-checked="true"/.test(m[1]),
  }))
  assert.deepEqual(items, [
    { name: 'alice', checked: true },
    { name: 'bob', checked: false },
    { name: 'carol', checked: true },
    { name: 'dave', checked: false },
  ])
  assert.match(html, /已选 2 \/ 共 4/)
  // 未传 selectedProfiles 时不渲染档案切换按钮
  assert.doesNotMatch(html, /class="picker-profile"/)
})

test('传入 selectedProfiles 时仅对已选且有额外档案（或非默认档案）的用户提供内联档案切换，并支持按档案名搜索', async (t) => {
  const { state, profileChanges } = setup(t, ['alice', 'bob'], {
    selectedProfiles: { alice: 'phone', bob: '', carol: '' },
  })
  // alice 已选且有额外档案 -> 显示；bob 无额外档案且为默认 -> 不显示；carol 有额外档案但未选 -> 不显示
  assert.equal(state.showProfilePicker(sampleUsers[0]), true)
  assert.equal(state.showProfilePicker(sampleUsers[1]), false)
  assert.equal(state.showProfilePicker(sampleUsers[2]), false)

  // 菜单包含 default 与所有档案，当前项带勾选标记
  assert.deepEqual(
    state.profileMenuOptions(sampleUsers[0]).map((o) => o.label),
    ['default', '✓ phone', 'laptop'],
  )

  state.selectProfile('alice', 'laptop')
  state.selectProfile('alice', state.profileMenuOptions(sampleUsers[0])[0].key)
  assert.deepEqual(profileChanges, [['alice', 'laptop'], ['alice', '']])

  // 按档案名搜索可命中对应用户
  state.query = 'office'
  await nextTick()
  assert.deepEqual(state.visibleItems.map((u) => u.name), ['carol'])

  // 渲染时仅为已选多档案用户渲染档案徽章，且非默认档案高亮 custom 类
  const prevDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
  const el = () => ({ style: {}, dataset: {}, setAttribute() {}, appendChild: (child) => child, insertBefore: (child) => child, querySelector: () => null, classList: { add() {}, remove() {} }, textContent: '' })
  globalThis.document = { baseURI: 'http://localhost/', documentElement: el(), head: el(), createElement: el, createTextNode: el, querySelector: () => null }
  t.after(() => {
    if (prevDocument) Object.defineProperty(globalThis, 'document', prevDocument)
    else delete globalThis.document
  })
  const app = createSSRApp({
    render: () => h(UserMultiPicker, {
      value: ['alice', 'bob', 'carol'],
      users: sampleUsers,
      selectedProfiles: { alice: 'phone', bob: '', carol: '' },
    }),
  })
  const html = await renderToString(app)
  const badges = [...html.matchAll(/<button\b([^>]*class="picker-profile[^"]*"[^>]*)>[\s\S]*?<span class="picker-profile-text"[^>]*>([^<]+)<\/span>/g)].map((m) => ({
    label: m[2].trim(),
    custom: /\bcustom\b/.test(m[1]),
  }))
  assert.deepEqual(badges, [
    { label: 'phone', custom: true },
    { label: 'default', custom: false },
  ])
})
