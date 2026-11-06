import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let mod
before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  mod = await server.ssrLoadModule('/src/util/traffic-stats.ts')
})
after(async () => { await server?.close() })

test('环比按上一期计算百分比，上一期为 0 时记为新增', () => {
  assert.deepEqual(mod.compareChange(150, 100), { text: '+50%', trend: 'up' })
  assert.deepEqual(mod.compareChange(95, 100), { text: '−5.0%', trend: 'down' })
  assert.deepEqual(mod.compareChange(100, 100), { text: '持平', trend: 'flat' })
  assert.deepEqual(mod.compareChange(10, 0), { text: '新增', trend: 'new' })
  assert.deepEqual(mod.compareChange(0, 0), { text: '—', trend: 'flat' })
  assert.deepEqual(mod.compareChange(10, undefined), { text: '', trend: 'none' })
})

test('图表标出无明细的时间桶，并把与缺口重叠的时间桶合并成连续区段', () => {
  const buckets = ['2026-11-01T00:00:00Z', '2026-11-01T01:00:00Z', '2026-11-01T02:00:00Z', '2026-11-01T03:00:00Z', '2026-11-01T04:00:00Z']
  const gaps = [
    { stack: 'usa1', kind: 'lost', reason: '', from: '2026-11-01T02:10:00Z', to: '2026-11-01T03:05:00Z', open: false },
    { stack: 'usa2', kind: 'delayed', reason: '', from: '2026-11-01T04:30:00Z', to: '2026-11-01T04:30:00Z', open: false },
  ]
  assert.deepEqual(mod.chartAreas(buckets, '2026-11-01T05:00:00Z', '2026-11-01T01:00:00Z', gaps), [
    { from: 0, to: 0, kind: 'missing', label: '无明细' },
    { from: 2, to: 3, kind: 'gap', label: '流量丢失' },
    { from: 4, to: 4, kind: 'gap', label: '延后计入' },
  ])
  assert.deepEqual(mod.chartAreas(buckets, '2026-11-01T05:00:00Z', '0001-01-01T00:00:00Z', []), [])
  assert.deepEqual(mod.gapSummary(gaps), { count: 2, minutes: 55 })
})

test('名次按所选口径排序，同用量并列，零用量不排名', () => {
  const rows = [{ id: 1, v: 5 }, { id: 2, v: 9 }, { id: 3, v: 5 }, { id: 4, v: 0 }]
  const ranks = mod.rankBy(rows, (row) => row.v)
  assert.deepEqual(rows.map((row) => ranks.get(row)), [2, 1, 2, undefined])
})
