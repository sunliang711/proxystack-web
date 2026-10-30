<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  NBadge,
  NButton,
  NDropdown,
  NLayout,
  NLayoutContent,
  NLayoutHeader,
  NLayoutSider,
  NMenu,
  type MenuOption,
} from 'naive-ui'
import { logout, session } from '../session'
import PendingDrawer from '../components/PendingDrawer.vue'
import { connectEvents, disconnectEvents, refreshPending, runtime } from '../runtime'

const route = useRoute()
const router = useRouter()
const collapsed = ref(false)

const link = (name: string, label: string): MenuOption => ({
  key: name,
  label: () => h(RouterLink, { to: { name } }, { default: () => label }),
})

const menu: MenuOption[] = [
  link('dashboard', '总览'),
  link('stacks', 'Stack'),
  link('users', '用户'),
  link('deps', '依赖'),
  link('history', '配置历史'),
  link('backup', '备份'),
  link('settings', '系统设置'),
  link('system', '系统'),
]
const parents: Record<string, string> = { stack: 'stacks', user: 'users' }
const active = computed(() => {
  const name = String(route.name ?? '')
  return parents[name] ?? name
})
const title = computed(() => String(route.meta.title ?? ''))

const showPending = ref(false)
onMounted(() => {
  connectEvents()
  refreshPending()
})
onBeforeUnmount(disconnectEvents)
// 任何页面保存后都可能产生待应用变更：路由切换时顺便刷新。
router.afterEach(() => refreshPending())

const accountOptions = [
  { key: 'password', label: '修改密码' },
  { key: 'logout', label: '退出登录' },
]

async function onAccount(key: string) {
  if (key === 'password') {
    router.push({ name: 'change-password' })
  } else if (key === 'logout') {
    await logout()
    router.replace({ name: 'login' })
  }
}
</script>

<template>
  <NLayout has-sider style="height: 100vh">
    <NLayoutSider
      bordered
      collapse-mode="width"
      :collapsed-width="0"
      :width="200"
      :collapsed="collapsed"
      show-trigger="bar"
      breakpoint="md"
      @collapse="collapsed = true"
      @expand="collapsed = false"
    >
      <div class="brand">proxystack-web</div>
      <NMenu :options="menu" :value="active" />
    </NLayoutSider>
    <NLayout>
      <NLayoutHeader bordered class="header">
        <span class="title">{{ title }}</span>
        <div class="header-actions">
          <NBadge :value="runtime.pendingCount" :show="runtime.pendingCount > 0 || !!runtime.pendingError" :dot="!!runtime.pendingError && !runtime.pendingCount">
            <NButton :type="runtime.pendingCount ? 'warning' : 'default'" secondary size="small" @click="showPending = true">待应用变更</NButton>
          </NBadge>
          <NDropdown :options="accountOptions" trigger="click" @select="onAccount">
            <NButton quaternary>{{ session.admin?.username }}</NButton>
          </NDropdown>
        </div>
      </NLayoutHeader>
      <NLayoutContent content-style="padding: 16px;" :native-scrollbar="false" style="height: calc(100vh - 57px)">
        <RouterView />
      </NLayoutContent>
    </NLayout>
  </NLayout>
  <PendingDrawer v-model:show="showPending" />
</template>

<style scoped>
.brand {
  font-weight: 600;
  font-size: 16px;
  padding: 18px 20px 12px;
  white-space: nowrap;
}
.header {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
}
.header-actions {
  display: flex;
  align-items: center;
  gap: 16px;
}
.title {
  font-size: 16px;
  font-weight: 500;
}
</style>
