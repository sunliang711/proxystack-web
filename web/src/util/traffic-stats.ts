import type { ChartArea } from './chart'

export interface Bytes { up: number; down: number }

export const totalOf = (bytes: Bytes) => bytes.up + bytes.down

/** 与上一期相比的变化。上一期为 0 时，本期有用量记为新增。 */
export interface Change {
  text: string
  trend: 'up' | 'down' | 'flat' | 'new' | 'none'
}

export function compareChange(current: number, previous: number | undefined | null): Change {
  if (previous === undefined || previous === null) return { text: '', trend: 'none' }
  if (previous === 0) return current > 0 ? { text: '新增', trend: 'new' } : { text: '—', trend: 'flat' }
  const percent = ((current - previous) / previous) * 100
  if (Math.abs(percent) < 0.5) return { text: '持平', trend: 'flat' }
  const value = Math.abs(percent) >= 10 ? Math.round(Math.abs(percent)).toString() : Math.abs(percent).toFixed(1)
  return { text: `${percent > 0 ? '+' : '−'}${value}%`, trend: percent > 0 ? 'up' : 'down' }
}

export interface GapLike { stack: string; kind: string; reason: string; from: string; to: string; open: boolean }

/** 缺口的段数和覆盖的总分钟数（同一时间多个 stack 中断时分别计算）。 */
export function gapSummary(gaps: GapLike[]): { count: number; minutes: number } {
  const minutes = gaps.reduce((sum, gap) => sum + Math.max(0, Date.parse(gap.to) - Date.parse(gap.from)) / 60_000, 0)
  return { count: gaps.length, minutes: Math.max(gaps.length ? 1 : 0, Math.round(minutes)) }
}

/**
 * 计算图表上要标出的时间段：早于 availableFrom 的时间桶没有明细；与缺口重叠的时间桶标为中断。
 * buckets 是各时间桶的开始时刻，end 是最后一个时间桶的结束时刻。
 */
export function chartAreas(buckets: string[], end: string, availableFrom: string | null | undefined, gaps: GapLike[]): ChartArea[] {
  const starts = buckets.map((bucket) => Date.parse(bucket))
  const ends = [...starts.slice(1), Date.parse(end)]
  const areas: ChartArea[] = []
  const available = availableFrom ? Date.parse(availableFrom) : NaN
  if (!Number.isNaN(available) && available > 0) {
    const missing = starts.filter((start) => start < available).length
    if (missing > 0) areas.push({ from: 0, to: missing - 1, kind: 'missing', label: '无明细' })
  }
  const marked = starts.map(() => '')
  for (const gap of gaps) {
    const from = Date.parse(gap.from)
    const to = Math.max(Date.parse(gap.to), from + 1)
    starts.forEach((start, index) => {
      if (start < to && ends[index]! > from) marked[index] = marked[index] === '' || marked[index] === gap.kind ? gap.kind : 'mixed'
    })
  }
  for (let index = 0; index < marked.length; index++) {
    if (!marked[index]) continue
    let last = index
    while (last + 1 < marked.length && marked[last + 1] === marked[index]) last++
    const label = marked[index] === 'lost' ? '流量丢失' : marked[index] === 'delayed' ? '延后计入' : '采集中断'
    areas.push({ from: index, to: last, kind: 'gap', label })
    index = last
  }
  return areas
}

/** 缺口类型的说明。 */
export function gapKindLabel(kind: string): string {
  return kind === 'lost' ? '流量丢失' : '延后计入'
}

/** 按指定口径排序后的名次（从 1 开始），同用量并列取相同名次；用量为 0 的不排名。 */
export function rankBy<T>(rows: T[], value: (row: T) => number): Map<T, number> {
  const sorted = rows.filter((row) => value(row) > 0).sort((a, b) => value(b) - value(a))
  const out = new Map<T, number>()
  sorted.forEach((row, index) => {
    const previous = sorted[index - 1]
    out.set(row, previous && value(previous) === value(row) ? out.get(previous)! : index + 1)
  })
  return out
}
