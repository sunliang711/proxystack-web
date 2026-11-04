<script setup lang="ts">
import { computed, type Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NTabPane, NTabs, NText } from 'naive-ui'
import AuditView from './AuditView.vue'
import HistoryView from './HistoryView.vue'
import NotificationsView from './NotificationsView.vue'

// 审计日志、配置历史和通知记录都是只读的历史记录，合在一个侧栏入口下，用标签切换。
const tabs: { name: string; label: string; description: string; view: Component }[] = [
  { name: 'audit', label: '审计日志', description: '管理员在面板上执行的每一次修改操作及其结果，可按操作类型和操作者筛选。', view: AuditView },
  { name: 'history', label: '配置历史', description: '每次配置变更都会保存一个完整快照。可以比较任意两个版本的差异，或回滚到选中的版本。', view: HistoryView },
  { name: 'notifications', label: '通知记录', description: '每日阈值、额度超限、到期暂停等事件产生的邮件通知及其发送状态。发送失败的记录可以手动重发。', view: NotificationsView },
]

const route = useRoute()
const router = useRouter()
const tab = computed({
  get: () => (tabs.some((item) => item.name === route.query.tab) ? String(route.query.tab) : 'audit'),
  set: (value: string) => { void router.replace({ query: { ...route.query, tab: value } }) },
})
</script>

<template>
  <NTabs v-model:value="tab" type="line" class="records-tabs">
    <NTabPane v-for="item in tabs" :key="item.name" :name="item.name" :tab="item.label" display-directive="if">
      <NText depth="3" tag="p" class="tab-desc">{{ item.description }}</NText>
      <component :is="item.view" />
    </NTabPane>
  </NTabs>
</template>

<style scoped>
.tab-desc {
  margin: 4px 0 16px;
  font-size: 13px;
}
</style>
