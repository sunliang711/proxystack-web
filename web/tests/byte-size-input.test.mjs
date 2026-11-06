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

test('流量额度的小数保存后重新加载仍按原单位显示，不出现长串小数', async (t) => {
  for (const [bytes, unit, amount] of [
    [Math.round(0.1 * GB), GB, 0.1],
    [Math.round(1.23 * TB), TB, 1.23],
    [500 * MB, MB, 500],
    [1000 * GB, GB, 1000],
    [Math.round(0.5 * MB), MB, 0.5],
  ]) {
    const { state } = setup(t, false, bytes)
    assert.equal(state.unit, unit, `${bytes}`)
    assert.equal(state.amount, amount, `${bytes}`)
  }
})

test('无法用两位小数准确还原的旧数据取数值不小于 1 的最大单位并保留两位小数', async (t) => {
  const { state } = setup(t, false, 123456789)
  assert.equal(state.unit, MB)
  assert.equal(state.amount, 117.74)
})

test('流量额度可选 MB / GB / TB，每日阈值可选 MB / GB，未设置时都默认 GB', async (t) => {
  assert.deepEqual(setup(t, false).state.units.map((u) => u.label), ['MB', 'GB', 'TB'])
  assert.deepEqual(setup(t, true).state.units.map((u) => u.label), ['MB', 'GB'])
  assert.equal(setup(t, false).state.unit, GB)
  assert.equal(setup(t, true).state.unit, GB)
})
