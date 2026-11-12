<script setup lang="ts">
import { NButton, NSpace, NText, NTimePicker } from 'naive-ui'

/** 编辑每日停服时段列表，每项为 HH:MM-HH:MM，结束早于开始表示跨零点。 */
const value = defineModel<string[]>('value', { required: true })

function parts(window: string): [string, string] {
  const [start = '', end = ''] = window.split('-')
  return [start, end]
}
function toMillis(text: string): number | null {
  const [h, m] = text.split(':').map(Number)
  return Number.isFinite(h) && Number.isFinite(m) && text ? new Date(2000, 0, 1, h, m).getTime() : null
}
function toText(millis: number | null): string {
  if (millis === null) return ''
  const date = new Date(millis)
  return `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
}
function update(index: number, side: 0 | 1, millis: number | null) {
  const next = [...value.value]
  const current = parts(next[index] ?? '')
  current[side] = toText(millis)
  next[index] = `${current[0]}-${current[1]}`
  value.value = next
}
function crosses(window: string) {
  const [start, end] = parts(window)
  return start && end && end < start
}
</script>

<template>
  <NSpace vertical :size="6">
    <NSpace v-for="(window, index) in value" :key="index" align="center" :size="8">
      <NTimePicker :value="toMillis(parts(window)[0])" format="HH:mm" placeholder="开始" style="width: 110px" @update:value="(v) => update(index, 0, v)" />
      <span>—</span>
      <NTimePicker :value="toMillis(parts(window)[1])" format="HH:mm" placeholder="结束" style="width: 110px" @update:value="(v) => update(index, 1, v)" />
      <NText v-if="crosses(window)" depth="3">次日</NText>
      <NButton size="tiny" quaternary @click="value = value.filter((_, i) => i !== index)">删除</NButton>
    </NSpace>
    <NButton size="small" dashed @click="value = [...value, '02:00-08:00']">添加时段</NButton>
  </NSpace>
</template>
