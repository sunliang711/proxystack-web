/** 图表分类色：固定顺序分配给对象，深色主题提高亮度以突出数据。 */
export const seriesColors = {
  light: ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#6250d6', '#e34948'],
  dark: ['#52d6b4', '#6aa8f5', '#f3b76b', '#b59bf5', '#ec8cac', '#90ce7f', '#78cfdf', '#ef8383'],
}

/** 坐标轴、网格等次要元素的颜色，图表文字不使用系列色。 */
export const chartTokens = {
  light: { surface: '#ffffff', text: '#52514e', muted: '#8a8984', grid: '#ecebe7', other: '#a3a29c' },
  dark: { surface: '#131d2b', text: '#c5d1e2', muted: '#94a6bf', grid: '#243247', other: '#71859f' },
}

export interface ChartSeries {
  name: string
  data: number[]
  /** 分类色槽位；未设置时使用「其他」的中性色。 */
  slot?: number
}
