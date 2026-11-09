<script setup lang="ts">
import { NAlert, NButton } from 'naive-ui'
import type { Schemas } from '../api/client'
import { runtime } from '../runtime'

/** 用户订阅缺节点的提示：没有任何节点时给出原因，有 stack 尚未应用时提示去应用。两者都没有时不显示。 */
defineProps<{ status: Schemas['UserSubscriptionStatus'] | null | undefined }>()
</script>

<template>
  <NAlert v-if="status && (status.nodes === 0 || status.unapplied_stacks.length)" type="warning" :bordered="false" class="gap-alert"
    :title="status.nodes === 0 ? '订阅端会对该用户返回 not_found' : '部分节点尚未进入订阅'">
    <template v-if="status.nodes === 0">{{ status.no_nodes_reason }}</template>
    <template v-else>
      入站已引用该用户，但 stack {{ status.unapplied_stacks.join('、') }} 还没有重新应用，这些 stack 的节点暂时不在订阅中。
    </template>
    <template v-if="status.unapplied_stacks.length">
      {{ status.auto_publish ? '应用后会自动发布到订阅端。' : '应用后再发布订阅包即可生效。' }}
      <NButton text type="primary" style="margin-left: 8px" @click="runtime.pendingDrawer = true">查看待应用变更</NButton>
    </template>
  </NAlert>
</template>

<style scoped>
.gap-alert { margin-bottom: 12px; }
</style>
