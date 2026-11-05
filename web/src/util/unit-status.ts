import type { Schemas } from '../api/client'

/**
 * 列表中的紧凑状态：运行中只显示进程名（绿色）；其他状态附上两个字。
 * 已停止时按是否符合预期区分：stack 设为保持停止时是灰色，本应运行却停了是红色。
 */
export function unitBrief(unit: Schemas['UnitView'], desiredRunning: boolean): { text: string; type: 'success' | 'error' | 'warning' | 'info' | 'default' } {
  const name = unit.unit.includes('xray') ? 'xray' : 'mihomo'
  if (!unit.present) return { text: `${name} 未生成`, type: 'default' }
  switch (unit.active) {
    case 'active':
      return { text: name, type: 'success' }
    case 'activating':
    case 'reloading':
      return { text: `${name} 启动中`, type: 'info' }
    case 'failed':
      return { text: `${name} 失败`, type: 'error' }
    case 'deactivating':
      return { text: `${name} 停止中`, type: 'warning' }
    default:
      return { text: `${name} 已停止`, type: desiredRunning ? 'error' : 'default' }
  }
}
