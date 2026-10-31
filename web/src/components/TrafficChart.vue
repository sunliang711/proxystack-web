<script setup lang="ts">
import { computed } from 'vue'
import { use } from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, MarkLineComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import VChart from 'vue-echarts'
import { isDark } from '../theme'
import { chartTokens, seriesColors, type ChartSeries } from '../util/chart'

use([BarChart, LineChart, GridComponent, LegendComponent, MarkLineComponent, TooltipComponent, CanvasRenderer])

const props = defineProps<{
  kind: 'bar' | 'line'
  labels: string[]
  series: ChartSeries[]
  format: (value: number) => string
  /** 参考线，例如每日阈值。 */
  reference?: { value: number; label: string }
  height?: string
}>()

const mode = computed(() => (isDark.value ? 'dark' : 'light'))

const option = computed(() => {
  const tokens = chartTokens[mode.value]
  const palette = seriesColors[mode.value]
  const last = props.series.length - 1
  return {
    animation: false,
    textStyle: { fontFamily: 'inherit' },
    grid: { left: 8, right: 24, top: props.series.length > 1 ? 36 : 16, bottom: 8, containLabel: true },
    legend: {
      show: props.series.length > 1, top: 0, left: 0, itemWidth: 12, itemHeight: 12, icon: 'roundRect',
      textStyle: { color: tokens.text },
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: props.kind === 'line' ? 'line' : 'shadow' },
      valueFormatter: (value: number) => props.format(value ?? 0),
    },
    xAxis: {
      type: 'category', data: props.labels, boundaryGap: props.kind === 'bar',
      axisLine: { lineStyle: { color: tokens.grid } }, axisTick: { show: false },
      axisLabel: { color: tokens.muted, hideOverlap: true },
    },
    yAxis: {
      type: 'value', splitNumber: 4, minInterval: 1,
      axisLabel: { color: tokens.muted, formatter: (value: number) => props.format(value) },
      splitLine: { lineStyle: { color: tokens.grid } },
    },
    series: props.series.map((series, index) => {
      const color = series.slot === undefined ? tokens.other : palette[series.slot]
      const reference = index === 0 && props.reference
        ? {
            markLine: {
              silent: true, symbol: 'none',
              lineStyle: { color: tokens.text, type: 'dashed', width: 1 },
              label: { color: tokens.text, position: 'insideEndTop', formatter: props.reference.label },
              data: [{ yAxis: props.reference.value }],
            },
          }
        : {}
      if (props.kind === 'bar') {
        return {
          ...reference, name: series.name, type: 'bar', stack: 'total', data: series.data, barMaxWidth: 24,
          itemStyle: { color, borderColor: tokens.surface, borderWidth: 1, borderRadius: index === last ? [4, 4, 0, 0] : 0 },
        }
      }
      return {
        ...reference, name: series.name, type: 'line', data: series.data, showSymbol: false, symbolSize: 8,
        lineStyle: { width: 2, color }, itemStyle: { color },
      }
    }),
  }
})
</script>

<template>
  <VChart :option="option" autoresize :style="{ height: height ?? '280px', width: '100%' }" />
</template>
