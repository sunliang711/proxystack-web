<script setup lang="ts">
import { NAlert } from 'naive-ui'

defineProps<{ errors: string[]; title?: string }>()
</script>

<template>
  <NAlert v-if="errors.length" type="error" :title="title ?? '保存失败'" style="margin-bottom: 12px">
    <ul style="margin: 0; padding-left: 18px">
      <li v-for="(e, i) in errors" :key="i">{{ e }}</li>
    </ul>
  </NAlert>
</template>
