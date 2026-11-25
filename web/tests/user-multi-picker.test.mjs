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
  { name: 'alice', remark: '开发组' },
  { name: 'bob', remark: '测试' },
  { name: 'carol', remark: '运维' },
  { name: 'dave', remark: '开发实习' },
]

function setup(t, initial = [], extraProps = {}) {
  const renderer = createRenderer({
    createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null,
  })
  const model = reactive({ value: [...initial] })
  const component = { ...UserMultiPicker, render: () => null }
  let node
  const app = renderer.createApp({
    render: () => (node = h(component, {
      users: sampleUsers,
      ...extraProps,
      value: model.value,
      'onUpdate:value': (value) => { model.value = value },
    })),
  })
  app.provide(ssrContextKey, {})
  app.mount({})
  t.after(() => app.unmount())
  return { model, state: node.component.setupState }
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
})
