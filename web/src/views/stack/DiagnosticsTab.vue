<script setup lang="ts">
import { h, ref } from 'vue'
import { NAlert, NButton, NCard, NDataTable, NRadioButton, NRadioGroup, NSpace, NTag, NText, useMessage, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../../api/client'

type Delay = Schemas['DelayResult']

const props = defineProps<{ stackId: number }>()
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
  if (!delays.value.length) message.info('这个 stack 没有可以测速的代理')
}

const delayColumns: DataTableColumns<Delay> = [
  { title: '代理', key: 'name' },
  {
    title: '延迟', key: 'delay', width: 140, sorter: (a, b) => (a.delay || 1e9) - (b.delay || 1e9),
    render: (row) => (row.delay ? `${row.delay} ms` : h(NTag, { size: 'small', type: 'error' }, { default: () => '失败' })),
  },
  { title: '错误', key: 'error', ellipsis: { tooltip: true } },
]
</script>

<template>
  <NSpace vertical :size="12">
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
          <NText depth="3">调用 mihomo controller 测试全部代理和代理组。</NText>
          <NButton size="small" :loading="testing" @click="testDelay">测速</NButton>
        </NSpace>
        <NAlert v-if="delayError" type="error" :bordered="false">{{ delayError }}</NAlert>
        <NDataTable v-if="delays.length" :columns="delayColumns" :data="delays" :row-key="(row: Delay) => row.name" size="small" />
      </NSpace>
    </NCard>
  </NSpace>
</template>
