<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NTabPane, NTabs } from 'naive-ui'
import { api } from '../api/client'
import ManualNodesView from './ManualNodesView.vue'
import RemoteSourcesView from './RemoteSourcesView.vue'

/** 本机 stack 之外进入订阅的节点：手工录入的节点，以及从其他实例拉取的远端节点源。 */
const route = useRoute()
const router = useRouter()
type Tab = 'manual' | 'remote'
const tab = ref<Tab>(route.query.tab === 'remote' ? 'remote' : 'manual')
// 已在本页时从全局搜索打开另一个标签页。
watch(() => route.query.tab, (value) => { if (route.name === 'extra-nodes') tab.value = value === 'remote' ? 'remote' : 'manual' })
watch(tab, (name) => {
  if (route.query.tab !== name) void router.replace({ query: { ...route.query, tab: name === 'manual' ? undefined : name } })
})
const initialUser = computed(() => (typeof route.query.user === 'string' ? route.query.user : ''))
const manualCount = ref<number | null>(null)
const remoteCount = ref<number | null>(null)
// 标签上的数量先整体读取一次；子页面增删后通过 count 事件更新。
onMounted(async () => {
  const [manual, remote] = await Promise.all([api.GET('/api/manual-nodes'), api.GET('/api/remote-sources')])
  if (manualCount.value === null) manualCount.value = manual.data?.length ?? null
  if (remoteCount.value === null) remoteCount.value = remote.data?.length ?? null
})
const label = (text: string, count: number | null) => (count === null ? text : `${text} ${count}`)
</script>

<template>
  <NTabs v-model:value="tab" type="line" class="extra-tabs">
    <NTabPane name="manual" :tab="label('手工节点', manualCount)" display-directive="show:lazy">
      <ManualNodesView :initial-user="initialUser" @count="(value: number) => (manualCount = value)" />
    </NTabPane>
    <NTabPane name="remote" :tab="label('远端节点源', remoteCount)" display-directive="show:lazy">
      <RemoteSourcesView @count="(value: number) => (remoteCount = value)" />
    </NTabPane>
  </NTabs>
</template>

<style scoped>
.extra-tabs > :deep(.n-tabs-nav) { margin-bottom: 4px; }
</style>
