<script setup lang="ts">
import { ref } from 'vue'
import { NButton, NInputGroup, NInputNumber, NText } from 'naive-ui'
import { api, problemMessage } from '../api/client'

type Pool = 'xray_inbound' | 'xray_api' | 'mihomo_socks' | 'mihomo_http' | 'mihomo_controller'

const props = defineProps<{
  pool: Pool
  stackId?: number
  /** 表单内其他端口，分配时避开。 */
  exclude?: number[]
}>()
const value = defineModel<number | null>('value', { required: true })
const warning = ref('')
const busy = ref(false)

async function allocate() {
  busy.value = true
  const { data, error } = await api.POST('/api/ports/allocate', {
    body: { pool: props.pool, exclude: (props.exclude ?? []).filter((p) => p !== value.value) },
  })
  busy.value = false
  if (data) {
    value.value = data.port
    warning.value = ''
  } else {
    warning.value = problemMessage(error, '分配失败')
  }
}

async function check() {
  warning.value = ''
  if (!value.value) {
    return
  }
  if ((props.exclude ?? []).filter((p) => p === value.value).length > 1) {
    warning.value = '与本 stack 的其他端口重复'
    return
  }
  const { data } = await api.GET('/api/ports/check', {
    params: { query: { port: value.value, stack_id: props.stackId ?? 0 } },
  })
  if (data?.conflict_with) {
    warning.value = `与 ${data.conflict_with} 冲突`
  } else if (data?.in_use) {
    warning.value = '本机已有其他进程占用该端口'
  }
}
</script>

<template>
  <div style="width: 100%">
    <NInputGroup>
      <NInputNumber v-model:value="value" :min="1" :max="65535" :show-button="false" style="flex: 1" @blur="check" />
      <NButton :loading="busy" @click="allocate">自动分配</NButton>
    </NInputGroup>
    <NText v-if="warning" type="warning" depth="2" style="font-size: 12px">{{ warning }}</NText>
  </div>
</template>
