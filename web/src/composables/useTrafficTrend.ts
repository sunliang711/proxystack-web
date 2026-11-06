import { ref, watch } from 'vue'
import { api, problemMessage, type Schemas } from '../api/client'
import type { ChartSeries } from '../util/chart'
import { recentDayRange, recentHourRange, trafficBounds } from '../util/traffic-range'

/** 趋势查询：最近 span 个小时或自然日；view 为 user 时每个用户一条序列，为 stack 时每个 stack 一条序列。 */
export interface TrendQuery {
  level: 'hour' | 'day'
  span: number
  view: 'user' | 'stack'
  stacks?: number[]
  users?: number[]
}

export interface Trend {
  labels: string[]
  series: ChartSeries[]
  empty: boolean
}

/** 图表最多单独显示的序列数，其余合并为「其他」。 */
const MAX_SERIES = 7

let timezone: Promise<string> | null = null
/** 统计时区只读取一次；读取失败时下次重试。 */
export function loadTimezone(): Promise<string> {
  timezone ??= api.GET('/api/config/settings').then(({ data }) => {
    if (!data) {
      timezone = null
      return 'UTC'
    }
    return data.basic.timezone || 'UTC'
  })
  return timezone
}

/** 把统计结果的各行转成图表序列：零用量的行不画，超出数量的并入「其他」。 */
export function statsSeries(rows: Schemas['TrafficStatsRow'][], limit = MAX_SERIES): ChartSeries[] {
  const drawn = rows.filter((row) => row.up + row.down > 0 && row.series_up && row.series_down)
  const values = (row: Schemas['TrafficStatsRow']) => row.series_up!.map((up, index) => up + row.series_down![index]!)
  const series: ChartSeries[] = drawn.slice(0, limit).map((row, index) => ({
    name: row.deleted ? `${row.name}（已删除）` : row.name,
    data: values(row),
    slot: index,
    excludeFromTotal: row.excluded,
    entity: (row.kind === 'user' || row.kind === 'stack') && !row.deleted ? { kind: row.kind, id: row.id } : undefined,
  }))
  const rest = drawn.slice(limit)
  if (rest.length) {
    const sums = values(rest[0]!).map(() => 0)
    for (const row of rest) values(row).forEach((value, index) => { sums[index]! += row.excluded ? 0 : value })
    series.push({ name: `其他 ${rest.length} 项`, data: sums })
  }
  return series
}

/** 按统计粒度格式化时间桶。 */
export function bucketLabels(buckets: string[], level: string, zone: string, long = false): string[] {
  const parts: Intl.DateTimeFormatOptions = {
    hour: long ? { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' } : { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' },
    day: { month: '2-digit', day: '2-digit' },
    month: { year: 'numeric', month: '2-digit' },
    year: { year: 'numeric' },
  }[level] as Intl.DateTimeFormatOptions
  const formatter = new Intl.DateTimeFormat('zh-CN', { ...parts, timeZone: zone })
  return buckets.map((bucket) => formatter.format(new Date(bucket)))
}

/**
 * 查询最近若干小时或自然日的趋势，服务端已补齐没有流量的时间桶。
 * query 返回 null 时不查询；条件变化时清空并重新查询，load 用于跟随页面轮询刷新。
 */
export function useTrafficTrend(query: () => TrendQuery | null) {
  const trend = ref<Trend | null>(null)
  const error = ref('')
  let version = 0

  async function load() {
    const current = query()
    if (!current) return
    const id = ++version
    const zone = await loadTimezone()
    const range = current.level === 'hour' ? recentHourRange(current.span, zone) : recentDayRange(current.span, zone)
    const bounds = trafficBounds(current.level, range, zone)
    const { data, error: problem } = await api.GET('/api/traffic/stats', {
      params: {
        query: {
          view: current.view, level: current.level, ...bounds,
          stacks: (current.stacks ?? []).join(','), users: (current.users ?? []).join(','),
        },
      },
    })
    if (id !== version) return
    if (!data) {
      error.value = problemMessage(problem, '趋势查询失败')
      return
    }
    error.value = ''
    const series = statsSeries(data.rows)
    trend.value = {
      labels: bucketLabels(data.buckets, current.level, data.timezone, current.span > 24),
      series,
      empty: !series.some((item) => item.data.some((value) => value > 0)),
    }
  }

  watch(() => JSON.stringify(query()), () => {
    trend.value = null
    void load()
  }, { immediate: true })

  return { trend, error, load }
}
