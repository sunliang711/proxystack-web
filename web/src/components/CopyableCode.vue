<script setup lang="ts">
import { CopyOutline } from '@vicons/ionicons5'
import { NButton, NCode, NIcon, NSpace, NTooltip, useMessage } from 'naive-ui'

const props = defineProps<{
  value: string
  label?: string
}>()

const message = useMessage()

async function copy() {
  try {
    await navigator.clipboard.writeText(props.value)
    message.success('已复制')
  } catch {
    message.error('复制失败：浏览器不允许访问剪贴板')
  }
}
</script>

<template>
  <NSpace vertical :size="6">
    <div v-if="label" class="copyable-label">{{ label }}</div>
    <div class="copyable-code">
      <NCode :code="value" word-wrap />
      <NTooltip>
        <template #trigger>
          <NButton size="small" secondary circle :disabled="!value" aria-label="复制" @click="copy">
            <template #icon><NIcon><CopyOutline /></NIcon></template>
          </NButton>
        </template>
        复制
      </NTooltip>
    </div>
  </NSpace>
</template>

<style scoped>
.copyable-label {
  font-weight: 600;
}

.copyable-code {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--n-border-color);
  border-radius: 4px;
}

.copyable-code :deep(pre) {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

@media (max-width: 640px) {
  .copyable-code {
    grid-template-columns: minmax(0, 1fr);
  }

  .copyable-code .n-button {
    justify-self: end;
  }
}
</style>
