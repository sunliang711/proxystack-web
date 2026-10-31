<script setup lang="ts">
import { NCard, NIcon } from 'naive-ui'
import { LayersOutline } from '@vicons/ionicons5'

defineProps<{ title: string }>()
</script>

<template>
  <div class="auth-page">
    <div class="auth-box">
      <div class="auth-brand">
        <div class="auth-mark">
          <NIcon :size="22"><LayersOutline /></NIcon>
        </div>
        <span class="auth-name">proxystack</span>
      </div>
      <NCard :title="title" class="auth-card" size="large">
        <slot />
      </NCard>
    </div>
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
  background:
    radial-gradient(circle at 20% 10%, rgba(24, 160, 88, 0.12), transparent 45%),
    radial-gradient(circle at 85% 90%, rgba(32, 128, 240, 0.1), transparent 40%);
}
.auth-box {
  width: 100%;
  max-width: 400px;
}
.auth-brand {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  margin-bottom: 20px;
}
.auth-mark {
  width: 38px;
  height: 38px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  background: linear-gradient(135deg, #36ad6a, #18a058 55%, #0c7a43);
  box-shadow: 0 4px 12px rgba(24, 160, 88, 0.35);
}
.auth-name {
  font-size: 22px;
  font-weight: 600;
  letter-spacing: 0.3px;
}
.auth-card {
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.06);
}
</style>
