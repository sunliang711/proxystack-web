import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createRenderer, h, nextTick, reactive, ssrContextKey } from 'vue'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

let server
let SecretInput
const MASKED = '••••••'
before(async () => {
  globalThis.document = { baseURI: 'http://localhost/' }
  server = await createServer({ configFile: false, plugins: [vue()], server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ default: SecretInput } = await server.ssrLoadModule('/src/components/SecretInput.vue'))
})
after(async () => {
  await server?.close()
  delete globalThis.document
})

function setup(t, reveal, initialValue = MASKED) {
  // 挂载真实 v-model 和异步显示逻辑，省略 UI 库的 DOM 渲染。
  const renderer = createRenderer({
    createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null,
  })
  const model = reactive({ value: initialValue })
  const errors = []
  const component = { ...SecretInput, render: () => null }
  let node
  const app = renderer.createApp({
    render: () => (node = h(component, { value: model.value, reveal: () => reveal?.(model), 'onUpdate:value': (value) => { model.value = value } })),
  })
  app.provide(ssrContextKey, {})
  app.provide('n-message-api', { error: (text) => errors.push(text), warning: (text) => errors.push(text), success: () => {} })
  app.mount({})
  t.after(() => app.unmount())
  return { model, errors, state: node.component.setupState }
}

test('第一次点击取回并显示明文，再次隐藏和显示不重复请求', async (t) => {
  let calls = 0
  const { state, errors } = setup(t, async (model) => { calls++; model.value = 'test-secret' })
  assert.equal(state.visible, false)
  await state.toggleVisibility()
  assert.equal(state.visible, true)
  assert.equal(calls, 1)
  await state.toggleVisibility()
  assert.equal(state.visible, false)
  await state.toggleVisibility()
  assert.equal(state.visible, true)
  assert.equal(calls, 1)
  assert.deepEqual(errors, [])
})

test('请求期间重复点击不会重复获取凭据', async (t) => {
  let finish
  let calls = 0
  const { state } = setup(t, async (model) => {
    calls++
    await new Promise((resolve) => { finish = resolve })
    model.value = 'test-secret'
  })
  const pending = state.toggleVisibility()
  assert.equal(state.revealing, true)
  await state.toggleVisibility()
  assert.equal(calls, 1)
  finish()
  await pending
  assert.equal(state.visible, true)
  assert.equal(state.revealing, false)
})

test('获取异常或仍返回占位时继续遮蔽，允许重试', async (t) => {
  for (const fail of [() => { throw new Error('Request failed') }, () => {}]) {
    let failed = false
    const { state, errors } = setup(t, (model) => {
      if (!failed) { failed = true; return fail() }
      model.value = 'test-secret'
    })
    await state.toggleVisibility()
    assert.equal(state.visible, false)
    assert.equal(state.revealing, false)
    assert.equal(errors.length, 1)
    await state.toggleVisibility()
    assert.equal(state.visible, true)
  }
})

test('同一次获取回填其他字段时，只显示被点击的字段', async (t) => {
  const other = setup(t, undefined)
  const current = setup(t, (model) => {
    model.value = 'current-secret'
    other.model.value = 'other-secret'
  })
  await current.state.toggleVisibility()
  assert.equal(current.state.visible, true)
  assert.equal(other.state.visible, false)
  await other.state.toggleVisibility()
  assert.equal(other.state.visible, true)
})

test('新生成凭据直接切换显隐，保存后重新遮蔽时清除显示状态', async (t) => {
  let calls = 0
  const { model, state } = setup(t, () => { calls++ }, 'generated-secret')
  await state.toggleVisibility()
  assert.equal(state.visible, true)
  assert.equal(calls, 0)
  model.value = MASKED
  await nextTick()
  assert.equal(state.visible, false)
})

test('遮蔽状态下点击复制先取回明文再复制，字段保持隐藏', async (t) => {
  const copied = []
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'navigator')
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { clipboard: { writeText: async (text) => { copied.push(text) } } } })
  t.after(() => { if (previous) Object.defineProperty(globalThis, 'navigator', previous); else delete globalThis.navigator })
  let calls = 0
  const { state, errors } = setup(t, async (model) => { calls++; model.value = 'test-secret' })
  await state.copy()
  assert.equal(calls, 1)
  assert.deepEqual(copied, ['test-secret'])
  assert.equal(state.visible, false)
  assert.deepEqual(errors, [])
})
