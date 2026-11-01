<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { NCard, NSpace } from 'naive-ui'
import TaskDetails from '../components/TaskDetails.vue'
import type { TaskSource } from '../tasks'

const route = useRoute()
const source = computed(() => route.params.source as TaskSource)
const id = computed(() => Number(route.params.id))
</script>

<template>
  <NSpace vertical :size="20">
    <RouterLink :to="{ name: 'tasks' }">← 返回任务中心</RouterLink>
    <NCard size="small"><TaskDetails :source="source" :id="id" /></NCard>
  </NSpace>
</template>
