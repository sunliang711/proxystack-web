<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NPopover, NSpace, NTable, NTag, NText } from 'naive-ui'
import type { Schemas } from '../api/client'

const props = defineProps<{ stack: Schemas['StackSummary'] }>()
const listeners = computed(() => [
  { protocol: 'SOCKS5', address: props.stack.mihomo_socks },
  { protocol: 'HTTP', address: props.stack.mihomo_http },
].filter((listener) => listener.address))
</script>

<template>
  <NText v-if="!stack.mihomo_enabled" depth="3">未启用</NText>
  <NText v-else-if="!listeners.length" depth="3">暂无监听</NText>
  <NSpace v-else vertical :size="4">
    <NSpace :size="4">
      <NTag v-for="listener in listeners" :key="listener.protocol" size="small" :bordered="false">{{ listener.protocol }}</NTag>
    </NSpace>
    <NPopover trigger="click" placement="bottom-start" style="max-width: calc(100vw - 32px)">
      <template #trigger>
        <NButton text type="primary" size="small" :aria-label="`查看 ${stack.name} 的 ${listeners.length} 个 Mihomo 监听`">查看 {{ listeners.length }} 个监听</NButton>
      </template>
      <div style="max-width: calc(100vw - 56px); overflow: auto">
        <NText strong>{{ stack.name }} · Mihomo 监听</NText>
        <NTable size="small" :bordered="false" style="margin-top: 8px">
          <thead><tr><th>协议</th><th>监听地址</th></tr></thead>
          <tbody>
            <tr v-for="listener in listeners" :key="listener.protocol">
              <td>{{ listener.protocol }}</td>
              <td style="white-space: nowrap">{{ listener.address }}</td>
            </tr>
          </tbody>
        </NTable>
      </div>
    </NPopover>
  </NSpace>
</template>
