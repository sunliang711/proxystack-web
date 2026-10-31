<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref, type Component } from 'vue'
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
  MoonOutline,
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

const route = useRoute()
const router = useRouter()
// 侧栏的 breakpoint 只在窗口尺寸变化时触发，首次进入时按当前宽度决定是否收起。
const collapsed = ref(window.matchMedia('(max-width: 767px)').matches)
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
      { name: 'jobs', label: '定时任务', icon: TimerOutline },
      { name: 'health', label: '健康检查', icon: PulseOutline },
      { name: 'notifications', label: '通知记录', icon: NotificationsOutline },
      { name: 'audit', label: '审计日志', icon: ReceiptOutline },
      { name: 'history', label: '配置历史', icon: GitCommitOutline },
      { name: 'backup', label: '备份', icon: ArchiveOutline },
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

const parents: Record<string, string> = { stack: 'stacks', user: 'users' }
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
onMounted(() => {
  loadVersion()
  connectEvents()
  refreshPending()
})
onBeforeUnmount(disconnectEvents)
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
  <NLayout has-sider style="height: 100vh">
    <NLayoutSider
      bordered
      collapse-mode="width"
      :collapsed-width="64"
      :width="220"
      :collapsed="collapsed"
      :native-scrollbar="false"
      show-trigger="bar"
      breakpoint="md"
      @collapse="collapsed = true"
      @expand="collapsed = false"
    >
      <NTooltip placement="bottom-start" style="max-width: min(420px, calc(100vw - 32px)); box-sizing: border-box; overflow-wrap: anywhere">
        <template #trigger>
          <RouterLink :to="{ name: 'system' }" class="brand" :class="{ collapsed }" :aria-label="`系统信息，${versionLabel}`">
            <div class="brand-mark">
              <NIcon :size="18"><LayersOutline /></NIcon>
            </div>
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
        :collapsed-width="64"
        :collapsed-icon-size="20"
        :icon-size="18"
        :indent="20"
        class="nav"
      />
    </NLayoutSider>
    <NLayout>
      <NLayoutHeader bordered class="header">
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
          <NBadge :value="runtime.pendingCount" :show="runtime.pendingCount > 0 || !!runtime.pendingError" :dot="!!runtime.pendingError && !runtime.pendingCount">
            <NButton :type="runtime.pendingCount ? 'warning' : 'default'" secondary size="small" @click="showPending = true">
              <template #icon><NIcon><GitPullRequestOutline /></NIcon></template>
              待应用变更
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
      <NLayoutContent :native-scrollbar="false" style="height: calc(100vh - 57px)">
        <div class="page">
          <div class="page-head">
            <h1 class="page-title">{{ title }}</h1>
            <p v-if="description" class="page-desc">{{ description }}</p>
          </div>
          <RouterView />
        </div>
      </NLayoutContent>
    </NLayout>
  </NLayout>
  <PendingDrawer v-model:show="showPending" />
</template>

<style scoped>
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 56px;
  padding: 0 18px;
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
  width: 30px;
  height: 30px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  background: linear-gradient(135deg, #36ad6a, #18a058 55%, #0c7a43);
  box-shadow: 0 2px 6px rgba(24, 160, 88, 0.35);
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
  font-size: 15px;
  letter-spacing: 0.2px;
}
.brand-sub {
  font-size: 12px;
  opacity: 0.55;
  overflow: hidden;
  text-overflow: ellipsis;
}
.nav {
  padding-bottom: 16px;
}
.header {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 20px;
}
.crumbs {
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
  padding: 20px 24px 32px;
}
.page-head {
  margin-bottom: 16px;
}
.page-title {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  line-height: 28px;
}
.page-desc {
  margin: 4px 0 0;
  max-width: 820px;
  font-size: 13px;
  line-height: 1.7;
  opacity: 0.65;
}
@media (max-width: 640px) {
  .page {
    padding: 16px 12px 24px;
  }
  .header {
    padding: 0 12px;
  }
}

@media (max-width: 767px) {
  .page :deep(.n-form) {
    max-width: none !important;
  }

  .page :deep(.n-form-item) {
    display: block;
  }

  .page :deep(.n-form-item-label) {
    width: auto !important;
    height: auto !important;
    padding: 0 0 4px !important;
  }

  .page :deep(.n-form-item-blank) {
    min-width: 0;
  }
}
</style>
