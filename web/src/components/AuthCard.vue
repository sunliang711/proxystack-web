<script setup lang="ts">
import { NCard } from 'naive-ui'

defineProps<{ title: string }>()
</script>

<template>
  <div class="auth-page">
    <NCard :title="title" class="auth-card" size="large">
      <slot />
    </NCard>
  </div>
</template>

<style scoped>
.auth-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  box-sizing: border-box;
}
.auth-card {
  width: 100%;
  max-width: 400px;
}
</style>
