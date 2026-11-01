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
const amount = ref(value.value ? value.value / unit.value : 0)

watch(value, (bytes) => {
  // 自己发出的字节数回传时保留输入数字和手动选择的单位。
  if (bytes === Math.round(amount.value * unit.value)) return
  if (bytes) unit.value = fit(bytes)
  amount.value = bytes ? bytes / unit.value : 0
})

function updateAmount(next: number | null) {
  amount.value = next ?? 0
  value.value = Math.round(amount.value * unit.value)
}

function updateUnit(next: number) {
  unit.value = next
  value.value = Math.round(amount.value * unit.value)
}
</script>

<template>
  <NInputGroup>
    <NInputNumber :value="amount" @update:value="updateAmount" :min="0" :precision="2" style="flex: 1" placeholder="0 表示不限" />
    <NSelect :value="unit" @update:value="updateUnit" :options="units" style="width: 80px" />
  </NInputGroup>
</template>
