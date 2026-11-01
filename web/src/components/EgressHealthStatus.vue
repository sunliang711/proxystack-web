<script setup lang="ts">
import { computed } from 'vue'
import { NTag, NText, NTooltip } from 'naive-ui'
import type { Schemas } from '../api/client'
import { formatTime } from '../util/format'

const props = defineProps<{ health?: Schemas['EgressHealth']; error?: string }>()
const labels: Record<Schemas['EgressHealth']['status'], { text: string; type: 'default' | 'success' | 'warning' | 'error' }> = {
  unknown: { text: '待检测', type: 'default' },
  unavailable: { text: '运行状态查询失败', type: 'warning' },
  healthy: { text: '后台正常', type: 'success' },
  failing: { text: '出口待确认', type: 'warning' },
  down: { text: '出口异常', type: 'error' },
  stopped: { text: '检测暂停：未运行', type: 'default' },
  unsupported: { text: '暂不支持检测', type: 'default' },
  paused: { text: '检测任务已关闭', type: 'default' },
}
const view = computed(() => props.health ? labels[props.health.status] : labels.unknown)
const checked = computed(() => props.health?.checked_at && !props.health.checked_at.startsWith('0001-') ? formatTime(props.health.checked_at) : '')
</script>

<template>
  <NTooltip style="max-width: 460px; white-space: pre-wrap">
    <template #trigger>
      <div class="egress-health">
        <NTag size="tiny" :bordered="false" :type="error ? 'warning' : view.type">{{ error ? '状态刷新失败' : view.text }}</NTag>
        <NText v-if="checked" depth="3" style="font-size: 11px">{{ checked }}</NText>
      </div>
    </template>
    <div v-if="error">{{ error }}，当前保留上次结果</div>
    <div>后台独立检测，和上方浏览器缓存的出口 IP 分开显示；手动刷新 IP 不累计失败次数。</div>
    <template v-if="health">
      <div v-if="health.status === 'unavailable'">暂时无法读取此 Stack 的运行状态，本轮跳过检测并保留原有记录。</div>
      <div v-if="health.status === 'unsupported'">需要已应用的 Mihomo SOCKS 监听；仅运行 Xray 的 Stack 暂不支持。</div>
      <div v-if="checked">最后完成检测：{{ checked }}</div>
      <div>后台 IPv4：{{ health.ipv4 || '-' }}</div>
      <div>后台 IPv6：{{ health.ipv6 || '-' }}</div>
      <div v-if="health.failures">连续失败：{{ health.failures }} 次</div>
      <div v-if="health.ipv4_error">IPv4：{{ health.ipv4_error }}</div>
      <div v-if="health.ipv6_error">IPv6：{{ health.ipv6_error }}</div>
    </template>
  </NTooltip>
</template>

<style scoped>
.egress-health {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
}
</style>
