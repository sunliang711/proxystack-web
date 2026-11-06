<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NInputGroup, NInputNumber, NSelect } from 'naive-ui'

const MB = 1024 ** 2
const GB = 1024 ** 3
const TB = 1024 ** 4
const props = defineProps<{ small?: boolean }>()
const value = defineModel<number>('value', { required: true })
/** small 用于每日阈值这类较小的量：单位是 MB / GB；流量额度可选 MB / GB / TB。 */
const units = computed(() => [
  { label: 'MB', value: MB },
  { label: 'GB', value: GB },
  ...(props.small ? [] : [{ label: 'TB', value: TB }]),
])
/** 输入框保留两位小数。 */
const round2 = (n: number) => Math.round(n * 100) / 100
/**
 * 显示用的单位：从大到小找第一个能用两位小数准确还原字节数的单位，
 * 例如保存时输入 0.1 GB，再次打开仍显示 0.1 GB，而不是 0.0999999996 GB 或 102.4 MB。
 * 都无法准确还原时（例如旧数据），用数值不小于 1 的最大单位。
 */
const fit = (bytes: number) => {
  const largestFirst = [...units.value].reverse()
  const exact = largestFirst.find((u) => {
    const shown = round2(bytes / u.value)
    return shown > 0 && Math.round(shown * u.value) === bytes
  })
  return (exact ?? largestFirst.find((u) => bytes >= u.value) ?? units.value[0]!).value
}
/** 未设置时的默认单位为 GB，流量额度和每日阈值相同。 */
const unit = ref(value.value ? fit(value.value) : GB)
const amount = ref(value.value ? round2(value.value / unit.value) : 0)

watch(value, (bytes) => {
  // 自己发出的字节数回传时保留输入数字和手动选择的单位。
  if (bytes === Math.round(amount.value * unit.value)) return
  if (bytes) unit.value = fit(bytes)
  amount.value = bytes ? round2(bytes / unit.value) : 0
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
    <!-- 0 表示不限：输入框留空并显示「不限」，避免出现 0.00 这种看不出含义的数字。 -->
    <NInputNumber :value="amount || null" @update:value="updateAmount" :min="0" :precision="2" style="flex: 1" placeholder="不限" />
    <NSelect :value="unit" @update:value="updateUnit" :options="units" style="width: 80px" />
  </NInputGroup>
</template>
