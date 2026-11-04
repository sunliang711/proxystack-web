import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let describeCron
before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ describeCron } = await server.ssrLoadModule('/src/util/cron.ts'))
})
after(async () => { await server?.close() })

test('常见计划翻译成中文', () => {
  const cases = {
    '* * * * *': '每分钟',
    '*/2 * * * *': '每 2 分钟',
    '*/30 * * * *': '每 30 分钟',
    '0 * * * *': '每小时整点',
    '15 * * * *': '每小时第 15 分',
    '0 */6 * * *': '每 6 小时整点',
    '0 4 * * *': '每天 04:00',
    '30 2,14 * * *': '每天 02:30、14:30',
    '0 5 * * 1': '每周一 05:00',
    '0 5 * * 0': '每周日 05:00',
    '0 9 * * 1-5': '每周一至周五 09:00',
    '0 4 1 * *': '每月 1 日 04:00',
    '@daily': '每天 00:00',
    '@every 1h30m': '每隔 1h30m',
  }
  for (const [expr, want] of Object.entries(cases)) assert.equal(describeCron(expr), want, expr)
})

test('复杂表达式不翻译', () => {
  for (const expr of ['0 4 1 * 1', '0 4 * 6 *', '*/5 1-3 * * *', '0 4 L * *', '1 2 3', '@reboot']) {
    assert.equal(describeCron(expr), null, expr)
  }
})
