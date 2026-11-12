import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let mod

before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  mod = await server.ssrLoadModule('/src/util/pause-window.ts')
})
after(async () => server?.close())

test('时段文字标出跨零点的结束时刻', () => {
  assert.equal(mod.windowsText(['02:00-08:00', '23:00-06:00']), '02:00–08:00、23:00–次日 06:00')
  assert.equal(mod.windowsText([]), '')
})

test('时刻按设置时区显示，跨天时标明明天或日期', () => {
  const now = new Date('2026-11-12T04:00:00Z') // 上海 12:00
  assert.equal(mod.clockText('2026-11-12T06:30:00Z', 'Asia/Shanghai', now), '14:30')
  assert.equal(mod.clockText('2026-11-13T00:00:00Z', 'Asia/Shanghai', now), '明天 08:00')
  assert.equal(mod.clockText('2026-11-15T00:00:00Z', 'Asia/Shanghai', now), '11-15 08:00')
  // 同一时刻在纽约仍是 11 月 11 日晚上。
  assert.equal(mod.clockText('2026-11-12T04:00:00Z', 'America/New_York', now), '23:00')
})

test('Go 的零值时间视为没有', () => {
  assert.equal(mod.hasTime('0001-01-01T00:00:00Z'), false)
  assert.equal(mod.hasTime(''), false)
  assert.equal(mod.hasTime('2026-11-12T04:00:00Z'), true)
})
