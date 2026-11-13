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

test('全局搜索的字段索引覆盖设置页上的全部字段标签', async () => {
  const { readFile } = await import('node:fs/promises')
  const source = await readFile(new URL('../src/views/SettingsView.vue', import.meta.url), 'utf8')
  const sections = [...source.matchAll(/<SettingsSection\b[^>]*\bname="(\w+)"[^>]*>([\s\S]*?)<\/SettingsSection>/g)]
  assert.deepEqual(sections.map((match) => match[1]).sort(), [...mod.settingsSections].sort())
  for (const [, name, body] of sections) {
    // 只检查静态标签；动态标签（:label）在索引中手工展开。
    const labels = [...body.matchAll(/<NFormItem\b[^>]*?\slabel="([^"]+)"/g)].map((match) => match[1])
    const fields = mod.sectionSearch[name].fields
    for (const label of labels) assert.ok(fields.includes(label), `设置分组 ${name} 的字段「${label}」没有加入全局搜索索引`)
  }
})

test('停服时段的字段存放在 traffic 下，但归属「基本」标签页的停服时段分组', () => {
  assert.equal(mod.tabOfSection('pause'), 'basic')
  assert.equal(mod.sectionOfField('traffic.pause_windows'), 'pause')
  assert.equal(mod.sectionOfField('traffic.pause_windows[0]'), 'pause')
  assert.equal(mod.tabForField('traffic.disconnect_on_pause'), 'basic')
  assert.equal(mod.tabForField('traffic.interval'), 'monitor')
  const settings = { traffic: { interval: 10, pause_windows: ['02:00-08:00'], disconnect_on_pause: true } }
  assert.deepEqual(mod.sectionValue(settings, 'pause'), { pause_windows: ['02:00-08:00'], disconnect_on_pause: true })
  assert.deepEqual(mod.sectionValue(settings, 'traffic'), { interval: 10 })
})

test('全局域名黑名单存放在 traffic 下，但归属「基本」标签页的域名黑名单分组', () => {
  assert.equal(mod.tabOfSection('blocklist'), 'basic')
  assert.equal(mod.sectionOfField('traffic.blocked_domains'), 'blocklist')
  assert.equal(mod.tabForField('traffic.blocked_domains'), 'basic')
  const settings = { traffic: { interval: 10, pause_windows: [], disconnect_on_pause: false, blocked_domains: ['example.com'] } }
  assert.deepEqual(mod.sectionValue(settings, 'blocklist'), { blocked_domains: ['example.com'] })
  assert.deepEqual(mod.sectionValue(settings, 'traffic'), { interval: 10 })
})
