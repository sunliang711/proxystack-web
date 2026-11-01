import assert from 'node:assert/strict'
import { before, after, beforeEach, afterEach, test } from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { effectScope, nextTick, reactive } from 'vue'
import { createServer } from 'vite'

let server
let api
let session
let findEntity
let useEntityReference
let originalGet
const scopes = []
const originalDocument = globalThis.document

before(async () => {
  globalThis.document = { baseURI: 'http://localhost/' }
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ api } = await server.ssrLoadModule('/src/api/client.ts'))
  ;({ session } = await server.ssrLoadModule('/src/session.ts'))
  ;({ findEntity, useEntityReference } = await server.ssrLoadModule('/src/composables/useEntityReference.ts'))
  originalGet = api.GET
})
beforeEach(() => { session.admin = { username: 'test-admin' } })
afterEach(() => {
  for (const scope of scopes.splice(0)) scope.stop()
  api.GET = originalGet
  session.admin = null
})
after(async () => {
  await server?.close()
  if (originalDocument === undefined) delete globalThis.document
  else globalThis.document = originalDocument
})

function reference(value) {
  const props = reactive(value)
  const scope = effectScope()
  scopes.push(scope)
  return { props, record: scope.run(() => useEntityReference(() => props)) }
}

test('稳定 ID 优先于名称，已删除、未知和非法 ID 不误指向同名新对象', () => {
  const records = [{ id: 3, name: 'same-name' }, { id: 4, name: 'renamed' }]
  assert.equal(findEntity(records, { id: '4', name: 'old-name' }).name, 'renamed')
  assert.equal(findEntity(records, { name: 'same-name' }).id, 3)
  for (const id of [2, 0, -1, '', 'unknown', '3.5', '4.7']) {
    assert.equal(findEntity(records, { id, name: 'same-name' }), undefined)
  }
  assert.equal(findEntity(records, { name: 'deleted' }), undefined)
  assert.equal(findEntity([{ name: 'unsaved' }], { name: 'unsaved' }), undefined)
})

test('同批大量引用只按对象类型发一次请求，Stack 和用户的同名对象不会混淆', async () => {
  const calls = []
  api.GET = async (path) => {
    calls.push(path)
    return path === '/api/stacks' ? { data: { stacks: [{ id: 9, name: 'shared' }] } } : { data: { users: [{ id: 2, name: 'shared' }] } }
  }
  const stacks = Array.from({ length: 100 }, () => reference({ kind: 'stack', name: 'shared' }))
  const user = reference({ kind: 'user', name: 'shared' })
  await setImmediate()
  assert.deepEqual(calls, ['/api/stacks', '/api/users'])
  assert.ok(stacks.every((item) => item.record.value.id === 9))
  assert.equal(user.record.value.id, 2)
  user.props.id = 999
  await nextTick()
  assert.equal(user.record.value, undefined)
})

test('重新进入页面刷新目录，删除的对象和请求失败均退回纯文本', async () => {
  api.GET = async () => ({ data: { stacks: [{ id: 9, name: 'edge' }] } })
  const first = reference({ kind: 'stack', id: 9 })
  await setImmediate()
  assert.equal(first.record.value.id, 9)
  api.GET = async () => ({ data: { stacks: [] } })
  reference({ kind: 'stack', id: 9 })
  await setImmediate()
  assert.equal(first.record.value, undefined)
  api.GET = async () => { throw new Error('Network unavailable') }
  reference({ kind: 'stack', id: 9 })
  await setImmediate()
  assert.equal(first.record.value, undefined)
})

test('切换账号后旧请求迟到不能覆盖新账号的引用目录', async () => {
  let finishOld
  api.GET = () => new Promise((resolve) => { finishOld = resolve })
  reference({ kind: 'user', name: 'alice' })
  session.admin = { username: 'another-admin' }
  api.GET = async () => ({ data: { users: [{ id: 8, name: 'alice' }] } })
  const current = reference({ kind: 'user', name: 'alice' })
  await setImmediate()
  assert.equal(current.record.value.id, 8)
  finishOld({ data: { users: [{ id: 1, name: 'alice' }] } })
  await setImmediate()
  assert.equal(current.record.value.id, 8)
})
