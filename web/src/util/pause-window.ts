/** 每日停服时段的展示文字。时段按系统设置的时区定义，时刻也按该时区显示。 */

/** 02:00-08:00、23:00-06:00 → 「02:00–08:00、23:00–次日 06:00」。 */
export function windowsText(windows: string[]): string {
  return windows
    .map((window) => {
      const [start = '', end = ''] = window.split('-')
      return end < start ? `${start}–次日 ${end}` : `${start}–${end}`
    })
    .join('、')
}

function dayKey(date: Date, timeZone: string) {
  return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
}

/** 某个时刻在指定时区的 HH:MM；不在 now 当天时加「明天」或日期。 */
export function clockText(value: string, timeZone: string, now = new Date()): string {
  const zone = timeZone || Intl.DateTimeFormat().resolvedOptions().timeZone
  const at = new Date(value)
  const clock = new Intl.DateTimeFormat('zh-CN', { timeZone: zone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(at)
  const day = dayKey(at, zone)
  if (day === dayKey(now, zone)) return clock
  if (day === dayKey(new Date(now.getTime() + 86_400_000), zone)) return `明天 ${clock}`
  return `${day.slice(5)} ${clock}`
}

/** 零值时间（Go 的 0001-01-01）表示没有。 */
export function hasTime(value: string | undefined): value is string {
  return !!value && new Date(value).getFullYear() > 1
}
