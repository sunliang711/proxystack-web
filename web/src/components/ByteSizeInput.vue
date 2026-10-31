<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NInputGroup, NInputNumber, NSelect } from 'naive-ui'

const MB = 1024 ** 2
const GB = 1024 ** 3
const TB = 1024 ** 4
const props = defineProps<{ small?: boolean }>()
const value = defineModel<number>('value', { required: true })
/** small 用于每日阈值这类较小的量：单位是 MB / GB。 */
const units = computed(() => (props.small ? [{ label: 'MB', value: MB }, { label: 'GB', value: GB }] : [{ label: 'GB', value: GB }, { label: 'TB', value: TB }]))
const fit = (bytes: number) => {
  const [low, high] = units.value
  return bytes && bytes % high!.value === 0 ? high!.value : low!.value
}
const unit = ref(fit(value.value))

watch(value, (bytes) => {
  if (bytes) unit.value = fit(bytes)
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
    <NSelect v-model:value="unit" :options="units" style="width: 80px" />
  </NInputGroup>
</template>
