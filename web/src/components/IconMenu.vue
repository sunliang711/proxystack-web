<script setup lang="ts">
import { NPopover } from 'naive-ui'
import DomainIcon from './DomainIcon.vue'
import IconActions from './IconActions.vue'
import { ref } from 'vue'

/** 可点击的网站图标：点开后为该主域名（或服务行的服务）设置手动图标。 */
const props = withDefaults(defineProps<{ domain: string; service?: string; fallbacks?: string[]; size?: number }>(), { size: 16, fallbacks: () => [], service: '' })
const show = ref(false)
</script>

<template>
  <NPopover v-model:show="show" trigger="click" placement="bottom-start" :show-arrow="false">
    <template #trigger>
      <button type="button" class="icon-trigger" :title="`设置 ${props.service || props.domain} 的图标`" @click.stop>
        <DomainIcon :domain="domain" :service="service" :fallbacks="fallbacks" :size="size" />
      </button>
    </template>
    <IconActions :kind="service ? 'service' : 'domain'" :name="service || domain" @done="show = false" />
  </NPopover>
</template>

<style scoped>
.icon-trigger { display: inline-flex; padding: 0; border: 0; background: none; cursor: pointer; border-radius: 4px; line-height: 0; }
.icon-trigger:hover, .icon-trigger:focus-visible { outline: 2px solid var(--ps-accent); outline-offset: 1px; }
</style>
