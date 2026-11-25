<script setup lang="ts">
import { computed, ref } from 'vue'
import { NButton, NEmpty, NSelect, NSpace, NTag } from 'naive-ui'

const props = defineProps<{ options: string[] }>()
const value = defineModel<string[]>('value', { required: true })
const pending = ref<string | null>(null)

const available = computed(() =>
  props.options.filter((option) => !value.value.includes(option)).map((option) => ({ label: option, value: option })),
)

function add(option: string | null) {
  if (option && !value.value.includes(option)) {
    value.value = [...value.value, option]
  }
  pending.value = null
}

function moveTo(index: number, target: number) {
  if (index < 0 || index >= value.value.length) return
  const clamped = Math.max(0, Math.min(value.value.length - 1, target))
  if (clamped === index) return
  const next = [...value.value]
  const [item] = next.splice(index, 1)
  next.splice(clamped, 0, item)
  value.value = next
}

function move(index: number, delta: number) {
  moveTo(index, index + delta)
}

function moveTop(index: number) {
  moveTo(index, 0)
}

function moveBottom(index: number) {
  moveTo(index, value.value.length - 1)
}

function remove(index: number) {
  value.value = value.value.filter((_, i) => i !== index)
}
</script>

<template>
  <div style="width: 100%">
    <NSelect
      :value="pending"
      :options="available"
      filterable
      placeholder="选择要加入的成员"
      @update:value="add"
    />
    <div class="list">
      <NEmpty v-if="!value.length" size="small" description="尚未选择" />
      <div v-for="(item, index) in value" :key="item" class="row">
        <NTag size="small" :bordered="false">{{ index + 1 }}</NTag>
        <span class="name" :title="item">{{ item }}</span>
        <NSpace :size="4" :wrap="false" class="row-actions">
          <NButton size="tiny" quaternary :disabled="index === 0" @click="moveTop(index)">置顶</NButton>
          <NButton size="tiny" quaternary :disabled="index === 0" @click="move(index, -1)">上移</NButton>
          <NButton size="tiny" quaternary :disabled="index === value.length - 1" @click="move(index, 1)">下移</NButton>
          <NButton size="tiny" quaternary :disabled="index === value.length - 1" @click="moveBottom(index)">置底</NButton>
          <NButton size="tiny" quaternary type="error" @click="remove(index)">移除</NButton>
        </NSpace>
      </div>
    </div>
  </div>
</template>

<style scoped>
.list {
  margin-top: 8px;
}
.row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 2px 0;
}
.name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, monospace;
}
.row-actions {
  flex-shrink: 0;
}
</style>
