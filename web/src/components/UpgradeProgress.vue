<script setup lang="ts">
import { computed } from 'vue'
import { NProgress, NStep, NSteps, NText } from 'naive-ui'
import type { Schemas } from '../api/client'
import type { UpgradeStage } from '../util/self-update'

const props = defineProps<{
  stage: UpgradeStage
  failed?: boolean
  progress?: Schemas['TaskProgress'] | null
  /** 下载速度，字节每秒。 */
  speed?: number
  target?: string
}>()

const steps: { key: Exclude<UpgradeStage, 'done'>; title: string; detail: string }[] = [
  { key: 'resolve', title: '查找版本', detail: '经下载代理查询发布信息' },
  { key: 'download', title: '下载', detail: '下载对应平台的程序文件' },
  { key: 'verify', title: '校验', detail: '核对发布附带的 sha256' },
  { key: 'install', title: '替换程序', detail: '试运行新程序后原子替换，保留上一版本' },
  { key: 'restart', title: '重启管理服务', detail: '页面会短暂断开，代理流量不受影响' },
  { key: 'confirm', title: '确认新版本', detail: '等待重启后的服务报告新版本' },
]
const current = computed(() => (props.stage === 'done' ? steps.length + 1 : steps.findIndex((step) => step.key === props.stage) + 1))

const mib = (bytes: number) => `${(bytes / (1 << 20)).toFixed(1)} MiB`
const download = computed(() => {
  const progress = props.progress
  if (props.stage !== 'download' || !progress || progress.stage !== 'download') return null
  const done = progress.done ?? 0
  const total = progress.total ?? 0
  const percent = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : null
  const parts = [total > 0 ? `${mib(done)} / ${mib(total)}` : `已下载 ${mib(done)}`]
  if (props.speed && props.speed > 0 && !props.failed) {
    parts.push(`${mib(props.speed)}/s`)
    if (total > done) parts.push(`剩余约 ${Math.max(1, Math.ceil((total - done) / props.speed))} 秒`)
  }
  return { percent, text: parts.join(' · ') }
})
</script>

<template>
  <NSteps vertical size="small" :current="current" :status="failed ? 'error' : 'process'" class="upgrade-steps">
    <NStep v-for="step in steps" :key="step.key" :title="step.title">
      <NText depth="3" class="step-detail">{{ step.key === 'resolve' && target ? `目标版本 ${target}` : step.detail }}</NText>
      <div v-if="step.key === 'download' && download" class="download">
        <NProgress
          type="line"
          :percentage="download.percent ?? 100"
          :processing="download.percent === null || !failed"
          :show-indicator="download.percent !== null"
          :status="failed ? 'error' : 'default'"
          :height="8"
        />
        <NText depth="3" class="download-text">{{ download.text }}</NText>
      </div>
    </NStep>
  </NSteps>
</template>

<style scoped>
.upgrade-steps { text-align: left; }
.step-detail { font-size: 12px; }
.download { margin-top: 6px; }
.download-text { display: block; margin-top: 4px; font-size: 12px; font-variant-numeric: tabular-nums; }
</style>
