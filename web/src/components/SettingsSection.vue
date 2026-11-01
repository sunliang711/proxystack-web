<script setup lang="ts">
import { NCard, NCollapse, NCollapseItem } from 'naive-ui'

const props = defineProps<{ name: string; title: string; expandedNames: string[] }>()
const emit = defineEmits<{ 'update:expandedNames': [value: string[]] }>()
function update(names: Array<string | number>) {
  const other = props.expandedNames.filter((name) => name !== props.name)
  emit('update:expandedNames', names.includes(props.name) ? [...other, props.name] : other)
}
</script>

<template>
  <NCard size="small" class="settings-section" :class="{ 'is-expanded': expandedNames.includes(name) }" content-style="padding: 0">
    <NCollapse class="settings-collapse" :expanded-names="expandedNames.includes(name) ? [name] : []" @update:expanded-names="update">
      <NCollapseItem :title="title" :name="name" display-directive="show">
        <slot />
      </NCollapseItem>
    </NCollapse>
  </NCard>
</template>

<style scoped>
.settings-section {
  margin-bottom: 14px;
  transition: border-color 0.2s;
}
.settings-section.is-expanded { border-color: var(--ps-accent); }
.settings-section :deep(.settings-collapse > .n-collapse-item > .n-collapse-item__header:hover) { background: var(--ps-accent-soft); }
.settings-section :deep(.settings-collapse > .n-collapse-item > .n-collapse-item__header) {
  padding: 18px 20px;
  font-size: 15px;
  font-weight: 600;
}
.settings-section :deep(.n-collapse-item__content-inner) {
  padding: 12px 20px 4px;
}
</style>
