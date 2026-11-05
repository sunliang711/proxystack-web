import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let mod

before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  mod = await server.ssrLoadModule('/src/composables/useSettingsSections.ts')
})
after(async () => server?.close())

test('每个设置分组只属于一个标签页，并且都有生效方式', () => {
  const placed = mod.settingsTabs.flatMap((tab) => tab.sections)
  assert.equal(new Set(placed).size, placed.length)
  assert.deepEqual([...placed].sort(), [...mod.settingsSections].sort())
  for (const section of mod.settingsSections) assert.ok(mod.sectionEffects[section]?.label, section)
  assert.deepEqual(mod.settingsTabs.map((tab) => tab.name), ['basic', 'subscription', 'monitor', 'download', 'advanced'])
})

test('分组和校验错误字段定位到所在标签页', () => {
  assert.equal(mod.tabOfSection('notify'), 'monitor')
  assert.equal(mod.tabOfSection('port_pools'), 'advanced')
  assert.equal(mod.tabOfSection('unknown'), 'basic')
  assert.equal(mod.tabForField('defaults.xray.policy.stats_user_uplink'), 'advanced')
  assert.equal(mod.tabForField('download_proxy.port'), 'download')
  assert.equal(mod.tabForField('basic.external_host'), 'basic')
  assert.equal(mod.tabForField('subscription.interval'), 'subscription')
})
