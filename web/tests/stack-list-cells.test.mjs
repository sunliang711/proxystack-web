import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let shortOrg
let shortLocation
let healthView
let unitBrief
const originalDocument = globalThis.document

before(async () => {
  globalThis.document = { baseURI: 'http://localhost/' }
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ shortOrg, shortLocation, healthView } = await server.ssrLoadModule('/src/util/egress-view.ts'))
  ;({ unitBrief } = await server.ssrLoadModule('/src/util/unit-status.ts'))
})

after(async () => {
  await server?.close()
  if (originalDocument === undefined) delete globalThis.document
  else globalThis.document = originalDocument
})

test('运营商简称去掉 AS 号和公司后缀', () => {
  assert.equal(shortOrg('AS24940 Hetzner Online GmbH'), 'Hetzner')
  assert.equal(shortOrg('AS25820 IT7 Networks Inc'), 'IT7')
  assert.equal(shortOrg('AS16509 Amazon.com, Inc.'), 'Amazon.com')
  assert.equal(shortOrg('Google LLC'), 'Google')
  assert.equal(shortOrg('China Telecom'), 'China Telecom')
  assert.equal(shortOrg('Online'), 'Online')
})

test('地区优先城市，否则用地区并去掉国家代码', () => {
  assert.equal(shortLocation('Frankfurt am Main', 'DE Hesse', 'DE'), 'Frankfurt am Main')
  assert.equal(shortLocation('', 'US California', 'US'), 'California')
})

test('健康圆点只在需要关注时附文字', () => {
  assert.deepEqual([healthView('healthy').tone, healthView('healthy').badge], ['success', ''])
  assert.deepEqual([healthView('down').tone, healthView('down').badge], ['error', '异常'])
  assert.deepEqual([healthView('failing').tone, healthView('failing').badge], ['warning', '待确认'])
  assert.equal(healthView('paused').badge, '')
  assert.equal(healthView(undefined).tone, 'default')
  assert.equal(healthView('healthy', true).badge, '刷新失败')
})

test('运行列紧凑状态：运行中只显示名字，停止按是否符合预期配色', () => {
  const unit = (active, present = true) => ({ unit: 'proxystack-xray@usa1.service', active, present, since: '', sub: '' })
  assert.deepEqual(unitBrief(unit('active'), true), { text: 'xray', type: 'success' })
  assert.deepEqual(unitBrief(unit('inactive'), false), { text: 'xray 已停止', type: 'default' })
  assert.deepEqual(unitBrief(unit('inactive'), true), { text: 'xray 已停止', type: 'error' })
  assert.deepEqual(unitBrief(unit('failed'), false), { text: 'xray 失败', type: 'error' })
  assert.deepEqual(unitBrief(unit('activating'), true), { text: 'xray 启动中', type: 'info' })
  assert.deepEqual(unitBrief(unit('', false), true), { text: 'xray 未生成', type: 'default' })
})
