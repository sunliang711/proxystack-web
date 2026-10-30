<script setup lang="ts">
import { computed } from 'vue'
import { darkTheme, dateZhCN, NConfigProvider, NDialogProvider, NGlobalStyle, NMessageProvider, useOsTheme, zhCN } from 'naive-ui'

const osTheme = useOsTheme()
const theme = computed(() => (osTheme.value === 'dark' ? darkTheme : null))
</script>

<template>
  <NConfigProvider :theme="theme" :locale="zhCN" :date-locale="dateZhCN">
    <NGlobalStyle />
    <NMessageProvider>
      <NDialogProvider>
        <RouterView />
      </NDialogProvider>
    </NMessageProvider>
  </NConfigProvider>
</template>

<style>
/* 页面内的路由链接沿用主题主色，暗色主题下也清晰可读。 */
a {
  color: #18a058;
  text-decoration: none;
}
a:hover {
  text-decoration: underline;
}
@media (prefers-color-scheme: dark) {
  a {
    color: #63e2b7;
  }
}
</style>
