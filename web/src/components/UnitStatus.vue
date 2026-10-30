<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NDropdown, NSpace, NTag, NTooltip, useMessage } from 'naive-ui'
import { api, problemMessage } from '../api/client'
import { runtime, unitLabel } from '../runtime'

const props = defineProps<{ stack: string; actions?: boolean }>()
const message = useMessage()
const state = computed(() => runtime.stacks[props.stack])

const options = [
  { label: '启动全部', key: 'start:all' },
  { label: '重启全部', key: 'restart:all' },
  { label: '停止全部', key: 'stop:all' },
  { type: 'divider', key: 'd1' },
  { label: '重启 xray', key: 'restart:xray' },
  { label: '重启 mihomo', key: 'restart:mihomo' },
  { label: '停止 xray', key: 'stop:xray' },
  { label: '停止 mihomo', key: 'stop:mihomo' },
]

async function run(key: string) {
  const [action, component] = key.split(':') as ['start' | 'stop' | 'restart', 'all' | 'xray' | 'mihomo']
  const { error } = await api.POST('/api/runtime/units', { body: { stack: props.stack, component, action } })
  if (error) {
    message.error(problemMessage(error, '操作失败') + (error.errors?.map((e) => `：${e.message}`).join('') ?? ''))
  } else {
    message.success('已执行')
  }
}
</script>

<template>
  <NSpace :size="4" align="center" :wrap="false">
    <template v-if="state">
      <NTooltip v-for="unit in state.units" :key="unit.unit">
        <template #trigger>
          <NTag size="small" :type="unitLabel(unit).type" :bordered="false">
            {{ unit.unit.includes('xray') ? 'xray' : 'mihomo' }} {{ unitLabel(unit).text }}
          </NTag>
        </template>
        {{ unit.unit }}<span v-if="unit.since && !unit.since.startsWith('0001')">，自 {{ new Date(unit.since).toLocaleString() }}</span>
      </NTooltip>
      <NTag v-if="state.drift" size="small" type="error">生成物被改动</NTag>
      <NTag v-if="!state.applied_revision" size="small">未应用</NTag>
    </template>
    <NDropdown v-if="actions" :options="options" trigger="click" @select="run">
      <NButton size="tiny" quaternary>操作</NButton>
    </NDropdown>
  </NSpace>
</template>
