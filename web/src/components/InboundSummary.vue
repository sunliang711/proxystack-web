<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NPopover, NSpace, NTable, NTag, NText } from 'naive-ui'
import type { Schemas } from '../api/client'
import EntityLink from './EntityLink.vue'

const props = defineProps<{ stack: Schemas['StackSummary'] }>()
const protocols = computed(() => {
  const counts = new Map<string, number>()
  for (const inbound of props.stack.inbounds) counts.set(inbound.protocol, (counts.get(inbound.protocol) ?? 0) + 1)
  return [...counts].map(([name, count]) => ({ name, count }))
})
const address = (listen: string, port: number) => `${listen.includes(':') ? `[${listen}]` : listen}:${port}`
</script>

<template>
  <NText v-if="!stack.xray_enabled" depth="3">未启用</NText>
  <NText v-else-if="!stack.inbounds.length" depth="3">暂无入站</NText>
  <NSpace v-else vertical :size="4">
    <NSpace :size="4">
      <NTag v-for="protocol in protocols.slice(0, 2)" :key="protocol.name" size="small" :bordered="false">{{ protocol.name.toUpperCase() }} ×{{ protocol.count }}</NTag>
      <NText v-if="protocols.length > 2" depth="3">另 {{ protocols.length - 2 }} 种</NText>
    </NSpace>
    <NPopover trigger="click" placement="bottom-start" style="max-width: calc(100vw - 32px)">
      <template #trigger>
        <NButton text type="primary" size="small" :aria-label="`查看 ${stack.name} 的 ${stack.inbounds.length} 个入站`">查看 {{ stack.inbounds.length }} 个入站</NButton>
      </template>
      <div style="max-width: calc(100vw - 56px); max-height: 360px; overflow: auto">
        <NText strong><EntityLink kind="stack" :id="stack.id" :name="stack.name" tab="inbounds" /> · Xray 入站</NText>
        <NTable size="small" :bordered="false" style="margin-top: 8px">
          <thead><tr><th>名称</th><th>协议</th><th>监听地址</th></tr></thead>
          <tbody>
            <tr v-for="inbound in stack.inbounds" :key="inbound.name">
              <td>{{ inbound.name }}</td>
              <td>{{ inbound.protocol }}</td>
              <td style="white-space: nowrap">{{ address(inbound.listen, inbound.port) }}</td>
            </tr>
          </tbody>
        </NTable>
      </div>
    </NPopover>
  </NSpace>
</template>
