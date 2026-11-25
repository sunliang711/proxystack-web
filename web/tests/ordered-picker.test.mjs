import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createRenderer, createSSRApp, h, nextTick, reactive, ssrContextKey } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

let server
let OrderedPicker

before(async () => {
  server = await createServer({ configFile: false, plugins: [vue()], server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ default: OrderedPicker } = await server.ssrLoadModule('/src/components/OrderedPicker.vue'))
})
after(async () => { await server?.close() })

function setup(t, initial, options = ['a', 'b', 'c', 'd']) {
  // 挂载真实组件逻辑及 v-model 回传，仅省略 UI 库的 DOM 渲染。
  const renderer = createRenderer({
    createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null,
  })
  const model = reactive({ value: [...initial] })
  const component = { ...OrderedPicker, render: () => null }
  let node
  const app = renderer.createApp({
    render: () => (node = h(component, { options, value: model.value, 'onUpdate:value': (value) => { model.value = value } })),
  })
  app.provide(ssrContextKey, {})
  app.mount({})
  t.after(() => app.unmount())
  return { model, state: node.component.setupState }
}

test('置顶和置底将指定项移至列表首尾并保持其余项相对顺序', async (t) => {
  const { model, state } = setup(t, ['a', 'b', 'c', 'd'])
  state.moveTop(2)
  await nextTick()
  assert.deepEqual(model.value, ['c', 'a', 'b', 'd'])

  state.moveBottom(1)
  await nextTick()
  assert.deepEqual(model.value, ['c', 'b', 'd', 'a'])

  // 已在首尾时置顶/置底不产生变化
  state.moveTop(0)
  await nextTick()
  assert.deepEqual(model.value, ['c', 'b', 'd', 'a'])
  state.moveBottom(3)
  await nextTick()
  assert.deepEqual(model.value, ['c', 'b', 'd', 'a'])
})

test('上移、下移、添加和移除在列表边界正常生效', async (t) => {
  const { model, state } = setup(t, ['b'])
  state.moveTop(0)
  state.moveBottom(0)
  await nextTick()
  assert.deepEqual(model.value, ['b'])

  state.add('d')
  await nextTick()
  state.add('a')
  await nextTick()
  assert.deepEqual(model.value, ['b', 'd', 'a'])

  state.move(2, -1)
  await nextTick()
  assert.deepEqual(model.value, ['b', 'a', 'd'])

  state.move(0, 1)
  await nextTick()
  assert.deepEqual(model.value, ['a', 'b', 'd'])

  state.remove(1)
  await nextTick()
  assert.deepEqual(model.value, ['a', 'd'])
})

test('每行按顺序渲染置顶、上移、下移、置底、移除按钮并在首尾禁用对应操作', async (t) => {
  const prevDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
  const el = () => ({ style: {}, dataset: {}, setAttribute() {}, appendChild: (child) => child, insertBefore: (child) => child, querySelector: () => null, classList: { add() {}, remove() {} }, textContent: '' })
  globalThis.document = { baseURI: 'http://localhost/', documentElement: el(), head: el(), createElement: el, createTextNode: el, querySelector: () => null }
  t.after(() => {
    if (prevDocument) Object.defineProperty(globalThis, 'document', prevDocument)
    else delete globalThis.document
  })

  const app = createSSRApp({
    render: () => h(OrderedPicker, { value: ['hk', 'jp', 'us'], options: ['hk', 'jp', 'us'] }),
  })
  const html = await renderToString(app)
  const buttons = [...html.matchAll(/<button\b([^>]*)>[\s\S]*?<span class="n-button__content">([\s\S]*?)<\/span>[\s\S]*?<\/button>/g)].map((m) => ({
    label: m[2].replace(/<!--[\s\S]*?-->|<[^>]+>/g, '').trim(),
    disabled: /\bn-button--disabled\b/.test(m[1]) && /(?:^|\s)disabled(?==|\s|$)/.test(m[1]),
  }))
  assert.deepEqual(buttons, [
    // 第 1 项 hk：置顶、上移禁用
    { label: '置顶', disabled: true },
    { label: '上移', disabled: true },
    { label: '下移', disabled: false },
    { label: '置底', disabled: false },
    { label: '移除', disabled: false },
    // 第 2 项 jp：全部可用
    { label: '置顶', disabled: false },
    { label: '上移', disabled: false },
    { label: '下移', disabled: false },
    { label: '置底', disabled: false },
    { label: '移除', disabled: false },
    // 第 3 项 us：下移、置底禁用
    { label: '置顶', disabled: false },
    { label: '上移', disabled: false },
    { label: '下移', disabled: true },
    { label: '置底', disabled: true },
    { label: '移除', disabled: false },
  ])
})
