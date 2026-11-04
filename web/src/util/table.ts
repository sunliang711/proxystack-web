import { h } from 'vue'
import { NIcon, NSpace, NTooltip } from 'naive-ui'
import { InformationCircleOutline } from '@vicons/ionicons5'

/** 表头文字加一个悬停说明图标，用于解释列含义。 */
export function tipTitle(label: string, tip: string) {
  return () =>
    h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
      label,
      h(NTooltip, { style: 'max-width: min(420px, calc(100vw - 32px))' }, {
        trigger: () => h(NIcon, { size: 14, style: 'opacity: 0.5; cursor: help', 'aria-label': `${label}说明` }, () => h(InformationCircleOutline)),
        default: () => tip,
      }),
    ])
}
