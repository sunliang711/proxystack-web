<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NEmpty, NSpace, NTabPane, NTabs } from 'naive-ui'
import { api } from '../api/client'
import { loadTimezone } from '../composables/useTrafficTrend'
import StatsPanel from './traffic/StatsPanel.vue'
import RankingPanel from './traffic/RankingPanel.vue'

type Tab = 'user' | 'stack' | 'ranking'
const route = useRoute()
const router = useRouter()
const tabs: Tab[] = ['user', 'stack', 'ranking']
const tab = ref<Tab>(tabs.includes(route.query.tab as Tab) ? (route.query.tab as Tab) : 'user')
watch(tab, (next) => {
  void router.replace({ query: { ...route.query, tab: next === 'user' ? undefined : next } })
})

const timezone = ref('')
const userOptions = ref<{ label: string; value: number }[]>([])
const stackOptions = ref<{ label: string; value: number }[]>([])
onMounted(async () => {
  const [zone, { data: stacks }, { data: users }] = await Promise.all([loadTimezone(), api.GET('/api/stacks'), api.GET('/api/users')])
  stackOptions.value = (stacks?.stacks ?? []).map((stack) => ({ label: stack.role === 'auto' ? `${stack.name}（auto）` : stack.name, value: stack.id }))
  userOptions.value = (users?.users ?? []).map((user) => ({ label: user.remark ? `${user.name}（${user.remark}）` : user.name, value: user.id! }))
  timezone.value = zone
})
</script>

<template>
  <NSpace vertical :size="16">
    <NTabs v-model:value="tab" type="line" animated>
      <NTabPane name="user" tab="按用户" />
      <NTabPane name="stack" tab="按 Stack" />
      <NTabPane name="ranking" tab="排行" />
    </NTabs>
    <template v-if="timezone">
      <StatsPanel v-if="tab !== 'ranking'" :key="tab" :view="tab" :timezone="timezone" :user-options="userOptions" :stack-options="stackOptions" />
      <RankingPanel v-else :timezone="timezone" :stack-options="stackOptions" />
    </template>
    <NEmpty v-else description="正在加载" />
  </NSpace>
</template>
