export type TrafficLevel = 'hour' | 'day' | 'month' | 'year'
export type TrafficRange = [string, string]

// 选择器使用日期文本，避免浏览器时区改变用户所选的统计日期。
function civil(value: string): Date {
  return new Date(value.replace(' ', 'T') + 'Z')
}

function text(value: Date): string {
  return value.toISOString().slice(0, 19).replace('T', ' ')
}

function localTime(value: number, timezone: string): Date {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  }).formatToParts(value)
  const fields = Object.fromEntries(parts.map((part) => [part.type, part.value]))
  return civil(`${fields.year}-${fields.month}-${fields.day} ${fields.hour}:${fields.minute}:${fields.second}`)
}

function floor(value: Date, level: TrafficLevel): Date {
  const date = new Date(value)
  date.setUTCMinutes(0, 0, 0)
  if (level !== 'hour') date.setUTCHours(0)
  if (level === 'month' || level === 'year') date.setUTCDate(1)
  if (level === 'year') date.setUTCMonth(0)
  return date
}

function step(value: Date, level: TrafficLevel, amount: number): Date {
  const date = new Date(value)
  if (level === 'hour') date.setUTCHours(date.getUTCHours() + amount)
  if (level === 'day') date.setUTCDate(date.getUTCDate() + amount)
  if (level === 'month') date.setUTCMonth(date.getUTCMonth() + amount)
  if (level === 'year') date.setUTCFullYear(date.getUTCFullYear() + amount)
  return date
}

// 从周期两侧取得时区偏移，兼容夏令时切换；重复小时包含两次出现。
function instant(date: Date, timezone: string): number {
  const target = date.getTime()
  const offsets = new Set([-36, 0, 36].map((hour) => {
    const at = target + hour * 3600_000
    return localTime(at, timezone).getTime() - at
  }))
  const candidates = [...offsets].map((offset) => target - offset)
  const exact = candidates.filter((at) => localTime(at, timezone).getTime() === target)
  if (exact.length) return Math.min(...exact)
  // 时钟前拨跳过的边界落在跳转时刻，不把缺失小时算到前一天。
  return Math.min(...candidates.filter((at) => localTime(at, timezone).getTime() > target))
}

// 小时粒度默认显示统计时区的当天，其他粒度显示最近若干个周期。
export function defaultTrafficRange(level: TrafficLevel, timezone: string, now = Date.now()): TrafficRange {
  const local = localTime(now, timezone)
  const end = floor(local, level)
  if (level === 'hour') return [text(floor(local, 'day')), text(end)]
  const count = { day: 30, month: 12, year: 5 }[level]
  return [text(step(end, level, 1 - count)), text(end)]
}

export function trafficBounds(level: TrafficLevel, range: TrafficRange, timezone: string) {
  const from = instant(floor(civil(range[0]), level), timezone)
  const to = instant(step(floor(civil(range[1]), level), level, 1), timezone)
  return { from: from / 1000, to: to / 1000 }
}

export function trafficRangeLabel(bounds: { from: number; to: number }, timezone: string): string {
  return `${text(localTime(bounds.from * 1000, timezone))} ～ ${text(localTime(bounds.to * 1000 - 1000, timezone))}（${timezone}）`
}

/** 截至当前小时（含）的最近 hours 个小时，按统计时区的整点计算。 */
export function recentHourRange(hours: number, timezone: string, now = Date.now()): TrafficRange {
  const end = floor(localTime(now, timezone), 'hour')
  return [text(step(end, 'hour', 1 - hours)), text(end)]
}



/** 截至今天（含）的最近 days 个自然日，按统计时区计算。 */
export function recentDayRange(days: number, timezone: string, now = Date.now()): TrafficRange {
  const end = floor(localTime(now, timezone), 'day')
  return [text(step(end, 'day', 1 - days)), text(end)]
}

/** 排行的快捷时间段。 */
export type RankingPreset = 'today' | 'yesterday' | 'month' | 'last_month'

/** 快捷时间段的查询边界（Unix 秒），按统计时区的自然日和自然月计算。 */
export function presetBounds(preset: RankingPreset, timezone: string, now = Date.now()) {
  const local = localTime(now, timezone)
  const level: TrafficLevel = preset === 'today' || preset === 'yesterday' ? 'day' : 'month'
  const start = step(floor(local, level), level, preset === 'yesterday' || preset === 'last_month' ? -1 : 0)
  return trafficBounds(level, [text(start), text(start)], timezone)
}

/** 更粗一级的粒度，用于明细超出保留期时建议切换。 */
export function coarserLevel(level: TrafficLevel): TrafficLevel | null {
  return ({ hour: 'day', day: 'month', month: 'year', year: null } as const)[level]
}

/** 流量明细的快捷时间段，每个时间段带有适合它的粒度。 */
export type TrafficPreset = 'today' | 'yesterday' | '7d' | '30d' | 'month' | 'last_month' | '12m' | 'year'

export const trafficPresets: { value: TrafficPreset; label: string }[] = [
  { value: 'today', label: '今日' },
  { value: 'yesterday', label: '昨日' },
  { value: '7d', label: '近 7 天' },
  { value: '30d', label: '近 30 天' },
  { value: 'month', label: '本月' },
  { value: 'last_month', label: '上月' },
  { value: '12m', label: '近 12 个月' },
  { value: 'year', label: '今年' },
]

/** 快捷时间段对应的粒度和范围（首尾都是时间桶的开始时刻，含末尾桶）。 */
export function presetRange(preset: TrafficPreset, timezone: string, now = Date.now()): { level: TrafficLevel; range: TrafficRange } {
  const local = localTime(now, timezone)
  const day = floor(local, 'day')
  const month = floor(local, 'month')
  switch (preset) {
    case 'today': return { level: 'hour', range: [text(day), text(floor(local, 'hour'))] }
    case 'yesterday': {
      const start = step(day, 'day', -1)
      return { level: 'hour', range: [text(start), text(step(start, 'hour', 23))] }
    }
    case '7d': return { level: 'day', range: [text(step(day, 'day', -6)), text(day)] }
    case '30d': return { level: 'day', range: [text(step(day, 'day', -29)), text(day)] }
    case 'month': return { level: 'day', range: [text(month), text(day)] }
    case 'last_month': {
      const start = step(month, 'month', -1)
      return { level: 'day', range: [text(start), text(step(month, 'day', -1))] }
    }
    case '12m': return { level: 'month', range: [text(step(month, 'month', -11)), text(month)] }
    case 'year': return { level: 'month', range: [text(floor(local, 'year')), text(month)] }
  }
}

/** 当前粒度和范围恰好等于某个快捷时间段时返回它。 */
export function matchPreset(level: TrafficLevel, range: TrafficRange, timezone: string, now = Date.now()): TrafficPreset | null {
  const normalized = range.map((value) => text(floor(civil(value), level))).join()
  for (const { value } of trafficPresets) {
    const preset = presetRange(value, timezone, now)
    if (preset.level === level && preset.range.join() === normalized) return value
  }
  return null
}

/** 范围内的时间桶数量（含首尾）。 */
export function bucketCount(level: TrafficLevel, range: TrafficRange): number {
  const end = floor(civil(range[1]), level).getTime()
  let count = 0
  for (let at = floor(civil(range[0]), level); at.getTime() <= end && count < 100_000; at = step(at, level, 1)) count++
  return count
}

/** 切换粒度时换算范围：起点取新粒度的周期开始，终点取原末尾周期内的最后一个新粒度时间桶，且不超过当前时间。 */
export function convertRange(from: TrafficLevel, to: TrafficLevel, range: TrafficRange, timezone: string, now = Date.now()): TrafficRange {
  const start = floor(civil(range[0]), to)
  const lastMoment = new Date(step(floor(civil(range[1]), from), from, 1).getTime() - 1)
  const current = floor(localTime(now, timezone), to)
  let end = floor(lastMoment, to)
  if (end > current) end = current < start ? start : current
  return [text(start), text(end)]
}

/**
 * 把范围整体前移或后移一段。从自然日、自然月、自然年开头起算的范围按完整的自然日、月、年移动
 * （例如今日的小时范围移到昨日全天，本月的日范围移到上月全月），其余按时间桶数量移动。
 */
export function shiftRange(level: TrafficLevel, range: TrafficRange, direction: 1 | -1): TrafficRange {
  const start = floor(civil(range[0]), level)
  const end = floor(civil(range[1]), level)
  const whole = (unit: TrafficLevel, units: number): TrafficRange => {
    const next = step(start, unit, direction * units)
    return [text(next), text(step(step(next, unit, units), level, -1))]
  }
  if (level === 'hour' && start.getUTCHours() === 0) {
    return whole('day', Math.max(1, Math.ceil((end.getTime() - start.getTime() + 3_600_000) / 86_400_000)))
  }
  if (level === 'day' && start.getUTCDate() === 1) {
    return whole('month', (end.getUTCFullYear() - start.getUTCFullYear()) * 12 + end.getUTCMonth() - start.getUTCMonth() + 1)
  }
  if (level === 'month' && start.getUTCMonth() === 0) {
    return whole('year', end.getUTCFullYear() - start.getUTCFullYear() + 1)
  }
  const count = bucketCount(level, range)
  return [text(step(start, level, direction * count)), text(step(end, level, direction * count))]
}

/** 范围的起点是否晚于当前时间所在的时间桶，用于禁用「后一段」。 */
export function rangeInFuture(level: TrafficLevel, range: TrafficRange, timezone: string, now = Date.now()): boolean {
  return floor(civil(range[0]), level) > floor(localTime(now, timezone), level)
}
