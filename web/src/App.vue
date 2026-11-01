<script setup lang="ts">
import { computed } from 'vue'
import {
  darkTheme,
  dateZhCN,
  NConfigProvider,
  NDialogProvider,
  NGlobalStyle,
  NMessageProvider,
  zhCN,
  type GlobalThemeOverrides,
} from 'naive-ui'
import { isDark as dark } from './theme'
import './styles.css'

const theme = computed(() => (dark.value ? darkTheme : null))

const fontFamily =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Helvetica Neue", Arial, sans-serif'

// 全站共用颜色和尺寸，图表及普通 CSS 使用 styles.css 中对应的主题变量。
const overrides = computed<GlobalThemeOverrides>(() => ({
  common: {
    fontFamily,
    borderRadius: '8px',
    primaryColor: dark.value ? '#52d6b4' : '#087f6a',
    primaryColorHover: dark.value ? '#7ee4c9' : '#129a80',
    primaryColorPressed: dark.value ? '#36b996' : '#066957',
    primaryColorSuppl: '#129a80',
    bodyColor: dark.value ? '#0b111b' : '#f3f6fa',
    cardColor: dark.value ? '#131d2b' : '#ffffff',
    modalColor: dark.value ? '#182435' : '#ffffff',
    popoverColor: dark.value ? '#1b293b' : '#ffffff',
    tableColor: dark.value ? '#131d2b' : '#ffffff',
    inputColor: dark.value ? '#0e1724' : '#f8fafc',
    borderColor: dark.value ? '#29374a' : '#dce4ed',
    dividerColor: dark.value ? '#243247' : '#e7edf3',
    textColorBase: dark.value ? '#e7eef8' : '#1e293b',
    textColor1: dark.value ? '#edf3fc' : '#17283d',
    textColor2: dark.value ? '#c5d1e2' : '#40516a',
    textColor3: dark.value ? '#94a6bf' : '#65758b',
    placeholderColor: dark.value ? '#778ba5' : '#8190a5',
    heightMedium: '36px',
    fontSize: '14px',
    lineHeight: '1.65',
  },
  Layout: {
    color: dark.value ? '#0b111b' : '#f3f6fa',
    headerColor: dark.value ? '#101925' : '#ffffff',
    siderColor: dark.value ? '#101925' : '#ffffff',
  },
  Card: {
    borderRadius: '14px',
    titleFontWeight: '600',
    titleFontSizeSmall: '15px',
    titleFontSizeMedium: '16px',
    paddingSmall: '20px',
    paddingMedium: '24px',
    paddingLarge: '28px',
  },
  Menu: {
    itemHeight: '36px',
    borderRadius: '8px',
    groupTextColor: dark.value ? '#71859f' : '#8190a5',
    itemColorActive: dark.value ? '#1b3a3b' : '#e5f5f0',
    itemTextColorActive: dark.value ? '#7ce5c8' : '#087f6a',
    itemIconColorActive: dark.value ? '#7ce5c8' : '#087f6a',
  },
  DataTable: {
    thFontWeight: '600',
    thColor: dark.value ? '#182436' : '#f4f7fb',
    tdColorHover: dark.value ? '#19283b' : '#f1f7f8',
    tdColorStriped: dark.value ? '#152131' : '#f9fbfd',
    borderRadius: '10px',
    thPaddingMedium: '13px 16px',
    tdPaddingMedium: '14px 16px',
    thPaddingSmall: '11px 14px',
    tdPaddingSmall: '12px 14px',
  },
  Form: { labelTextColor: dark.value ? '#c5d1e2' : '#40516a', feedbackTextColor: dark.value ? '#94a6bf' : '#65758b' },
  Tabs: { tabFontWeightActive: '600', tabGapMediumLine: '28px' },
  Statistic: { labelTextColor: dark.value ? '#94a6bf' : '#65758b', valueFontWeight: '600' },
  Tag: { borderRadius: '6px' },
  Button: { fontWeight: '500' },
  Tooltip: { borderRadius: '8px' },
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
  color: var(--ps-accent);
  text-decoration: none;
}
a:hover {
  text-decoration: underline;
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
