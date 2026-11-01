<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref, watch, type Component } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  NBadge,
  NBreadcrumb,
  NBreadcrumbItem,
  NButton,
  NDropdown,
  NIcon,
  NLayout,
  NLayoutContent,
  NLayoutHeader,
  NLayoutSider,
  NMenu,
  NTooltip,
  useMessage,
  type MenuOption,
} from 'naive-ui'
import {
  ArchiveOutline,
  CheckmarkOutline,
  ChevronDownOutline,
  CloudDownloadOutline,
  CubeOutline,
  DesktopOutline,
  DocumentTextOutline,
  GitCommitOutline,
  GitPullRequestOutline,
  GridOutline,
  InformationCircleOutline,
  LayersOutline,
  ListOutline,
  MoonOutline,
  MenuOutline,
  NotificationsOutline,
  PaperPlaneOutline,
  PeopleOutline,
  PersonCircleOutline,
  PulseOutline,
  ReceiptOutline,
  ServerOutline,
  SettingsOutline,
  StatsChartOutline,
  SunnyOutline,
  TimerOutline,
} from '@vicons/ionicons5'
import { logout, session } from '../session'
import { themePreference, type ThemePreference } from '../theme'
import PendingDrawer from '../components/PendingDrawer.vue'
import { connectEvents, disconnectEvents, refreshPending, runtime } from '../runtime'
import { api, type Schemas } from '../api/client'
import brandLogo from '/favicon.svg?url'
import TaskCenterDrawer from '../components/TaskCenterDrawer.vue'
import { taskCenter } from '../tasks'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const taskState = taskCenter.state
const stopNotices = taskCenter.onNotice(({ kind, task }) => {
  const failed = task.status === 'failed' || task.status === 'timeout'
  const text = kind === 'started' ? `已提交：${task.title}` : `${task.title}：${failed ? '执行失败' : task.status === 'skipped' ? '已跳过' : '已完成'}`
  const content = () => h('span', [text, h(NButton, { text: true, type: 'primary', style: 'margin-left: 12px', onClick: () => taskCenter.open(task) }, () => '查看详情')])
  if (kind === 'started') message.info(content, { duration: 4000, closable: true })
  else if (failed) message.error(content, { duration: 8000, closable: true })
  else message.success(content, { duration: 4000, closable: true })
})
// 窄屏侧栏覆盖正文，关闭后不占宽度；桌面仍支持收起为图标导航。
const mobileQuery = window.matchMedia('(max-width: 767px)')
const mobile = ref(mobileQuery.matches)
const collapsed = ref(mobile.value)
function resizeNavigation(event: MediaQueryListEvent) {
  mobile.value = event.matches
  collapsed.value = event.matches
}
mobileQuery.addEventListener('change', resizeNavigation)
watch(() => route.fullPath, () => { if (mobile.value) collapsed.value = true })
const buildInfo = ref<Schemas['SystemInfo'] | null>(null)
const versionLoaded = ref(false)
const versionLabel = computed(() => {
  if (!buildInfo.value) return versionLoaded.value ? '版本未知' : '读取版本中'
  const version = buildInfo.value.version
  return version.startsWith('dev+') ? `dev · ${version.slice(4).split('.').join(' · ')}` : version
})

async function loadVersion() {
  try {
    const { data } = await api.GET('/api/system/info')
    buildInfo.value = data ?? null
  } catch {
    buildInfo.value = null
  } finally {
    versionLoaded.value = true
  }
}

function buildValue(value: string | undefined) {
  return value && value !== 'unknown' ? value : '未知'
}

interface NavItem {
  name: string
  label: string
  icon: Component
}

const groups: { key: string; label: string; items: NavItem[] }[] = [
  {
    key: 'overview',
    label: '概览',
    items: [
      { name: 'dashboard', label: '总览', icon: GridOutline },
      { name: 'traffic', label: '流量明细', icon: StatsChartOutline },
    ],
  },
  {
    key: 'proxy',
    label: '代理',
    items: [
      { name: 'stacks', label: 'Stack', icon: LayersOutline },
      { name: 'users', label: '用户', icon: PeopleOutline },
      { name: 'manual-nodes', label: '手工节点', icon: ServerOutline },
      { name: 'remote-sources', label: '远端节点源', icon: CloudDownloadOutline },
    ],
  },
  {
    key: 'subscription',
    label: '订阅',
    items: [
      { name: 'templates', label: '订阅模板', icon: DocumentTextOutline },
      { name: 'sub-publish', label: '发布订阅包', icon: PaperPlaneOutline },
    ],
  },
  {
    key: 'ops',
    label: '运维',
    items: [
      { name: 'deps', label: '依赖', icon: CubeOutline },
      { name: 'tasks', label: '任务中心', icon: ListOutline },
      { name: 'jobs', label: '定时任务', icon: TimerOutline },
      { name: 'health', label: '健康检查', icon: PulseOutline },
      { name: 'notifications', label: '通知记录', icon: NotificationsOutline },
      { name: 'audit', label: '审计日志', icon: ReceiptOutline },
      { name: 'history', label: '配置历史', icon: GitCommitOutline },
      { name: 'backup', label: '备份与还原', icon: ArchiveOutline },
    ],
  },
  {
    key: 'system',
    label: '系统',
    items: [
      { name: 'settings', label: '系统设置', icon: SettingsOutline },
      { name: 'system', label: '系统', icon: InformationCircleOutline },
    ],
  },
]

const icon = (component: Component) => () => h(NIcon, null, { default: () => h(component) })

const menu: MenuOption[] = groups.map((group) => ({
  type: 'group',
  key: group.key,
  label: group.label,
  children: group.items.map((item) => ({
    key: item.name,
    label: () => h(RouterLink, { to: { name: item.name }, title: item.label }, { default: () => item.label }),
    icon: icon(item.icon),
  })),
}))

const parents: Record<string, string> = { stack: 'stacks', user: 'users', 'task-detail': 'tasks' }
const active = computed(() => {
  const name = String(route.name ?? '')
  return parents[name] ?? name
})
const title = computed(() => route.meta.title ?? '')
const description = computed(() => route.meta.description ?? '')

// 面包屑：分组 / 列表页 / 详情页，列表页在详情页中可点击返回。
const crumbs = computed(() => {
  const name = String(route.name ?? '')
  const group = groups.find((g) => g.items.some((item) => item.name === active.value))
  const items: { label: string; to?: string }[] = []
  if (group) {
    items.push({ label: group.label })
  }
  if (parents[name]) {
    const parent = group?.items.find((item) => item.name === parents[name])
    items.push({ label: parent?.label ?? '', to: parents[name] })
  }
  items.push({ label: title.value })
  return items
})

const showPending = ref(false)
watch(() => taskState.drawer, (show) => { if (show) showPending.value = false })
onMounted(() => {
  loadVersion()
  taskCenter.start(session.admin!.username)
  connectEvents()
  refreshPending()
})
onBeforeUnmount(() => {
  stopNotices()
  taskCenter.stop()
  disconnectEvents()
  mobileQuery.removeEventListener('change', resizeNavigation)
})
watch(() => runtime.connected, (connected) => { if (connected) void taskCenter.refresh() })
// 任何页面保存后都可能产生待应用变更：路由切换时顺便刷新。
router.afterEach(() => refreshPending())

const themeIcons: Record<ThemePreference, Component> = { system: DesktopOutline, light: SunnyOutline, dark: MoonOutline }
const themeLabels: Record<ThemePreference, string> = { system: '跟随系统', light: '浅色', dark: '深色' }
// 当前选中的主题在菜单项末尾打勾。
const themeOptions = computed(() =>
  (['system', 'light', 'dark'] as ThemePreference[]).map((key) => ({
    key,
    icon: icon(themeIcons[key]),
    label: () =>
      h('span', { style: 'display: inline-flex; align-items: center; gap: 12px' }, [
        themeLabels[key],
        key === themePreference.value ? h(NIcon, { size: 14 }, { default: () => h(CheckmarkOutline) }) : null,
      ]),
  })),
)

const accountOptions = [
  { key: 'security', label: '账号安全' },
  { key: 'password', label: '修改密码' },
  { key: 'logout', label: '退出登录' },
]

async function onAccount(key: string) {
  if (key === 'security') {
    router.push({ name: 'security' })
  } else if (key === 'password') {
    router.push({ name: 'change-password' })
  } else if (key === 'logout') {
    await logout()
    router.replace({ name: 'login' })
  }
}
</script>

<template>
  <NLayout has-sider class="app-shell" :class="{ 'is-mobile': mobile }">
    <button v-if="mobile && !collapsed" class="nav-backdrop" aria-label="关闭导航" @click="collapsed = true" />
    <NLayoutSider
      class="app-sidebar"
      :inert="mobile && collapsed"
      bordered
      collapse-mode="width"
      :collapsed-width="mobile ? 0 : 68"
      :width="232"
      :collapsed="collapsed"
      :native-scrollbar="false"
      :show-trigger="mobile ? false : 'bar'"
      @collapse="collapsed = true"
      @expand="collapsed = false"
    >
      <NTooltip placement="bottom-start" style="max-width: min(420px, calc(100vw - 32px)); box-sizing: border-box; overflow-wrap: anywhere">
        <template #trigger>
          <RouterLink :to="{ name: 'system' }" class="brand" :class="{ collapsed }" :aria-label="`系统信息，${versionLabel}`">
            <img class="brand-mark" :src="brandLogo" alt="" width="34" height="34" />
            <div v-if="!collapsed" class="brand-text">
              <span class="brand-name">proxystack</span>
              <span class="brand-sub">管理面板 · {{ versionLabel }}</span>
            </div>
          </RouterLink>
        </template>
        <div>版本：{{ buildInfo?.version || versionLabel }}</div>
        <div>提交：{{ buildValue(buildInfo?.commit) }}</div>
        <div>构建时间：{{ buildValue(buildInfo?.build_date) }}</div>
        <div>点击查看系统信息</div>
      </NTooltip>
      <NMenu
        :options="menu"
        :value="active"
        :collapsed="collapsed"
        :collapsed-width="mobile ? 0 : 68"
        :collapsed-icon-size="20"
        :icon-size="18"
        :indent="20"
        class="nav"
      />
    </NLayoutSider>
    <NLayout>
      <NLayoutHeader bordered class="header">
        <NButton v-if="mobile" quaternary circle aria-label="打开导航" @click="collapsed = !collapsed">
          <template #icon><NIcon><MenuOutline /></NIcon></template>
        </NButton>
        <NBreadcrumb class="crumbs">
          <NBreadcrumbItem v-for="(crumb, i) in crumbs" :key="i" :clickable="!!crumb.to">
            <RouterLink v-if="crumb.to" :to="{ name: crumb.to }" class="crumb-link">{{ crumb.label }}</RouterLink>
            <template v-else>{{ crumb.label }}</template>
          </NBreadcrumbItem>
        </NBreadcrumb>
        <div class="header-actions">
          <NDropdown :options="themeOptions" trigger="click" placement="bottom-end" @select="(key: ThemePreference) => (themePreference = key)">
            <NButton quaternary circle size="small" :title="`主题：${themeLabels[themePreference]}`">
              <template #icon><NIcon><component :is="themeIcons[themePreference]" /></NIcon></template>
            </NButton>
          </NDropdown>
          <NBadge :value="taskState.running" :show="taskState.running > 0 || taskState.unread.length > 0" :dot="!taskState.running && taskState.unread.length > 0" :type="taskState.unread.length ? 'error' : 'info'">
            <NButton secondary size="small" class="task-button" aria-label="后台任务" :title="`${taskState.running} 个运行中，${taskState.unread.length} 个未读失败提醒`" @click="showPending = false; taskCenter.open()">
              <template #icon><NIcon><ListOutline /></NIcon></template>
              <span class="task-label">后台任务</span>
            </NButton>
          </NBadge>
          <NBadge :value="runtime.pendingCount" :show="runtime.pendingCount > 0 || !!runtime.pendingError" :dot="!!runtime.pendingError && !runtime.pendingCount">
            <NButton :type="runtime.pendingCount ? 'warning' : 'default'" secondary size="small" class="pending-button" aria-label="待应用变更" @click="taskState.drawer = false; showPending = true">
              <template #icon><NIcon><GitPullRequestOutline /></NIcon></template>
              <span class="pending-label">待应用变更</span>
            </NButton>
          </NBadge>
          <NDropdown :options="accountOptions" trigger="click" placement="bottom-end" @select="onAccount">
            <NButton quaternary size="small" icon-placement="right">
              <template #icon><NIcon><ChevronDownOutline /></NIcon></template>
              <span class="account">
                <NIcon :size="20"><PersonCircleOutline /></NIcon>
                {{ session.admin?.username }}
              </span>
            </NButton>
          </NDropdown>
        </div>
      </NLayoutHeader>
      <NLayoutContent :native-scrollbar="false" class="workspace-content">
        <main class="page" :data-page="route.name">
          <div class="page-head">
            <h1 class="page-title">{{ title }}</h1>
            <p v-if="description" class="page-desc">{{ description }}</p>
          </div>
          <div class="page-body"><RouterView /></div>
        </main>
      </NLayoutContent>
    </NLayout>
  </NLayout>
  <PendingDrawer v-model:show="showPending" />
  <TaskCenterDrawer />
</template>

<style scoped>
.app-shell {
  height: 100vh;
  height: 100dvh;
}
.app-shell > :deep(.n-layout) { min-width: 0; }
.app-sidebar { z-index: 25; }
.brand {
  position: sticky;
  top: 0;
  z-index: 2;
  background: var(--n-color);
  display: flex;
  align-items: center;
  gap: 10px;
  height: 76px;
  padding: 0 22px;
  box-sizing: border-box;
  white-space: nowrap;
  overflow: hidden;
  color: inherit;
  text-decoration: none;
}
.brand.collapsed {
  padding: 0;
  justify-content: center;
}
.brand-mark {
  flex: none;
  width: 34px;
  height: 34px;
  display: block;
}
.brand-text {
  display: flex;
  flex-direction: column;
  gap: 4px;
  line-height: 1.2;
  min-width: 0;
}
.brand-name {
  font-weight: 600;
  font-size: 17px;
  letter-spacing: 0.2px;
}
.brand-sub {
  font-size: 12px;
  color: var(--ps-muted);
  overflow: hidden;
  text-overflow: ellipsis;
}
.nav {
  padding: 0 6px 20px;
}
.nav :deep(.n-menu-item) { margin-top: 3px; }
.nav :deep(.n-menu-item-group-title) { height: 30px; line-height: 30px; font-size: 11px; letter-spacing: 1px; }
.header {
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 32px;
}
.crumbs {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
}
.header-actions {
  flex: none;
  display: flex;
  align-items: center;
  gap: 12px;
}
.account {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.crumb-link {
  color: inherit;
}
.crumb-link:hover {
  text-decoration: none;
}
.page {
  padding: 28px 32px 48px;
  max-width: 1680px;
  margin: 0 auto;
}
.workspace-content { height: calc(100vh - 64px); height: calc(100dvh - 64px); }
.page:is([data-page='user'], [data-page='settings'], [data-page='backup'], [data-page='sub-publish'], [data-page='system'], [data-page='security']) { max-width: 1160px; }
.page-head {
  margin-bottom: 24px;
}
.page-title {
  margin: 0;
  font-size: 26px;
  font-weight: 600;
  line-height: 36px;
  letter-spacing: -0.5px;
}
.page-desc {
  margin: 8px 0 0;
  max-width: 960px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--ps-muted);
}
@media (max-width: 640px) {
  .page {
    padding: 20px 16px 32px;
  }
  .header {
    padding: 0 12px;
  }
  .header-actions { gap: 4px; }
  .pending-label, .task-label { display: none; }
  .pending-button :deep(.n-button__icon), .task-button :deep(.n-button__icon) { margin: 0; }
  .page-title { font-size: 23px; }
}

@media (max-width: 1023px) {
  .crumbs :deep(.n-breadcrumb-item:not(:last-child)) { display: none; }
}

@media (max-width: 767px) {
  .is-mobile > .app-sidebar { position: fixed; top: 0; bottom: 0; left: 0; height: 100dvh; }
  .nav-backdrop { position: fixed; inset: 0; z-index: 24; border: 0; background: rgba(0, 0, 0, 0.6); backdrop-filter: blur(3px); }
  .page :deep(.n-form) {
    max-width: none !important;
  }

  .page :deep(.n-form-item) {
    display: block;
  }

  .page :deep(.n-form-item .n-form-item-label) {
    display: flex;
    width: auto !important;
    height: auto !important;
    min-height: 0;
    padding: 0 0 4px !important;
    justify-content: flex-start;
    text-align: left;
  }

  .page :deep(.n-form-item-blank) {
    min-width: 0;
  }
}
</style>
