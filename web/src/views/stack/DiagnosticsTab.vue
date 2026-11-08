<script setup lang="ts">
import { computed, h, onBeforeUnmount, ref, watch } from 'vue'
import {
  NAlert, NButton, NCard, NDataTable, NDescriptions, NDescriptionsItem, NInput, NInputNumber, NProgress, NRadioButton, NRadioGroup, NSpace, NTag, NText,
  useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, panelBase, problemMessage, type Schemas } from '../../api/client'

type Delay = Schemas['DelayResult']
type SpeedProgress = Schemas['SpeedTestProgress']
type SpeedResult = Schemas['SpeedTestResult']

const props = defineProps<{ stackId: number; mihomoEnabled: boolean; httpListener: boolean }>()
const message = useMessage()

const family = ref<'all' | 'ipv4' | 'ipv6'>('all')
const egress = ref<Schemas['EgressResult'][]>([])
const egressError = ref('')
const querying = ref(false)

const failure = (error: Parameters<typeof problemMessage>[0] & { errors?: { message?: string }[] }) => error?.errors?.[0]?.message ?? problemMessage(error)

async function queryEgress() {
  querying.value = true
  egressError.value = ''
  const { data, error } = await api.GET('/api/stacks/{id}/egress', {
    params: { path: { id: props.stackId }, query: { family: family.value, timeout: 8 } },
  })
  querying.value = false
  if (error) egressError.value = failure(error)
  egress.value = data ?? []
}

const delays = ref<Delay[]>([])
const testing = ref(false)
const delayError = ref('')

async function testDelay() {
  testing.value = true
  delayError.value = ''
  const { data, error } = await api.POST('/api/stacks/{id}/delay', { params: { path: { id: props.stackId } }, body: { names: [] } })
  testing.value = false
  if (error) {
    delayError.value = failure(error)
    return
  }
  delays.value = data ?? []
  if (!delays.value.length) message.info('这个 stack 没有可以测延迟的代理')
}

const delayColumns: DataTableColumns<Delay> = [
  { title: '代理', key: 'name' },
  {
    title: '延迟', key: 'delay', width: 140, sorter: (a, b) => (a.delay || 1e9) - (b.delay || 1e9),
    render: (row) => (row.delay ? `${row.delay} ms` : h(NTag, { size: 'small', type: 'error' }, { default: () => '失败' })),
  },
  { title: '错误', key: 'error', ellipsis: { tooltip: true } },
]

const defaultSpeedURL = 'https://speed.cloudflare.com/__down?bytes={bytes}'
const speedStorageKey = 'proxystack.speedtest'

interface SpeedOptions {
  listener: 'socks' | 'http'
  mode: 'duration' | 'size'
  seconds: number
  mb: number
  url: string
}

function loadSpeedOptions(): SpeedOptions {
  const defaults: SpeedOptions = { listener: 'socks', mode: 'duration', seconds: 10, mb: 50, url: '' }
  try {
    return { ...defaults, ...JSON.parse(localStorage.getItem(speedStorageKey) ?? '{}') }
  } catch {
    return defaults
  }
}

const speed = ref<SpeedOptions>(loadSpeedOptions())
watch(speed, (value) => {
  try {
    localStorage.setItem(speedStorageKey, JSON.stringify(value))
  } catch {
    // 浏览器禁用存储时只是不记住选项。
  }
}, { deep: true })
const listener = computed(() => (speed.value.listener === 'http' && !props.httpListener ? 'socks' : speed.value.listener))

const speedRunning = ref(false)
const speedSamples = ref<SpeedProgress[]>([])
const speedResult = ref<SpeedResult | null>(null)
const speedError = ref('')
let speedSource: EventSource | null = null

function stopSpeedSource() {
  speedSource?.close()
  speedSource = null
  speedRunning.value = false
}

function startSpeedTest() {
  stopSpeedSource()
  speedSamples.value = []
  speedResult.value = null
  speedError.value = ''
  speedRunning.value = true
  const params = new URLSearchParams({
    listener: listener.value, mode: speed.value.mode, seconds: String(speed.value.seconds), mb: String(speed.value.mb),
  })
  if (speed.value.url.trim()) params.set('url', speed.value.url.trim())
  const source = new EventSource(`${panelBase}api/stacks/${props.stackId}/speedtest?${params}`)
  speedSource = source
  source.addEventListener('progress', (event) => {
    speedSamples.value = [...speedSamples.value, JSON.parse((event as MessageEvent).data)]
  })
  source.addEventListener('result', (event) => {
    // 收到结果后立即关闭，避免 EventSource 在服务端结束响应后自动重连再测一次。
    stopSpeedSource()
    const result: SpeedResult = JSON.parse((event as MessageEvent).data)
    if (result.error && !result.bytes) speedError.value = result.error
    else speedResult.value = result
  })
  source.onerror = () => {
    if (speedSource !== source) return
    stopSpeedSource()
    speedError.value = '与面板的连接中断，测速已停止'
  }
}

function cancelSpeedTest() {
  stopSpeedSource()
  message.info('已停止测速')
}

onBeforeUnmount(stopSpeedSource)

const lastSample = computed(() => speedSamples.value[speedSamples.value.length - 1])
const speedPercent = computed(() => {
  const sample = lastSample.value
  if (!sample) return 0
  const ratio = speed.value.mode === 'duration' ? sample.elapsed_ms / (speed.value.seconds * 1000) : sample.bytes / (speed.value.mb * 1e6)
  return Math.min(100, Math.round(ratio * 100))
})

const formatMbps = (value: number) => `${value >= 100 ? value.toFixed(0) : value.toFixed(1)} Mbps`
const formatBytes = (value: number) => (value >= 1e9 ? `${(value / 1e9).toFixed(2)} GB` : `${(value / 1e6).toFixed(1)} MB`)
const formatSeconds = (ms: number) => `${(ms / 1000).toFixed(1)} 秒`

const stopReasons: Record<string, string> = {
  duration: '达到设定时长',
  size: '达到设定大小',
  byte_cap: '达到 1 GB 流量上限',
  timeout: '达到时长上限',
  cancelled: '已取消',
  error: '下载出错',
}

/** 速度曲线：按采样顺序等距排列，纵轴以最高速度为顶。 */
const sparkline = computed(() => {
  const points = speedSamples.value.map((sample) => sample.mbps)
  if (points.length < 2) return ''
  const top = Math.max(...points) || 1
  return points.map((value, index) => `${((index / (points.length - 1)) * 100).toFixed(2)},${(38 - (value / top) * 34).toFixed(2)}`).join(' ')
})
</script>

<template>
  <NSpace vertical :size="20">
    <NCard title="出口 IP" size="small">
      <NSpace vertical>
        <NText depth="3">经该 stack 的 mihomo socks 监听查询，mihomo 需要在运行。</NText>
        <NSpace align="center">
          <NRadioGroup v-model:value="family" size="small">
            <NRadioButton value="all">全部</NRadioButton>
            <NRadioButton value="ipv4">IPv4</NRadioButton>
            <NRadioButton value="ipv6">IPv6</NRadioButton>
          </NRadioGroup>
          <NButton size="small" :loading="querying" @click="queryEgress">查询</NButton>
        </NSpace>
        <NAlert v-if="egressError" type="error" :bordered="false">{{ egressError }}</NAlert>
        <div v-for="result in egress" :key="result.family">
          <NSpace align="center">
            <NTag size="small">{{ result.family === 'ipv4' ? 'IPv4' : 'IPv6' }}</NTag>
            <NText v-if="result.ip" strong>{{ result.ip }}</NText>
            <NText v-else type="warning">没有查询到地址</NText>
            <NText depth="3">{{ result.region }}</NText>
          </NSpace>
          <NText v-for="source in result.sources.filter((item) => item.error)" :key="source.url" depth="3" style="display: block; font-size: 12px">
            {{ source.url }}：{{ source.error }}
          </NText>
        </div>
      </NSpace>
    </NCard>
    <NCard title="上游延迟" size="small">
      <NSpace vertical>
        <NSpace align="center">
          <NText depth="3">调用 mihomo controller 测试全部代理和代理组访问测试地址的延迟。</NText>
          <NButton size="small" :loading="testing" @click="testDelay">测延迟</NButton>
        </NSpace>
        <NAlert v-if="delayError" type="error" :bordered="false">{{ delayError }}</NAlert>
        <NDataTable v-if="delays.length" :columns="delayColumns" :data="delays" :row-key="(row: Delay) => row.name" size="small" />
      </NSpace>
    </NCard>
    <NCard title="下载测速" size="small">
      <NSpace vertical>
        <NText depth="3">
          经该 stack 已应用配置中的 mihomo 监听下载测试文件，测的是本服务器经这个 stack 出口的下载带宽，不含用户到服务器这一段。
          流量按规则和代理组当前的选择出站，结果中列出实际走的链路。测速会消耗上游流量，auto stack 的测速流量计入 edge stack 的 relay 入站。同一时间只能进行一个测速。
        </NText>
        <NAlert v-if="!mihomoEnabled" type="info" :bordered="false">这个 stack 没有启用 mihomo，无法测速。</NAlert>
        <div v-else class="speed-section">
          <NSpace align="center" :size="[16, 8]">
            <NRadioGroup v-model:value="speed.listener" size="small" :disabled="speedRunning">
              <NRadioButton value="socks">SOCKS 监听</NRadioButton>
              <NRadioButton value="http" :disabled="!httpListener">HTTP 监听</NRadioButton>
            </NRadioGroup>
            <NRadioGroup v-model:value="speed.mode" size="small" :disabled="speedRunning">
              <NRadioButton value="duration">按时长</NRadioButton>
              <NRadioButton value="size">按大小</NRadioButton>
            </NRadioGroup>
            <NInputNumber v-if="speed.mode === 'duration'" v-model:value="speed.seconds" size="small" :min="5" :max="60" :disabled="speedRunning" style="width: 120px">
              <template #suffix>秒</template>
            </NInputNumber>
            <NInputNumber v-else v-model:value="speed.mb" size="small" :min="10" :max="1000" :step="10" :disabled="speedRunning" style="width: 130px">
              <template #suffix>MB</template>
            </NInputNumber>
            <NButton v-if="!speedRunning" size="small" type="primary" :disabled="!speed.seconds || !speed.mb" @click="startSpeedTest">开始测速</NButton>
            <NButton v-else size="small" @click="cancelSpeedTest">停止</NButton>
          </NSpace>
          <NInput v-model:value="speed.url" size="small" clearable :disabled="speedRunning" :placeholder="`测速地址，默认 ${defaultSpeedURL}`" />
          <NText depth="3" style="font-size: 12px">
            地址中的 {bytes} 替换为单次请求的字节数，不带 {bytes} 的地址重复下载直到满足停止条件。
            按时长测速最多消耗 1 GB 流量；按大小测速最长 60 秒，到时按已下载的数据计算。
          </NText>
          <div v-if="speedRunning || (speedSamples.length && !speedResult)" class="speed-live">
            <NProgress type="line" :percentage="speedPercent" :show-indicator="false" :processing="speedRunning" />
            <NText v-if="lastSample">
              当前 <strong>{{ formatMbps(lastSample.mbps) }}</strong> · 已下载 {{ formatBytes(lastSample.bytes) }} · {{ formatSeconds(lastSample.elapsed_ms) }}
            </NText>
            <NText v-else-if="speedRunning" depth="3">正在连接…</NText>
          </div>
          <svg v-if="sparkline" class="speed-chart" viewBox="0 0 100 40" preserveAspectRatio="none" aria-label="速度曲线">
            <polyline :points="sparkline" fill="none" vector-effect="non-scaling-stroke" />
          </svg>
          <NAlert v-if="speedError" type="error" :bordered="false">{{ speedError }}</NAlert>
          <div v-if="speedResult" class="speed-section">
            <NAlert v-if="speedResult.error" type="warning" :bordered="false">下载中途出错，以下按已下载的数据计算：{{ speedResult.error }}</NAlert>
            <NDescriptions bordered size="small" label-placement="left" :column="3" class="speed-result">
              <NDescriptionsItem label="稳定速度"><strong>{{ formatMbps(speedResult.stable_mbps) }}</strong></NDescriptionsItem>
              <NDescriptionsItem label="平均速度">{{ formatMbps(speedResult.avg_mbps) }}</NDescriptionsItem>
              <NDescriptionsItem label="峰值速度">{{ formatMbps(speedResult.peak_mbps) }}</NDescriptionsItem>
              <NDescriptionsItem label="下载量">{{ formatBytes(speedResult.bytes) }}</NDescriptionsItem>
              <NDescriptionsItem label="用时">{{ formatSeconds(speedResult.elapsed_ms) }}</NDescriptionsItem>
              <NDescriptionsItem label="首字节">{{ speedResult.first_byte_ms }} ms</NDescriptionsItem>
              <NDescriptionsItem label="实际链路" :span="2">
                <template v-if="speedResult.chain.length">{{ speedResult.chain.join(' → ') }}</template>
                <NText v-else depth="3">未查询到</NText>
              </NDescriptionsItem>
              <NDescriptionsItem label="结束原因">{{ stopReasons[speedResult.stop_reason] ?? speedResult.stop_reason }}</NDescriptionsItem>
              <NDescriptionsItem v-if="speedResult.rule" label="命中规则" :span="3">
                {{ speedResult.rule }}<template v-if="speedResult.rule_payload">（{{ speedResult.rule_payload }}）</template>
              </NDescriptionsItem>
            </NDescriptions>
            <NText depth="3" style="font-size: 12px">稳定速度排除首字节后的第一秒（连接建立初期速度尚未爬升），平均速度按全部下载时间计算，峰值速度取一秒窗口内的最高值。</NText>
          </div>
        </div>
      </NSpace>
    </NCard>
  </NSpace>
</template>

<style scoped>
.speed-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.speed-live {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.speed-chart {
  width: 100%;
  height: 56px;
  border: 1px solid var(--ps-border);
  border-radius: 8px;
  background: var(--ps-bg);
}
.speed-chart polyline {
  stroke: var(--ps-accent);
  stroke-width: 2;
}
.speed-result :deep(.n-descriptions-table-content) {
  word-break: break-all;
}
</style>
