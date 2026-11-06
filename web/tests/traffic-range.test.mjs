import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let defaultTrafficRange
let trafficBounds
let trafficRangeLabel
let recentHourRange
let recentDayRange
let presetBounds
let coarserLevel
before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ defaultTrafficRange, trafficBounds, trafficRangeLabel, recentHourRange, recentDayRange, presetBounds, coarserLevel } = await server.ssrLoadModule('/src/util/traffic-range.ts'))
})
after(async () => { await server?.close() })

const isoBounds = (bounds) => [bounds.from, bounds.to].map((value) => new Date(value * 1000).toISOString())

test('默认最近30天包含今日和起始自然日，消除当前时分秒与额外一小时', () => {
  const range = defaultTrafficRange('day', 'Asia/Shanghai', Date.parse('2026-11-01T01:49:04Z'))
  assert.deepEqual(range, ['2026-10-03 00:00:00', '2026-11-01 00:00:00'])
  const bounds = trafficBounds('day', range, 'Asia/Shanghai')
  assert.deepEqual(isoBounds(bounds), ['2026-10-02T16:00:00.000Z', '2026-11-01T16:00:00.000Z'])
  assert.equal((bounds.to - bounds.from) / 86400, 30)
  assert.equal(trafficRangeLabel(bounds, 'Asia/Shanghai'), '2026-10-03 00:00:00 ～ 2026-11-01 23:59:59（Asia/Shanghai）')
})

test('同日查询包括完整自然日，排除次日零点桶', () => {
  const bounds = trafficBounds('day', ['2026-11-01 12:34:56', '2026-11-01 12:34:56'], 'Asia/Shanghai')
  const buckets = ['2026-10-30T16:00:00Z', '2026-10-31T16:00:00Z', '2026-11-01T16:00:00Z'].map(Date.parse)
  assert.deepEqual(buckets.filter((at) => at / 1000 >= bounds.from && at / 1000 < bounds.to), [Date.parse('2026-10-31T16:00:00Z')])
})

test('月与年按完整周期查询，起始月不会漏算，结束后周期不会混入', () => {
  assert.deepEqual(isoBounds(trafficBounds('month', ['2026-01-31 12:34:56', '2026-02-28 23:59:59'], 'UTC')),
    ['2026-01-01T00:00:00.000Z', '2026-03-01T00:00:00.000Z'])
  assert.deepEqual(isoBounds(trafficBounds('year', ['2024-02-29 12:34:56', '2026-11-01 23:59:59'], 'UTC')),
    ['2024-01-01T00:00:00.000Z', '2027-01-01T00:00:00.000Z'])
})

test('默认小时、月、年分别选择统计时区的当天、12个自然月与5个自然年', () => {
  const now = Date.parse('2026-11-01T23:49:04Z')
  assert.deepEqual(defaultTrafficRange('hour', 'Asia/Shanghai', now), ['2026-11-02 00:00:00', '2026-11-02 07:00:00'])
  assert.deepEqual(defaultTrafficRange('month', 'Asia/Shanghai', now), ['2025-12-01 00:00:00', '2026-11-01 00:00:00'])
  assert.deepEqual(defaultTrafficRange('year', 'Asia/Shanghai', now), ['2022-01-01 00:00:00', '2026-01-01 00:00:00'])
})

test('系统时区与浏览器时区不同时仍按系统日期查询，支持半小时时区', () => {
  for (const [timezone, expected] of [
    ['Asia/Kolkata', ['2026-10-31T18:30:00.000Z', '2026-11-01T18:30:00.000Z']],
    ['America/New_York', ['2026-11-01T04:00:00.000Z', '2026-11-02T05:00:00.000Z']],
  ]) {
    assert.deepEqual(isoBounds(trafficBounds('day', ['2026-11-01 00:00:00', '2026-11-01 00:00:00'], timezone)), expected)
  }
})

test('夏令时自然日分别覆盖23与25小时，不使用固定86400秒递增', () => {
  for (const [day, hours] of [['2026-03-08', 23], ['2026-11-01', 25]]) {
    const value = `${day} 00:00:00`
    const bounds = trafficBounds('day', [value, value], 'America/New_York')
    assert.equal((bounds.to - bounds.from) / 3600, hours)
  }
  const repeated = trafficBounds('hour', ['2026-11-01 01:00:00', '2026-11-01 01:00:00'], 'America/New_York')
  assert.equal((repeated.to - repeated.from) / 3600, 2)
})

test('最近24小时含当前小时，按统计时区整点切分', () => {
  const now = Date.parse('2026-11-01T23:49:04Z')
  const range = recentHourRange(24, 'Asia/Shanghai', now)
  assert.deepEqual(range, ['2026-11-01 08:00:00', '2026-11-02 07:00:00'])
  assert.deepEqual(isoBounds(trafficBounds('hour', range, 'Asia/Shanghai')), ['2026-11-01T00:00:00.000Z', '2026-11-02T00:00:00.000Z'])
  const kolkata = trafficBounds('hour', recentHourRange(2, 'Asia/Kolkata', now), 'Asia/Kolkata')
  assert.deepEqual(isoBounds(kolkata), ['2026-11-01T22:30:00.000Z', '2026-11-02T00:30:00.000Z'])
})

test('最近30个自然日按统计时区的零点切分', () => {
  const now = Date.parse('2026-11-01T23:49:04Z')
  const range = recentDayRange(30, 'Asia/Shanghai', now)
  assert.deepEqual(range, ['2026-10-04 00:00:00', '2026-11-02 00:00:00'])
  assert.deepEqual(isoBounds(trafficBounds('day', range, 'Asia/Shanghai')), ['2026-10-03T16:00:00.000Z', '2026-11-02T16:00:00.000Z'])
})

test('排行快捷时间段覆盖统计时区的今日、昨日、本月和上月', () => {
  const now = Date.parse('2026-11-01T01:49:04Z')
  assert.deepEqual(isoBounds(presetBounds('today', 'Asia/Shanghai', now)), ['2026-10-31T16:00:00.000Z', '2026-11-01T16:00:00.000Z'])
  assert.deepEqual(isoBounds(presetBounds('yesterday', 'Asia/Shanghai', now)), ['2026-10-30T16:00:00.000Z', '2026-10-31T16:00:00.000Z'])
  assert.deepEqual(isoBounds(presetBounds('month', 'Asia/Shanghai', now)), ['2026-10-31T16:00:00.000Z', '2026-11-30T16:00:00.000Z'])
  assert.deepEqual(isoBounds(presetBounds('last_month', 'America/New_York', now)), ['2026-09-01T04:00:00.000Z', '2026-10-01T04:00:00.000Z'])
  assert.equal(coarserLevel('hour'), 'day')
  assert.equal(coarserLevel('year'), null)
})

test('快捷时间段带有合适的粒度，并能从粒度和范围反查', async () => {
  const mod = await server.ssrLoadModule('/src/util/traffic-range.ts')
  const now = Date.parse('2026-11-06T03:20:00Z')
  assert.deepEqual(mod.presetRange('today', 'Asia/Shanghai', now), { level: 'hour', range: ['2026-11-06 00:00:00', '2026-11-06 11:00:00'] })
  assert.deepEqual(mod.presetRange('yesterday', 'Asia/Shanghai', now).range, ['2026-11-05 00:00:00', '2026-11-05 23:00:00'])
  assert.deepEqual(mod.presetRange('last_month', 'Asia/Shanghai', now), { level: 'day', range: ['2026-10-01 00:00:00', '2026-10-31 00:00:00'] })
  assert.deepEqual(mod.presetRange('year', 'Asia/Shanghai', now), { level: 'month', range: ['2026-01-01 00:00:00', '2026-11-01 00:00:00'] })
  assert.deepEqual(mod.presetRange('30d', 'Asia/Shanghai', now).range, mod.defaultTrafficRange('day', 'Asia/Shanghai', now))
  assert.equal(mod.matchPreset('day', ['2026-10-01 00:00:00', '2026-10-31 00:00:00'], 'Asia/Shanghai', now), 'last_month')
  assert.equal(mod.matchPreset('hour', ['2026-10-01 00:00:00', '2026-10-31 00:00:00'], 'Asia/Shanghai', now), null)
})

test('切换粒度时换算范围，不丢末尾周期也不超过当前时间', async () => {
  const mod = await server.ssrLoadModule('/src/util/traffic-range.ts')
  const now = Date.parse('2026-11-06T03:20:00Z')
  const tz = 'Asia/Shanghai'
  assert.deepEqual(mod.convertRange('day', 'hour', ['2026-11-01 00:00:00', '2026-11-03 00:00:00'], tz, now), ['2026-11-01 00:00:00', '2026-11-03 23:00:00'])
  assert.deepEqual(mod.convertRange('day', 'hour', ['2026-11-05 00:00:00', '2026-11-06 00:00:00'], tz, now), ['2026-11-05 00:00:00', '2026-11-06 11:00:00'])
  assert.deepEqual(mod.convertRange('day', 'month', ['2026-10-08 00:00:00', '2026-11-06 00:00:00'], tz, now), ['2026-10-01 00:00:00', '2026-11-01 00:00:00'])
  assert.deepEqual(mod.convertRange('month', 'day', ['2026-09-01 00:00:00', '2026-10-01 00:00:00'], tz, now), ['2026-09-01 00:00:00', '2026-10-31 00:00:00'])
  assert.equal(mod.bucketCount('day', ['2026-10-08 00:00:00', '2026-11-06 00:00:00']), 30)
})

test('前后移动范围：整月按自然月，整天按自然日，其余按时间桶数量', async () => {
  const mod = await server.ssrLoadModule('/src/util/traffic-range.ts')
  assert.deepEqual(mod.shiftRange('day', ['2026-10-01 00:00:00', '2026-10-31 00:00:00'], -1), ['2026-09-01 00:00:00', '2026-09-30 00:00:00'])
  assert.deepEqual(mod.shiftRange('day', ['2026-01-01 00:00:00', '2026-02-28 00:00:00'], 1), ['2026-03-01 00:00:00', '2026-04-30 00:00:00'])
  assert.deepEqual(mod.shiftRange('hour', ['2026-11-05 00:00:00', '2026-11-05 23:00:00'], 1), ['2026-11-06 00:00:00', '2026-11-06 23:00:00'])
  assert.deepEqual(mod.shiftRange('day', ['2026-10-08 00:00:00', '2026-11-06 00:00:00'], -1), ['2026-09-08 00:00:00', '2026-10-07 00:00:00'])
  assert.deepEqual(mod.shiftRange('month', ['2025-12-01 00:00:00', '2026-11-01 00:00:00'], -1), ['2024-12-01 00:00:00', '2025-11-01 00:00:00'])
  const now = Date.parse('2026-11-06T03:20:00Z')
  const tz = 'Asia/Shanghai'
  assert.deepEqual(mod.shiftRange('hour', mod.presetRange('today', tz, now).range, -1), mod.presetRange('yesterday', tz, now).range)
  assert.deepEqual(mod.shiftRange('day', mod.presetRange('month', tz, now).range, -1), mod.presetRange('last_month', tz, now).range)
  assert.deepEqual(mod.shiftRange('month', mod.presetRange('year', tz, now).range, -1), ['2025-01-01 00:00:00', '2025-12-01 00:00:00'])
  assert.equal(mod.rangeInFuture('day', ['2026-11-07 00:00:00', '2026-12-06 00:00:00'], 'Asia/Shanghai', now), true)
  assert.equal(mod.rangeInFuture('day', ['2026-11-06 00:00:00', '2026-12-05 00:00:00'], 'Asia/Shanghai', now), false)
})
