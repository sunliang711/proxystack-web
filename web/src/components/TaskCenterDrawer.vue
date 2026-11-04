<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { NAlert, NButton, NDrawer, NDrawerContent, NEmpty, NSpace, NTag, NText } from 'naive-ui'
import { taskCenter, taskKey, taskRoute } from '../tasks'
import { formatTime } from '../util/format'
import { taskDuration, taskStatus } from '../util/task'
import TaskDetails from './TaskDetails.vue'

const state = taskCenter.state
const now = ref(Date.now())
const timer = setInterval(() => { now.value = Date.now() }, 1000)
onBeforeUnmount(() => clearInterval(timer))
const shown = computed(() => state.items.slice(0, 20))
</script>

<template>
  <NDrawer v-model:show="state.drawer" width="min(520px, 100vw)" :show-mask="false" :mask-closable="false" :trap-focus="false" :block-scroll="false" :auto-focus="false" :close-on-esc="false" style="top: 64px; height: calc(100dvh - 64px)">
    <NDrawerContent title="后台任务" closable>
      <NSpace vertical :size="16">
        <NSpace justify="space-between" align="center">
          <NButton v-if="state.selected" size="small" @click="state.selected = null">返回任务列表</NButton>
          <NText v-else depth="3">{{ state.running ? `${state.running} 个任务正在运行` : '当前没有运行中的任务' }}</NText>
          <NButton size="small" :loading="state.loading" @click="taskCenter.refresh">刷新</NButton>
        </NSpace>
        <TaskDetails v-if="state.selected" :source="state.selected.source" :id="state.selected.id" />
        <template v-else>
          <NAlert v-if="state.error" type="warning" :bordered="false">{{ state.error }}</NAlert>
          <button v-for="task in shown" :key="taskKey(task)" class="task-item" @click="taskCenter.open(task)">
            <span class="task-item-head"><strong>{{ task.title }}</strong><NTag round size="small" :type="taskStatus[task.status].type">{{ taskStatus[task.status].label }}</NTag></span>
            <span class="task-meta">{{ task.source === 'job' ? '定时任务' : '一次性任务' }} · {{ formatTime(task.started_at) }} · {{ taskDuration(task, now) }}</span>
            <span v-if="task.error || task.log.length" class="task-line">{{ task.error || task.log[task.log.length - 1] }}</span>
          </button>
          <NEmpty v-if="!shown.length" :description="state.loading ? '正在读取任务' : '暂无任务记录'" />
          <NText v-if="state.items.length > 20" depth="3">此处展示最近 20 条，完整记录请进入后台任务页面。</NText>
        </template>
      </NSpace>
      <template #footer>
        <NSpace justify="space-between" align="center">
          <RouterLink v-if="state.selected" :to="taskRoute(state.selected)" target="_blank" rel="noopener">在新标签页查看完整详情 ↗</RouterLink>
          <RouterLink v-else :to="{ name: 'tasks' }" @click="state.drawer = false">查看全部后台任务</RouterLink>
          <NButton v-if="state.unread.length" size="small" @click="taskCenter.markRead()">失败提醒全部已读</NButton>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>

<style scoped>
.task-item { display: block; width: 100%; text-align: left; font: inherit; color: inherit; cursor: pointer; padding: 14px; border: 1px solid var(--ps-border); border-radius: 10px; background: transparent; }
.task-item:hover { border-color: var(--ps-accent); background: rgba(82, 214, 180, 0.04); }
.task-item-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }
.task-item-head strong { min-width: 0; overflow-wrap: anywhere; }
.task-meta { display: block; color: var(--ps-muted); font-size: 12px; margin-top: 8px; }
.task-line { display: block; margin-top: 8px; color: var(--ps-muted); font: 12px/1.6 ui-monospace, monospace; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
