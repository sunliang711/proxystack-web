<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NInputGroup, NInputNumber, NSelect } from 'naive-ui'

const GB = 1024 ** 3
const TB = 1024 ** 4
const value = defineModel<number>('value', { required: true })
const unit = ref(value.value && value.value % TB === 0 ? TB : GB)

watch(value, (bytes) => {
  if (bytes && bytes % TB === 0) unit.value = TB
})

const amount = computed({
  get: () => (value.value ? value.value / unit.value : 0),
  set: (next: number | null) => {
    value.value = Math.round((next ?? 0) * unit.value)
  },
})
</script>

<template>
  <NInputGroup>
    <NInputNumber v-model:value="amount" :min="0" :precision="2" style="flex: 1" placeholder="0 表示不限" />
    <NSelect v-model:value="unit" :options="[{ label: 'GB', value: GB }, { label: 'TB', value: TB }]" style="width: 80px" />
  </NInputGroup>
</template>
