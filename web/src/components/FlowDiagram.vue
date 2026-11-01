<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useThemeVars } from 'naive-ui'
import { isDark } from '../theme'
import type { FlowEdge, FlowNode, FlowTone } from '../util/flow'

const props = defineProps<{ nodes: FlowNode[]; edges: FlowEdge[]; columns: string[] }>()
const emit = defineEmits<{ select: [node: FlowNode] }>()

const theme = useThemeVars()

const NODE_WIDTH = 180
const COLUMN_GAP = 70
const NODE_GAP = 14
const PAD_X = 12
const HEADER = 30
const TITLE_HEIGHT = 20
const LINE_HEIGHT = 16
const PAD_Y = 10

const tones: Record<FlowTone, [light: string, dark: string]> = {
  client: ['#2080f0', '#70c0e8'],
  inbound: ['#2080f0', '#70c0e8'],
  stack: ['#18a058', '#63e2b7'],
  auto: ['#8a63d2', '#b79bf0'],
  xray: ['#18a058', '#63e2b7'],
  mihomo: ['#d97706', '#f2c97d'],
  rules: ['#d97706', '#f2c97d'],
  group: ['#8a63d2', '#b79bf0'],
  upstream: ['#0e9f8e', '#5fd4c4'],
  internet: ['#64748b', '#a3b1c6'],
  reject: ['#d03050', '#e88080'],
}
const tone = (name: FlowTone = 'internet') => tones[name][isDark.value ? 1 : 0]

/** 按显示宽度截断文字：中日韩字符按两个单位计。 */
function clip(text: string, units: number): string {
  let used = 0
  for (let index = 0; index < text.length; index++) {
    used += text.charCodeAt(index) > 0x2e80 ? 2 : 1
    if (used > units) return text.slice(0, Math.max(0, index - 1)) + '…'
  }
  return text
}

interface Placed extends FlowNode {
  x: number
  y: number
  height: number
}

const layout = computed(() => {
  const columnCount = Math.max(props.columns.length, ...props.nodes.map((node) => node.column + 1))
  const heights = Array.from({ length: columnCount }, () => 0)
  const placed: Placed[] = []
  for (const node of props.nodes) {
    const lines = node.lines?.length ?? 0
    const height = PAD_Y * 2 + TITLE_HEIGHT + (node.subtitle ? LINE_HEIGHT : 0) + lines * LINE_HEIGHT
    placed.push({ ...node, x: PAD_X + node.column * (NODE_WIDTH + COLUMN_GAP), y: heights[node.column], height })
    heights[node.column] += height + NODE_GAP
  }
  const tallest = Math.max(...heights.map((value) => value - NODE_GAP), 60)
  // 每一列在整体高度内垂直居中。
  for (const node of placed) {
    node.y += HEADER + (tallest - (heights[node.column] - NODE_GAP)) / 2
  }
  return {
    nodes: placed,
    byId: new Map(placed.map((node) => [node.id, node])),
    width: PAD_X * 2 + columnCount * NODE_WIDTH + (columnCount - 1) * COLUMN_GAP,
    height: HEADER + tallest + 8,
    columnX: (column: number) => PAD_X + column * (NODE_WIDTH + COLUMN_GAP) + NODE_WIDTH / 2,
  }
})

const paths = computed(() =>
  props.edges.flatMap((edge) => {
    const from = layout.value.byId.get(edge.from)
    const to = layout.value.byId.get(edge.to)
    if (!from || !to) return []
    const x1 = from.x + NODE_WIDTH
    const y1 = from.y + from.height / 2
    const x2 = to.x
    const y2 = to.y + to.height / 2
    const bend = Math.max(36, (x2 - x1) / 2)
    return [{ ...edge, key: `${edge.from}>${edge.to}`, d: `M${x1},${y1} C${x1 + bend},${y1} ${x2 - bend},${y2} ${x2},${y2}`, mx: (x1 + x2) / 2, my: (y1 + y2) / 2, color: tone(from.tone) }]
  }),
)

const hovered = ref<string | null>(null)
const related = computed(() => {
  if (!hovered.value) return null
  const ids = new Set([hovered.value])
  for (const edge of props.edges) {
    if (edge.from === hovered.value) ids.add(edge.to)
    if (edge.to === hovered.value) ids.add(edge.from)
  }
  return ids
})
const edgeActive = (edge: FlowEdge) => !hovered.value || edge.from === hovered.value || edge.to === hovered.value

// 图比容器宽时等比缩小到容器宽度，最多缩到 MIN_SCALE，再窄就横向滚动。
const MIN_SCALE = 0.72
const container = ref<HTMLDivElement>()
const available = ref(0)
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = new ResizeObserver(([entry]) => (available.value = entry.contentRect.width))
  if (container.value) observer.observe(container.value)
})
onBeforeUnmount(() => observer?.disconnect())
const scale = computed(() => (available.value ? Math.min(1, Math.max(MIN_SCALE, available.value / layout.value.width)) : 1))
</script>

<template>
  <div ref="container" class="flow">
    <svg :width="layout.width * scale" :height="layout.height * scale" :viewBox="`0 0 ${layout.width} ${layout.height}`" role="img">
      <defs>
        <marker id="flow-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M0,0 L10,5 L0,10 z" :fill="theme.textColor3" />
        </marker>
      </defs>

      <text
        v-for="(title, column) in columns"
        :key="title + column"
        :x="layout.columnX(column)"
        y="16"
        text-anchor="middle"
        class="heading"
        :fill="theme.textColor3"
      >
        {{ title }}
      </text>

      <g v-for="edge in paths" :key="edge.key" :class="['edge', { faded: !edgeActive(edge) }]">
        <path :d="edge.d" fill="none" :stroke="edge.color" stroke-opacity="0.22" stroke-width="2" marker-end="url(#flow-arrow)" />
        <path :d="edge.d" fill="none" :stroke="edge.color" stroke-width="2" :class="edge.dashed ? 'static' : 'stream'" stroke-linecap="round" />
        <g v-if="edge.label">
          <rect
            :x="edge.mx - clip(edge.label, 18).length * 3.4 - 6"
            :y="edge.my - 9"
            :width="clip(edge.label, 18).length * 6.8 + 12"
            height="18"
            rx="9"
            :fill="theme.cardColor"
            :stroke="theme.dividerColor"
          />
          <text :x="edge.mx" :y="edge.my + 4" text-anchor="middle" class="edge-label" :fill="theme.textColor2">{{ clip(edge.label, 18) }}</text>
        </g>
      </g>

      <g
        v-for="node in layout.nodes"
        :key="node.id"
        :class="['node', { clickable: node.clickable, faded: related && !related.has(node.id), dim: node.dim }]"
        @mouseenter="hovered = node.id"
        @mouseleave="hovered = null"
        @click="node.clickable && emit('select', node)"
      >
        <title>{{ [node.title, node.subtitle, ...(node.lines ?? []), node.hint].filter(Boolean).join('\n') }}</title>
        <rect :x="node.x" :y="node.y" :width="NODE_WIDTH" :height="node.height" rx="10" :fill="theme.cardColor" :stroke="theme.borderColor" />
        <rect :x="node.x" :y="node.y" width="4" :height="node.height" rx="2" :fill="tone(node.tone)" />
        <text :x="node.x + 14" :y="node.y + PAD_Y + 14" class="title" :fill="theme.textColor1">{{ clip(node.title, node.badge ? 15 : 22) }}</text>
        <g v-if="node.badge">
          <rect :x="node.x + NODE_WIDTH - 10 - node.badge.length * 7 - 10" :y="node.y + PAD_Y + 1" :width="node.badge.length * 7 + 10" height="17" rx="8.5" :fill="tone(node.tone)" fill-opacity="0.14" />
          <text :x="node.x + NODE_WIDTH - 15 - (node.badge.length * 7) / 2" :y="node.y + PAD_Y + 13" text-anchor="middle" class="badge" :fill="tone(node.tone)">{{ node.badge }}</text>
        </g>
        <text v-if="node.subtitle" :x="node.x + 14" :y="node.y + PAD_Y + TITLE_HEIGHT + 11" class="subtitle" :fill="tone(node.tone)">
          {{ clip(node.subtitle, 26) }}
        </text>
        <text
          v-for="(line, index) in node.lines ?? []"
          :key="index"
          :x="node.x + 14"
          :y="node.y + PAD_Y + TITLE_HEIGHT + (node.subtitle ? LINE_HEIGHT : 0) + index * LINE_HEIGHT + 11"
          class="line"
          :fill="theme.textColor3"
        >
          {{ clip(line, 26) }}
        </text>
      </g>
    </svg>
  </div>
</template>

<style scoped>
.flow {
  overflow-x: auto;
  overflow-y: hidden;
  padding: 20px 8px;
  border: 1px solid var(--ps-border);
  border-radius: 10px;
  background: radial-gradient(circle, var(--ps-border) 0.7px, transparent 0.7px) 0 0 / 18px 18px, var(--ps-bg);
}
svg {
  display: block;
  font-family: inherit;
}
.heading {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 1px;
}
.title {
  font-size: 13px;
  font-weight: 600;
}
.subtitle {
  font-size: 11px;
  font-weight: 500;
}
.line {
  font-size: 11px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
.badge {
  font-size: 10px;
  font-weight: 600;
}
.edge-label {
  font-size: 10.5px;
}
.node,
.edge {
  transition: opacity 0.15s;
}
.node.clickable {
  cursor: pointer;
}
.node.clickable:hover rect:first-of-type {
  stroke-width: 1.5;
}
.node.dim {
  opacity: 0.45;
}
.faded {
  opacity: 0.18;
}
/* 流动的虚线表示流量方向。 */
.stream {
  stroke-dasharray: 2 10;
  animation: flow 1.2s linear infinite;
}
.static {
  stroke-dasharray: 4 6;
  stroke-opacity: 0.6;
}
@keyframes flow {
  to {
    stroke-dashoffset: -12;
  }
}
@media (prefers-reduced-motion: reduce) {
  .stream {
    animation: none;
  }
}
</style>
