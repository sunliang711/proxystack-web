<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { NButton, NInput, NRadioButton, NRadioGroup, NScrollbar, NSpace, NSwitch, NText } from 'naive-ui'
import { panelBase, type Schemas } from '../../api/client'

const props = defineProps<{ stack: string }>()
const component = ref<'all' | 'xray' | 'mihomo'>('all')
const paused = ref(false)
const keyword = ref('')
const entries = ref<Schemas['LogEntry'][]>([])
const scroller = ref<InstanceType<typeof NScrollbar>>()
let source: EventSource | null = null
const buffer: Schemas['LogEntry'][] = []

function connect() {
  source?.close()
  entries.value = []
  buffer.length = 0
  const params = new URLSearchParams({ stack: props.stack, component: component.value, lines: '300' })
  source = new EventSource(`${panelBase}api/runtime/logs?${params}`)
  source.addEventListener('log', (message) => {
    buffer.push(JSON.parse((message as MessageEvent).data))
    if (!paused.value) flush()
  })
}

function flush() {
  entries.value = [...entries.value, ...buffer.splice(0)].slice(-2000)
  nextTick(() => scroller.value?.scrollTo({ top: 1e9 }))
}

watch([() => props.stack, component], connect, { immediate: true })
watch(paused, (value) => !value && flush())
onBeforeUnmount(() => source?.close())

const visible = computed(() => {
  const needle = keyword.value.trim().toLowerCase()
  return needle ? entries.value.filter((e) => e.message.toLowerCase().includes(needle)) : entries.value
})
const level = (priority: number) => (priority <= 3 ? 'error' : priority === 4 ? 'warning' : '')
</script>

<template>
  <NSpace align="center" style="margin-bottom: 8px">
    <NRadioGroup v-model:value="component" size="small">
      <NRadioButton value="all">全部</NRadioButton>
      <NRadioButton value="xray">xray</NRadioButton>
      <NRadioButton value="mihomo">mihomo</NRadioButton>
    </NRadioGroup>
    <NInput v-model:value="keyword" size="small" placeholder="关键字过滤" clearable style="width: 220px" />
    <NSwitch v-model:value="paused" size="small"><template #checked>已暂停</template><template #unchecked>实时</template></NSwitch>
    <NButton size="small" @click="entries = []">清除当前视图</NButton>
    <NText depth="3">{{ visible.length }} 行</NText>
  </NSpace>
  <NScrollbar ref="scroller" style="max-height: 560px; border: 1px solid rgba(128, 128, 128, 0.25); border-radius: 3px">
    <pre class="logs"><span v-for="(entry, i) in visible" :key="i" :class="level(entry.priority)">{{ new Date(entry.time).toLocaleTimeString() }} {{ entry.unit.replace('psw-', '').replace('.service', '') }} {{ entry.message }}
</span></pre>
  </NScrollbar>
</template>

<style scoped>
.logs {
  margin: 0;
  padding: 6px 8px;
  font-family: ui-monospace, monospace;
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
}
.error {
  color: #e88080;
}
.warning {
  color: #f2c97d;
}
</style>
