import { ref, watch } from 'vue'
import { api, problemMessage } from '../api/client'
import type { ChartSeries } from '../util/chart'
import { alignBuckets, dayBuckets, hourBuckets, recentDayRange, recentHourRange, trafficBounds } from '../util/traffic-range'

/** 趋势查询：最近 span 个小时或自然日，按 groupBy 拆成多条序列。 */
export interface TrendQuery {
  level: 'hour' | 'day'
  span: number
  scope: 'user' | 'inbound'
  groupBy: 'stack' | 'subject'
  stacks?: number[]
  subjects?: string[]
  /** 序列对应的实体，图例和柱子可点击打开。 */
  entity?: 'stack' | 'user'
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
function loadTimezone(): Promise<string> {
  timezone ??= api.GET('/api/config/settings').then(({ data }) => {
    if (!data) {
      timezone = null
      return 'UTC'
    }
    return data.basic.timezone || 'UTC'
  })
  return timezone
}

/**
 * 从流量明细查询最近若干小时或自然日的趋势，补齐没有流量的时间桶。
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
    const { data, error: problem } = await api.GET('/api/traffic/query', {
      params: {
        query: {
          level: current.level, scope: current.scope, group_by: current.groupBy, ...bounds,
          stacks: (current.stacks ?? []).join(','), subjects: (current.subjects ?? []).join(','),
        },
      },
    })
    if (id !== version) return
    if (!data) {
      error.value = problemMessage(problem, '趋势查询失败')
      return
    }
    error.value = ''
    const buckets = current.level === 'hour' ? hourBuckets(bounds) : dayBuckets(range, zone)
    const aligned = data.series.map((item) => ({ item, values: alignBuckets(buckets, data.buckets, item.up.map((up, index) => up + item.down[index]!)) }))
    const series: ChartSeries[] = aligned.slice(0, MAX_SERIES).map(({ item, values }, index) => ({
      name: item.label, data: values, slot: index,
      entity: current.entity ? { kind: current.entity, id: item.subject } : undefined,
    }))
    const rest = aligned.slice(MAX_SERIES)
    if (rest.length) series.push({ name: `其他 ${rest.length} 项`, data: buckets.map((_, index) => rest.reduce((sum, { values }) => sum + values[index]!, 0)) })
    const parts: Intl.DateTimeFormatOptions =
      current.level === 'day' ? { month: '2-digit', day: '2-digit' }
        : current.span > 24 ? { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
          : { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
    const formatter = new Intl.DateTimeFormat('zh-CN', { ...parts, timeZone: data.timezone })
    trend.value = {
      labels: buckets.map((at) => formatter.format(at * 1000)),
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
