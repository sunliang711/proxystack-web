<script setup lang="ts">
import { computed } from 'vue'
import { NTag, NTooltip } from 'naive-ui'
import { sectionEffects, type SettingsSectionName } from '../composables/useSettingsSections'

/** 系统设置标签页内的一个分组：标题、生效方式标签（按分组名查找）和字段。 */
const props = defineProps<{ name: SettingsSectionName; title: string }>()
const effect = computed(() => sectionEffects[props.name])
</script>

<template>
  <section :id="`settings-${name}`" class="settings-section">
    <h3 class="section-title">
      <span>{{ title }}</span>
      <NTooltip v-if="effect" placement="top-start" style="max-width: min(360px, calc(100vw - 32px))">
        <template #trigger>
          <NTag size="small" :type="effect.type" :bordered="false" tabindex="0">{{ effect.label }}</NTag>
        </template>
        {{ effect.tip }}
      </NTooltip>
    </h3>
    <slot />
  </section>
</template>

<style scoped>
.settings-section + .settings-section {
  margin-top: 8px;
  padding-top: 20px;
  border-top: 1px solid var(--ps-border);
}
.section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 16px;
  font-size: 15px;
  font-weight: 600;
}
</style>
