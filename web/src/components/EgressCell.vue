<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NIcon, NSpin, NTag, NText, NTooltip } from 'naive-ui'
import { RefreshOutline } from '@vicons/ionicons5'
import type { Schemas } from '../api/client'
import type { EgressState } from '../composables/useEgress'
import { healthView, shortLocation, shortOrg } from '../util/egress-view'
import { formatTime } from '../util/format'

/**
 * Stack 列表的出口 IP：浏览器查询的出口地址，加上后台定时检测的健康圆点。
 * 正常时只占两行（地址、地区与运营商），IPv6 有地址才多一行，异常时圆点旁加短文字。
 */
const props = defineProps<{
  state?: EgressState
  mihomoEnabled: boolean
  health?: Schemas['EgressHealth']
  /** 后台检测状态接口读取失败的原因。 */
  healthError?: string
}>()
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

const detail = computed(() => {
  const info = primary.value
  if (!info?.ip) return ''
  return [shortLocation(info.city, info.region, info.country), info.org ? shortOrg(info.org) : ''].filter(Boolean).join(' · ')
})
const families = computed(() => [
  { key: 'v4', label: 'IPv4', result: props.state?.v4, loading: props.state?.loading?.v4, error: props.state?.errors?.v4 },
  { key: 'v6', label: 'IPv6', result: props.state?.v6, loading: props.state?.loading?.v6, error: props.state?.errors?.v6 },
])
/** 第一行显示 IPv4；没有 IPv4 时显示 IPv6，第二行不再重复。 */
const main = computed(() => (props.state?.v4?.ip || props.state?.loading?.v4 || !props.state?.v6?.ip ? families.value[0] : families.value[1]))
const extraV6 = computed(() => (main.value.key === 'v4' && props.state?.v6?.ip ? families.value[1] : null))
const hasResult = computed(() => !!primary.value?.ip)

const healthInfo = computed(() => healthView(props.health?.status, !!props.healthError))
const checked = computed(() => (props.health?.checked_at && !props.health.checked_at.startsWith('0001-') ? formatTime(props.health.checked_at) : ''))
/** 后台检测和浏览器查询得到的 IPv4 不一致，通常是出口刚切换或浏览器缓存过时。 */
const mismatch = computed(() => {
  const backend = props.health?.ipv4
  const browser = props.state?.v4?.ip
  return backend && browser && backend !== browser && !props.state?.loading?.v4 ? backend : ''
})

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
            <div class="ip-line" :class="{ stale: main.loading && main.result?.ip }">
              <span class="dot" :class="`dot-${healthInfo.tone}`" :aria-label="healthInfo.text" role="img" />
              <span class="flag">{{ flag || '🌐' }}</span>
              <span v-if="main.result?.ip" class="ip" :class="{ v6: main.key === 'v6' }">{{ main.result.ip }}</span>
              <NText v-else-if="main.loading" depth="3">检测中…</NText>
              <NText v-else :type="main.error ? 'error' : 'warning'">{{ main.error ? '查询失败' : '没有查到出口' }}</NText>
              <NSpin v-if="main.loading" :size="10" />
              <NText v-if="healthInfo.badge" :type="healthInfo.tone === 'error' ? 'error' : 'warning'" class="badge">{{ healthInfo.badge }}</NText>
            </div>
            <div v-if="extraV6" class="sub-line" :class="{ stale: extraV6.loading }">
              <span class="family">v6</span><span class="ip v6">{{ extraV6.result?.ip }}</span>
            </div>
            <div v-if="detail" class="sub-line detail">{{ detail }}</div>
            <NText v-if="mismatch" type="warning" class="sub-line mismatch">后台查到 {{ mismatch }}</NText>
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
        <div v-if="state.at" class="tip-block">浏览器查询于 {{ formatTime(state.at) }}，结果缓存在当前浏览器。</div>
        <div class="tip-block">
          <div class="tip-title">后台检测：{{ healthInfo.text }}</div>
          <div v-if="healthError">{{ healthError }}</div>
          <div v-if="mismatch">后台查到的 IPv4 是 {{ mismatch }}，与浏览器查询结果不同，通常是出口刚切换或浏览器缓存过时，可点刷新按钮重新检测。</div>
          <template v-if="health">
            <div v-if="checked">最后完成：{{ checked }}</div>
            <div>后台 IPv4：{{ health.ipv4 || '-' }}　后台 IPv6：{{ health.ipv6 || '-' }}</div>
            <div v-if="health.failures">连续失败：{{ health.failures }} 次</div>
            <div v-if="health.ipv4_error" class="pre">IPv4：{{ health.ipv4_error }}</div>
            <div v-if="health.ipv6_error" class="pre">IPv6：{{ health.ipv6_error }}</div>
          </template>
          <div class="tip-note">圆点颜色来自服务端定时检测，与浏览器查询相互独立；手动刷新不计入失败次数。</div>
        </div>
      </NTooltip>
    </div>
    <NSpin v-if="state.status === 'loading' && hasResult" :size="14" class="side" />
    <NTooltip v-else-if="state.status !== 'loading'">
      <template #trigger>
        <NButton quaternary circle size="tiny" class="side" aria-label="重新检测出口 IP" @click="emit('refresh')">
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
.ip-line {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
  line-height: 20px;
}
.dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--ps-muted);
  opacity: 0.6;
}
.dot-success { background: #18a058; opacity: 1; }
.dot-warning { background: #f0a020; opacity: 1; }
.dot-error { background: var(--ps-danger); opacity: 1; }
.badge {
  flex: none;
  font-size: 12px;
  font-weight: 600;
}
.flag {
  flex: none;
  font-size: 15px;
  line-height: 1;
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
/* 第二行起与 IP 对齐：圆点 8px + 间距 6px + 国旗约 18px + 间距 6px。 */
.sub-line {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-left: 38px;
  font-size: 12px;
  line-height: 18px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.detail {
  display: block;
  opacity: 0.6;
}
.mismatch {
  display: block;
}
.family {
  flex: none;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.3px;
  opacity: 0.45;
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
.tip-note {
  margin-top: 4px;
  opacity: 0.75;
}
.pre {
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
