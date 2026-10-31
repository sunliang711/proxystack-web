<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NIcon, NSpin, NTag, NText, NTooltip } from 'naive-ui'
import { RefreshOutline } from '@vicons/ionicons5'
import type { EgressState } from '../composables/useEgress'
import { formatTime } from '../util/format'

const props = defineProps<{ state?: EgressState; mihomoEnabled: boolean }>()
const emit = defineEmits<{ refresh: [] }>()

/** 地区和运营商优先使用本轮已完成的结果，避免旧 IPv4 信息盖过刚返回的 IPv6。 */
const primary = computed(() => {
  const state = props.state
  if (state?.v4?.ip && !state.loading?.v4) return state.v4
  if (state?.v6?.ip && !state.loading?.v6) return state.v6
  return state?.v4?.ip ? state.v4 : state?.v6
})

/** 两位国家代码转成旗帜 emoji。 */
const flag = computed(() => {
  const code = primary.value?.country
  if (!code) return ''
  return String.fromCodePoint(...[...code].map((char) => 0x1f1e6 + char.charCodeAt(0) - 65))
})

/** 优先显示城市；数据源不给城市时用地区，去掉开头的国家代码。 */
const location = computed(() => {
  const info = primary.value
  if (!info) return ''
  if (info.city) return info.city
  const parts = info.region.split(/\s+/).filter(Boolean)
  if (parts[0] === info.country) parts.shift()
  return parts.join(' ')
})

const detail = computed(() => [location.value, primary.value?.org].filter(Boolean).join(' · '))
const families = computed(() => [
  { key: 'v4', label: 'IPv4', result: props.state?.v4, loading: props.state?.loading?.v4, error: props.state?.errors?.v4 },
  { key: 'v6', label: 'IPv6', result: props.state?.v6, loading: props.state?.loading?.v6, error: props.state?.errors?.v6 },
])
// 只有仍在查询的地址使用旧值并变淡；已返回的另一项正常显示。
const hasResult = computed(() => !!primary.value?.ip)

function sourceOf(result: (typeof families.value)[number]['result']): string {
  return result?.sources.find((source) => source.ip)?.url ?? ''
}
function errorsOf(result: (typeof families.value)[number]['result']): string {
  return result?.sources.map((source) => source.error).filter(Boolean).join('\n') ?? ''
}
</script>

<template>
  <NText v-if="!mihomoEnabled" depth="3">未启用 Mihomo</NText>
  <NText v-else-if="!state" depth="3">-</NText>
  <div v-else class="egress">
    <div class="body">
      <NTag v-if="state.status === 'stopped'" size="small" :bordered="false">mihomo 未运行</NTag>
      <NTooltip v-else placement="top-start" style="max-width: 460px">
        <template #trigger>
          <div>
            <div v-for="(item, index) in families" :key="item.key" class="ip-line" :class="{ stale: item.loading && item.result?.ip }">
              <span class="flag">{{ index === 0 ? flag || '🌐' : '' }}</span>
              <span class="family">{{ item.key }}</span>
              <span v-if="item.result?.ip" class="ip" :class="{ v6: item.key === 'v6' }">{{ item.result.ip }}</span>
              <span v-else-if="item.loading" class="pending"><NText depth="3">检测中…</NText></span>
              <NText v-else :type="item.error ? 'error' : 'warning'" class="family-result">{{ item.error ? '查询失败' : '-' }}</NText>
              <NSpin v-if="item.loading" :size="10" />
            </div>
            <div v-if="detail" class="detail">{{ detail }}</div>
          </div>
        </template>
        <div v-for="item in families" :key="item.label" class="tip-block">
          <div class="tip-title">{{ item.label }}：{{ item.result?.ip || (item.loading ? '检测中…' : item.error ? '查询失败' : '-') }}</div>
          <div v-if="item.loading && item.result?.ip">正在重新检测，当前显示上次结果</div>
          <template v-if="item.result?.ip">
            <div v-if="item.result.region">地区：{{ item.result.region }}</div>
            <div v-if="item.result.org">运营商：{{ item.result.org }}</div>
            <div v-if="sourceOf(item.result)">数据源：{{ sourceOf(item.result) }}</div>
          </template>
          <div v-else-if="item.error || errorsOf(item.result)" class="pre">{{ item.error || errorsOf(item.result) }}</div>
        </div>
        <div v-if="state.at">最近返回：{{ formatTime(state.at) }}</div>
      </NTooltip>
    </div>
    <NSpin v-if="state.status === 'loading' && hasResult" :size="14" class="side" />
    <NTooltip v-else-if="state.status !== 'loading'">
      <template #trigger>
        <NButton quaternary circle size="tiny" class="side" @click="emit('refresh')">
          <template #icon><NIcon><RefreshOutline /></NIcon></template>
        </NButton>
      </template>
      重新检测
    </NTooltip>
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
  line-height: 20px;
}
.flag {
  flex: none;
  width: 18px;
  font-size: 15px;
  line-height: 1;
}
.family {
  flex: none;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.3px;
  opacity: 0.45;
}
.ip {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ip.v6 {
  font-size: 12px;
  font-weight: 500;
}
.ip.none {
  font-weight: 400;
  opacity: 0.4;
}
.detail {
  margin: 2px 0 0 24px;
  font-size: 12px;
  opacity: 0.6;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.stale {
  opacity: 0.5;
}
.side {
  flex: none;
}
.tip-block {
  margin-bottom: 6px;
}
.tip-title {
  font-weight: 600;
}
.pre {
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
