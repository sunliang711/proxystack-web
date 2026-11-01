import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createSSRApp, effectScope, ssrContextKey } from 'vue'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

let server
let InboundsTab
before(async () => {
  // API 客户端只需 baseURI；无需模拟 DOM 或向服务发送请求。
  globalThis.document = { baseURI: 'http://localhost/' }
  server = await createServer({ configFile: false, plugins: [vue()], server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ default: InboundsTab } = await server.ssrLoadModule('/src/views/stack/InboundsTab.vue'))
})
after(async () => {
  await server?.close()
  delete globalThis.document
})

function setup(t, users, refs) {
  const scope = effectScope()
  t.after(() => scope.stop())
  const stack = { name: 'edge1', xray: { inbounds: [{ name: 'vmess', users: refs }] } }
  const app = createSSRApp({ render: () => null })
  app.provide(ssrContextKey, {})
  const state = app.runWithContext(() => scope.run(() => InboundsTab.setup({ stack, users, errors: { fields: {}, global: [] }, reveal: async () => {} }, { expose() {} })))
  state.open(0)
  return { state, stack }
}

test('添加全部保留已选用户的档案和覆盖，按列表顺序补齐且不恢复暂停用户', (t) => {
  const refs = [{ user: 'alice', profile: 'phone', remark: 'phone-node', display_template: '{{ .stack }} phone', tag: 'phone-tag' }]
  const users = [{ name: 'carol', enabled: false }, { name: 'alice', enabled: true }, { name: 'bob', enabled: true }]
  const { state, stack } = setup(t, users, refs)
  const preserved = state.editing.value.draft.users[0]
  state.addAllUsers()
  assert.equal(state.editing.value.draft.users[0], preserved)
  assert.deepEqual(state.editing.value.draft.users[0], refs[0])
  assert.deepEqual(state.editing.value.draft.users.map((ref) => ref.user), ['alice', 'carol', 'bob'])
  assert.equal(state.selectedUserCount.value, 3)
  assert.equal(users[0].enabled, false)
  assert.deepEqual(stack.xray.inbounds[0].users, refs)
})

test('防御异常草稿中的同用户多档案，保持原引用并且重复点击不产生重复项', (t) => {
  const refs = [
    { user: 'alice', profile: 'phone', remark: 'phone-node', display_template: '{{ .stack }} phone', tag: 'phone-tag' },
    { user: 'alice', profile: 'laptop', remark: 'laptop-node', display_template: '{{ .stack }} laptop', tag: 'laptop-tag' },
  ]
  const users = [{ name: 'alice', enabled: true }, { name: 'bob', enabled: true }, { name: 'carol', enabled: false }]
  const { state, stack } = setup(t, users, refs)
  const draft = state.editing.value.draft
  const preserved = [...draft.users]
  assert.equal(state.selectedUserCount.value, 1)
  assert.equal(state.missingUsers.value.length, 2)
  state.addAllUsers()
  assert.equal(draft.users[0], preserved[0])
  assert.equal(draft.users[1], preserved[1])
  assert.deepEqual(draft.users.slice(0, 2), refs)
  assert.deepEqual(draft.users.slice(2), ['bob', 'carol'].map((user) => ({ user, profile: '', remark: '', display_template: '', tag: '' })))
  assert.equal(state.selectedUserCount.value, 3)
  assert.equal(state.missingUsers.value.length, 0)
  assert.equal(users[2].enabled, false)
  assert.deepEqual(stack.xray.inbounds[0].users, refs)
  state.addAllUsers()
  assert.equal(draft.users.length, 4)
  state.confirm()
  assert.deepEqual(stack.xray.inbounds[0].users, draft.users)
})

test('没有可添加用户时保持空列表', (t) => {
  const { state } = setup(t, [], [])
  state.addAllUsers()
  assert.equal(state.selectedUserCount.value, 0)
  assert.equal(state.missingUsers.value.length, 0)
  assert.deepEqual(state.editing.value.draft.users, [])
})
