<script setup lang="ts">
import { NCard, NCollapse, NCollapseItem, NTag, NTooltip } from 'naive-ui'
import type { SectionEffect } from '../composables/useSettingsSections'

/** effect 标明分组的生效方式，显示在标题右侧，悬停查看说明。 */
const props = defineProps<{ name: string; title: string; expandedNames: string[]; effect?: SectionEffect; advanced?: boolean }>()
const emit = defineEmits<{ 'update:expandedNames': [value: string[]] }>()
function update(names: Array<string | number>) {
  const other = props.expandedNames.filter((name) => name !== props.name)
  emit('update:expandedNames', names.includes(props.name) ? [...other, props.name] : other)
}
</script>

<template>
  <NCard :id="`settings-${name}`" size="small" class="settings-section" :class="{ 'is-expanded': expandedNames.includes(name) }" content-style="padding: 0">
    <NCollapse class="settings-collapse" :expanded-names="expandedNames.includes(name) ? [name] : []" @update:expanded-names="update">
      <NCollapseItem :title="title" :name="name" display-directive="show">
        <template #header-extra>
          <span class="section-tags" @click.stop>
            <NTag v-if="advanced" size="small" :bordered="false">高级</NTag>
            <NTooltip v-if="effect" placement="top-end" style="max-width: min(360px, calc(100vw - 32px))">
              <template #trigger>
                <NTag size="small" :type="effect.type" :bordered="false" tabindex="0">{{ effect.label }}</NTag>
              </template>
              {{ effect.tip }}
            </NTooltip>
          </span>
        </template>
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
.section-tags { display: inline-flex; gap: 6px; cursor: default; }
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
