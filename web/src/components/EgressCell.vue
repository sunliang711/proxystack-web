<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NIcon, NSpin, NTag, NText, NTooltip } from 'naive-ui'
import { RefreshOutline } from '@vicons/ionicons5'
import type { EgressState } from '../composables/useEgress'
import { formatTime } from '../util/format'

const props = defineProps<{ state?: EgressState; mihomoEnabled: boolean }>()
const emit = defineEmits<{ refresh: [] }>()

/** 两位国家代码转成旗帜 emoji。 */
const flag = computed(() => {
  const code = props.state?.info?.country
  if (!code) return ''
  return String.fromCodePoint(...[...code].map((char) => 0x1f1e6 + char.charCodeAt(0) - 65))
})

/** 优先显示城市；数据源不给城市时用地区，去掉开头的国家代码。 */
const location = computed(() => {
  const info = props.state?.info
  if (!info) return ''
  if (info.city) return info.city
  const parts = info.region.split(/\s+/).filter(Boolean)
  if (parts[0] === info.country) parts.shift()
  return parts.join(' ')
})

const detail = computed(() => [location.value, props.state?.info?.org].filter(Boolean).join(' · '))
const source = computed(() => props.state?.info?.sources.find((item) => item.ip)?.url ?? '')
</script>

<template>
  <NText v-if="!mihomoEnabled" depth="3">未启用 Mihomo</NText>
  <NText v-else-if="!state" depth="3">—</NText>
  <div v-else class="egress">
    <div class="body">
      <span v-if="state.status === 'loading' && !state.info?.ip" class="pending">
        <NSpin :size="14" />
        <NText depth="3">检测中…</NText>
      </span>
      <NTag v-else-if="state.status === 'stopped'" size="small" :bordered="false">mihomo 未运行</NTag>
      <NTooltip v-else-if="state.status === 'error' || state.status === 'empty'" placement="top-start" style="max-width: 420px">
        <template #trigger>
          <NTag size="small" :type="state.status === 'error' ? 'error' : 'warning'" :bordered="false">
            {{ state.status === 'error' ? '查询失败' : '未取到地址' }}
          </NTag>
        </template>
        <div class="pre">{{ state.error }}</div>
      </NTooltip>
      <NTooltip v-else placement="top-start">
        <template #trigger>
          <div :class="{ stale: state.status === 'loading' }">
            <div class="ip-line">
              <span v-if="flag" class="flag">{{ flag }}</span>
              <span class="ip">{{ state.info?.ip }}</span>
            </div>
            <div v-if="detail" class="detail">{{ detail }}</div>
          </div>
        </template>
        <div>IP：{{ state.info?.ip }}</div>
        <div v-if="state.info?.region">地区：{{ state.info.region }}</div>
        <div v-if="state.info?.org">运营商：{{ state.info.org }}</div>
        <div v-if="source">数据源：{{ source }}</div>
        <div v-if="state.at">更新于 {{ formatTime(state.at) }}</div>
      </NTooltip>
    </div>
    <NTooltip v-if="state.status !== 'loading'">
      <template #trigger>
        <NButton quaternary circle size="tiny" class="refresh" @click="emit('refresh')">
          <template #icon><NIcon><RefreshOutline /></NIcon></template>
        </NButton>
      </template>
      重新检测
    </NTooltip>
    <NSpin v-else-if="state.info?.ip" :size="12" class="refreshing" />
  </div>
</template>

<style scoped>
.egress {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.body {
  flex: 1;
  min-width: 0;
}
.pending {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.ip-line {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}
.flag {
  font-size: 16px;
  line-height: 1;
}
.ip {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-weight: 600;
  font-size: 13px;
}
.detail {
  margin-top: 2px;
  font-size: 12px;
  opacity: 0.6;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 260px;
}
.stale {
  opacity: 0.5;
}
.refresh {
  flex: none;
  opacity: 0;
  transition: opacity 0.15s;
}
.refreshing {
  flex: none;
}
.egress:hover .refresh,
.refresh:focus-visible {
  opacity: 1;
}
/* 触屏没有悬停，刷新按钮常显。 */
@media (hover: none) {
  .refresh {
    opacity: 1;
  }
}
.pre {
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
