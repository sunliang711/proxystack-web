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
  <NCard size="small" class="settings-section" content-style="padding: 0">
    <NCollapse :expanded-names="expandedNames.includes(name) ? [name] : []" @update:expanded-names="update">
      <NCollapseItem :title="title" :name="name" display-directive="show">
        <slot />
      </NCollapseItem>
    </NCollapse>
  </NCard>
</template>

<style scoped>
.settings-section {
  margin-bottom: 12px;
}
.settings-section :deep(.n-collapse-item__header) {
  padding: 12px 16px;
}
.settings-section :deep(.n-collapse-item__content-inner) {
  padding: 0 16px 4px;
}
</style>
