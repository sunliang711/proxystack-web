import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let userTabFor
let userTabs

before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ userTabFor, userTabs } = await server.ssrLoadModule('/src/util/user-tabs.ts'))
})
after(async () => server?.close())

test('校验错误定位到字段所在的标签页', () => {
  assert.equal(userTabFor('name'), 'basic')
  assert.equal(userTabFor('expire_at'), 'basic')
  assert.equal(userTabFor('display_template'), 'subscription')
  assert.equal(userTabFor('uuid'), 'credentials')
  assert.equal(userTabFor('profiles[1].ss_password'), 'credentials')
  assert.equal(userTabFor('unknown.field'), 'basic')
})

test('每个需要保存的字段只属于一个标签页，当场生效的字段不计入', () => {
  const fields = userTabs.flatMap((tab) => tab.fields)
  assert.equal(new Set(fields).size, fields.length)
  assert.ok(!fields.includes('enabled'))
  assert.ok(!fields.includes('paused_stacks'))
  assert.deepEqual(userTabs.map((tab) => tab.name), ['basic', 'subscription', 'credentials', 'access', 'usage'])
})
