<script setup lang="ts">
import { NRadioButton, NRadioGroup } from 'naive-ui'

const value = defineModel<string>('value', { required: true })
</script>

<template>
  <NRadioGroup v-model:value="value">
    <NRadioButton value="inherit">继承全局</NRadioButton>
    <NRadioButton value="on">开</NRadioButton>
    <NRadioButton value="off">关</NRadioButton>
  </NRadioGroup>
</template>
