export function formatBytes(bytes: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${Number.isInteger(value) ? value : value.toFixed(2)} ${units[unit]}`
}

export function formatRate(bytesPerSecond: number): string {
  return `${formatBytes(Math.round(bytesPerSecond))}/s`
}

export function formatTime(value: string | number | Date): string {
  return new Date(value).toLocaleString('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  })
}
