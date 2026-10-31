<script setup lang="ts">
import { computed } from 'vue'
import {
  darkTheme,
  dateZhCN,
  NConfigProvider,
  NDialogProvider,
  NGlobalStyle,
  NMessageProvider,
  useOsTheme,
  zhCN,
  type GlobalThemeOverrides,
} from 'naive-ui'

const osTheme = useOsTheme()
const dark = computed(() => osTheme.value === 'dark')
const theme = computed(() => (dark.value ? darkTheme : null))

const fontFamily =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Helvetica Neue", Arial, sans-serif'

// 浅色主题下正文区用浅灰底，让白色卡片有层次；深色主题沿用默认配色。
const overrides = computed<GlobalThemeOverrides>(() => ({
  common: {
    fontFamily,
    borderRadius: '6px',
  },
  Layout: dark.value ? {} : { color: '#f5f7fa' },
  Card: {
    borderRadius: '10px',
    titleFontWeight: '600',
  },
  Menu: {
    itemHeight: '38px',
    borderRadius: '6px',
    groupTextColor: dark.value ? 'rgba(255, 255, 255, 0.38)' : 'rgba(31, 34, 37, 0.42)',
  },
  DataTable: {
    thFontWeight: '600',
  },
}))
</script>

<template>
  <NConfigProvider :theme="theme" :theme-overrides="overrides" :locale="zhCN" :date-locale="dateZhCN">
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
/* 卡片标题栏里的辅助文字使用正文字号，不随标题加粗放大。 */
.n-card-header .header-note {
  font-size: 14px;
  font-weight: 400;
}
/* 侧栏菜单里的链接跟随菜单项配色。 */
.n-menu a,
.n-menu a:hover {
  color: inherit;
  text-decoration: none;
}
</style>
