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
