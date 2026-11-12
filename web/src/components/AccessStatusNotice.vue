<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NSpace, NText } from 'naive-ui'
import { RouterLink } from 'vue-router'
import { api, problemMessage, type Schemas } from '../api/client'
import { formatTime } from '../util/format'
import CopyableCode from './CopyableCode.vue'

/** 访问域名统计的开关、读取状态，以及会导致统计缺失或偏少的 stack。 */
const emit = defineEmits<{ loaded: [Schemas['AccessStatus']] }>()
const status = ref<Schemas['AccessStatus'] | null>(null)
const failure = ref('')

onMounted(async () => {
  const { data, error } = await api.GET('/api/access/status')
  if (!data) {
    failure.value = problemMessage(error, '访问统计状态读取失败')
    return
  }
  status.value = data
  emit('loaded', data)
})

const noLog = computed(() => (status.value?.stacks ?? []).filter((stack) => !stack.access_log).map((stack) => stack.stack))
const limited = computed(() => (status.value?.stacks ?? []).filter((stack) => stack.access_log && stack.rate_limit === 'limited').map((stack) => stack.stack))
const fixCommand = [
  'sudo mkdir -p /etc/systemd/system/psw-xray@.service.d',
  "printf '[Service]\\nLogRateLimitIntervalSec=30s\\nLogRateLimitBurst=1000000\\n' | sudo tee /etc/systemd/system/psw-xray@.service.d/log-rate-limit.conf",
  'sudo systemctl daemon-reload',
  "sudo systemctl restart 'psw-xray@*.service'",
].join('\n')
</script>

<template>
  <NSpace v-if="status || failure" vertical :size="12">
    <NAlert v-if="failure" type="error" :bordered="false">{{ failure }}</NAlert>
    <NAlert v-else-if="status && !status.enabled" type="info" :bordered="false">
      访问域名统计未开启，下面只显示开启期间留下的历史数据。<RouterLink :to="{ name: 'settings', query: { section: 'traffic' } }">去系统设置开启</RouterLink>
    </NAlert>
    <template v-else-if="status">
      <NAlert v-if="status.error" type="warning" :bordered="false" title="日志读取中断">
        {{ status.error }}。面板会自动重试，恢复后从中断处继续统计。
      </NAlert>
      <NAlert v-if="noLog.length" type="warning" :bordered="false">
        以下 Stack 已应用的 xray 配置不输出访问日志，它们的访问不会被统计：{{ noLog.join('、') }}。
        在 Stack 详情或「系统设置 → 默认值」中打开「Xray 记录访问日志」（日志级别不能为 none），应用后生效。
      </NAlert>
      <NAlert v-if="limited.length" type="warning" :bordered="false" title="journald 限速会导致统计偏少">
        <NSpace vertical :size="8">
          <NText>以下 Stack 的 xray 沿用 journald 的默认限速，连接多时 journald 会丢弃部分访问日志：{{ limited.join('、') }}。在服务器上以 root 执行下面的命令放开限速（会重启这些 xray，连接短暂中断）：</NText>
          <CopyableCode :value="fixCommand" language="bash" />
        </NSpace>
      </NAlert>
      <NText depth="3" class="access-note">
        统计读取 xray 访问日志，按用户和主域名计数（同一网站的子域名合并，例如 www.google.com 计入 google.com），计的是连接次数而不是流量；
        HTTPS 只能看到域名，看不到具体网址。日期按 {{ status.timezone }} 计算，保留 {{ status.retention_days }} 天，最近约半分钟的访问尚未计入。
        <template v-if="status.last_at && Date.parse(status.last_at) > 0">最近读到的日志：{{ formatTime(status.last_at) }}。</template>
      </NText>
    </template>
  </NSpace>
</template>

<style scoped>
.access-note { display: block; font-size: 12px; }
</style>
