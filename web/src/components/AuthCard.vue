<script setup lang="ts">
import { NCard } from 'naive-ui'
import brandLogo from '/favicon.svg?url'

defineProps<{ title: string }>()
</script>

<template>
  <div class="auth-page">
    <div class="auth-box">
      <div class="auth-brand">
        <img class="auth-mark" :src="brandLogo" alt="" width="48" height="48" />
        <div>
          <div class="auth-name">proxystack</div>
          <div class="auth-caption">代理服务管理平台</div>
        </div>
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
  padding: 32px 20px;
  box-sizing: border-box;
  background:
    radial-gradient(ellipse at 30% 15%, rgba(40, 174, 143, 0.12), transparent 50%),
    radial-gradient(ellipse at 80% 85%, rgba(61, 110, 194, 0.09), transparent 45%),
    var(--ps-bg);
}
.auth-box {
  width: 100%;
  max-width: 440px;
}
.auth-brand {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 14px;
  margin-bottom: 32px;
}
.auth-mark {
  flex: none;
  width: 48px;
  height: 48px;
  display: block;
}
.auth-name {
  font-size: 24px;
  font-weight: 600;
  letter-spacing: 0.3px;
}
.auth-card {
  box-shadow: 0 24px 80px rgba(0, 0, 0, 0.16);
}
.auth-caption { margin-top: 4px; font-size: 12px; color: var(--ps-muted); letter-spacing: 1px; }
</style>
