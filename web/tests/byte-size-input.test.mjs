import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createRenderer, h, nextTick, reactive, ssrContextKey } from 'vue'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

let server
let ByteSizeInput
before(async () => {
  server = await createServer({ configFile: false, plugins: [vue()], server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ default: ByteSizeInput } = await server.ssrLoadModule('/src/components/ByteSizeInput.vue'))
})
after(async () => { await server?.close() })

const MB = 1024 ** 2
const GB = 1024 ** 3
const TB = 1024 ** 4

function setup(t, small = false, bytes = 0) {
  // 挂载真实组件逻辑及 v-model 回传，仅省略 UI 库的 DOM 渲染。
  const renderer = createRenderer({
    createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null,
  })
  const model = reactive({ value: bytes })
  const component = { ...ByteSizeInput, render: () => null }
  let node
  const app = renderer.createApp({
    render: () => (node = h(component, { small, value: model.value, 'onUpdate:value': (value) => { model.value = value } })),
  })
  app.provide(ssrContextKey, {})
  app.mount({})
  t.after(() => app.unmount())
  return { model, state: node.component.setupState }
}

for (const [label, small, low, high] of [['流量额度', false, GB, TB], ['每日用量阈值', true, MB, GB]]) {
  test(`${label}切换单位保留整数及小数，并按新单位更新字节数`, async (t) => {
    const { model, state } = setup(t, small)
    for (const amount of [10, 1.23]) {
      state.updateAmount(amount)
      await nextTick()
      for (const unit of [high, low, high, low]) {
        state.updateUnit(unit)
        await nextTick()
        assert.equal(state.amount, amount)
        assert.equal(state.unit, unit)
        assert.equal(model.value, Math.round(amount * unit))
      }
    }
  })

  test(`${label}手选单位在零值、清空和后续输入时保持不变`, async (t) => {
    const { model, state } = setup(t, small)
    state.updateUnit(high)
    await nextTick()
    state.updateAmount(1.5)
    await nextTick()
    assert.equal(state.unit, high)
    assert.equal(state.amount, 1.5)
    assert.equal(model.value, Math.round(1.5 * high))
    state.updateAmount(null)
    await nextTick()
    assert.equal(state.unit, high)
    assert.equal(state.amount, 0)
    assert.equal(model.value, 0)
    state.updateAmount(2)
    await nextTick()
    assert.equal(state.unit, high)
    assert.equal(model.value, 2 * high)
  })

  test(`${label}正确显示初始值和外部重新加载的数值`, async (t) => {
    const { model, state } = setup(t, small, 2 * high)
    assert.equal(state.unit, high)
    assert.equal(state.amount, 2)
    model.value = 3 * low
    await nextTick()
    assert.equal(state.unit, low)
    assert.equal(state.amount, 3)
    model.value = 0
    await nextTick()
    assert.equal(state.amount, 0)
  })
}
