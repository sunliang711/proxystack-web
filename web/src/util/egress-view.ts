/** Stack 列表出口 IP 列的显示规则。 */

export type HealthStatus = 'unknown' | 'healthy' | 'failing' | 'down' | 'stopped' | 'unsupported' | 'paused' | 'unavailable'

export interface HealthView {
  /** 圆点颜色。 */
  tone: 'success' | 'warning' | 'error' | 'default'
  /** 完整说明，用于悬停提示。 */
  text: string
  /** 异常时显示在圆点旁的短文字；正常或无需关注时为空。 */
  badge: string
}

const views: Record<HealthStatus, HealthView> = {
  healthy: { tone: 'success', text: '后台检测正常', badge: '' },
  failing: { tone: 'warning', text: '出口待确认：最近的后台检测失败', badge: '待确认' },
  down: { tone: 'error', text: '出口异常：后台检测连续失败', badge: '异常' },
  unavailable: { tone: 'warning', text: '暂时无法读取运行状态，本轮跳过检测', badge: '状态未知' },
  unknown: { tone: 'default', text: '后台尚未检测', badge: '' },
  stopped: { tone: 'default', text: '检测暂停：mihomo 未运行', badge: '' },
  unsupported: { tone: 'default', text: '暂不支持检测：需要已应用的 Mihomo SOCKS 监听', badge: '' },
  paused: { tone: 'default', text: '后台出口检测任务已关闭', badge: '' },
}

/** 后台检测状态对应的圆点；状态接口本身读取失败时显示为待确认。 */
export function healthView(status: HealthStatus | undefined, refreshFailed = false): HealthView {
  if (refreshFailed) return { tone: 'warning', text: '后台检测状态刷新失败，显示的是上次结果', badge: '刷新失败' }
  return views[status ?? 'unknown']
}

const suffix = /[\s,]+(inc\.?|llc|l\.l\.c\.|ltd\.?|limited|gmbh|ag|co\.?|corp\.?|corporation|company|s\.?a\.?|b\.?v\.?|plc|online|networks?|network services|technologies|technology|communications|hosting|holdings?)$/i

/** 运营商简称：去掉开头的 AS 号和末尾的公司类型后缀，例如 “AS24940 Hetzner Online GmbH” → “Hetzner”。 */
export function shortOrg(org: string): string {
  let name = org.trim().replace(/^AS\d+\s+/i, '')
  for (let previous = ''; previous !== name;) {
    previous = name
    const next = name.replace(suffix, '').trim()
    if (next) name = next
  }
  return name.replace(/,$/, '')
}

/** 城市优先；数据源不给城市时用地区，去掉开头的国家代码。 */
export function shortLocation(city: string, region: string, country: string): string {
  if (city) return city
  const parts = region.split(/\s+/).filter(Boolean)
  if (parts[0] === country) parts.shift()
  return parts.join(' ')
}
