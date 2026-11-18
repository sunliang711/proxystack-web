import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createServer } from 'vite'

let server
let sectionSearch

before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ sectionSearch } = await server.ssrLoadModule('/src/composables/useSettingsSections.ts'))
})
after(async () => server?.close())

test('系统设置订阅分组搜索索引包含节点 Stack 顺序字段', () => {
  assert.ok(sectionSearch.subscription.fields.includes('节点 Stack 顺序'))
})
