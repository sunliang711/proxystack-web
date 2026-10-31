/** 图表分类色：固定顺序分配给对象，已通过 CVD 和对比度校验（亮色面 #ffffff，暗色面 #18181c）。 */
export const seriesColors = {
  light: ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#6250d6', '#e34948'],
  dark: ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181', '#008300', '#9085e9', '#e66767'],
}

/** 坐标轴、网格等次要元素的颜色，图表文字不使用系列色。 */
export const chartTokens = {
  light: { surface: '#ffffff', text: '#52514e', muted: '#8a8984', grid: '#ecebe7', other: '#a3a29c' },
  dark: { surface: '#18181c', text: '#c3c2b7', muted: '#8f8e86', grid: '#2e2e33', other: '#6d6c66' },
}

export interface ChartSeries {
  name: string
  data: number[]
  /** 分类色槽位；未设置时使用「其他」的中性色。 */
  slot?: number
}
