<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { use } from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, MarkLineComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { CallbackDataParams } from 'echarts/types/dist/shared'
import VChart from 'vue-echarts'
import { isDark } from '../theme'
import { chartTokens, seriesColors, type ChartSeries } from '../util/chart'
import { useEntityDirectory } from '../composables/useEntityReference'

use([BarChart, LineChart, GridComponent, LegendComponent, MarkLineComponent, TooltipComponent, CanvasRenderer])

const props = defineProps<{
  kind: 'bar' | 'line'
  labels: string[]
  series: ChartSeries[]
  format: (value: number) => string
  /** 参考线，例如每日阈值。 */
  reference?: { value: number; label: string }
  height?: string
  /** 柱状图提示框是否显示各项合计；各项之间有重复计数时关闭。 */
  hideTotal?: boolean
}>()

const mode = computed(() => (isDark.value ? 'dark' : 'light'))
const router = useRouter()
const resolve = useEntityDirectory(() => props.series.flatMap((series) => series.entity ? [series.entity.kind] : []))
function openSeries(event: { seriesIndex?: number }) {
  const entity = event.seriesIndex === undefined ? undefined : props.series[event.seriesIndex]?.entity
  const record = entity && resolve(entity)
  if (record) router.push({ name: entity!.kind, params: { id: record.id! } })
}

function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]!)
}

function tooltipFormatter(params: CallbackDataParams | CallbackDataParams[]): string {
  const items = Array.isArray(params) ? params : [params]
  const values = items.map((item) => typeof item.value === 'number' ? item.value : Number(item.value) || 0)
  const excluded = (item: CallbackDataParams) => props.series[item.seriesIndex ?? -1]?.excludeFromTotal === true
  const lines = items.map((item, index) => `${item.marker ?? ''}${escapeHtml(item.seriesName ?? '')}${excluded(item) ? '（不计入合计）' : ''}: ${props.format(values[index]!)}`)
  const total = values.reduce((sum, value, index) => (excluded(items[index]!) ? sum : sum + value), 0)
  const head = `${escapeHtml(items[0]?.name ?? '')}<br/>${lines.join('<br/>')}`
  return props.hideTotal ? head : `${head}<br/><strong>合计：${props.format(total)}</strong>`
}

/** 全部为 0 时坐标轴会自动取 0～1，只保留 0 的刻度文字，避免出现无意义的「1 B/s」。 */
const allZero = computed(() => props.series.every((series) => series.data.every((value) => !value)))

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
      backgroundColor: tokens.surface,
      borderColor: tokens.grid,
      textStyle: { color: tokens.text },
      axisPointer: { type: props.kind === 'line' ? 'line' : 'shadow' },
      valueFormatter: (value: number) => props.format(value ?? 0),
      formatter: props.kind === 'bar' ? tooltipFormatter : undefined,
    },
    xAxis: {
      type: 'category', data: props.labels, boundaryGap: props.kind === 'bar',
      axisLine: { lineStyle: { color: tokens.grid } }, axisTick: { show: false },
      axisLabel: { color: tokens.muted, hideOverlap: true },
    },
    yAxis: {
      type: 'value', splitNumber: 4, minInterval: 1,
      axisLabel: { color: tokens.muted, formatter: (value: number) => (allZero.value && value !== 0 ? '' : props.format(value)) },
      splitLine: { lineStyle: { color: tokens.grid } },
    },
    series: props.series.map((series, index) => {
      const color = series.slot === undefined ? tokens.other : palette[series.slot]
      const cursor = series.entity && resolve(series.entity) ? 'pointer' : 'default'
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
          ...reference, name: series.name, type: 'bar', stack: 'total', data: series.data, barMaxWidth: 24, cursor,
          itemStyle: { color, borderColor: tokens.surface, borderWidth: 1, borderRadius: index === last ? [4, 4, 0, 0] : 0 },
        }
      }
      return {
        ...reference, name: series.name, type: 'line', data: series.data, showSymbol: false, symbolSize: 8, cursor,
        lineStyle: { width: 2, color }, itemStyle: { color },
      }
    }),
  }
})
</script>

<template>
  <VChart :option="option" autoresize :style="{ height: height ?? '280px', width: '100%' }" @click="openSeries" />
</template>
