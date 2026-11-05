<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import { NButton, NCard, NIcon, NProgress, NTag, NText } from 'naive-ui'
import { CheckmarkCircle, EllipseOutline } from '@vicons/ionicons5'
import { api, type Schemas } from '../api/client'
import { runtime } from '../runtime'
import { hasTime, targetReady } from '../util/subPublish'

/** 初始化清单：按顺序列出首次使用需要完成的步骤，每一步由当前数据判定是否完成。 */

const DISMISSED_KEY = 'psw-setup-checklist'

interface Step {
  key: string
  title: string
  detail: string
  done: boolean
  optional?: boolean
  action: string
  to?: RouteLocationRaw
  onClick?: () => void
}

const settings = ref<Schemas['Settings'] | null>(null)
const deps = ref<Schemas['DepStatus'][]>([])
const userCount = ref(0)
const stacks = ref<Schemas['StackSummary'][]>([])
const targets = ref<Schemas['PublishTargetView'][]>([])
const loaded = ref(false)
const dismissed = ref(readDismissed())

function readDismissed(): boolean {
  try {
    return localStorage.getItem(DISMISSED_KEY) === 'hidden'
  } catch {
    return false
  }
}
function dismiss() {
  dismissed.value = true
  try {
    localStorage.setItem(DISMISSED_KEY, 'hidden')
  } catch {
    // 存不下时只在本次会话隐藏。
  }
}

onMounted(async () => {
  if (dismissed.value) return
  const [settingsResult, depsResult, usersResult, stacksResult, targetsResult] = await Promise.all([
    api.GET('/api/config/settings'),
    api.GET('/api/deps'),
    api.GET('/api/users'),
    api.GET('/api/stacks'),
    api.GET('/api/sub-publish/targets'),
  ])
  settings.value = settingsResult.data ?? null
  deps.value = depsResult.data?.components ?? []
  userCount.value = usersResult.data?.users.length ?? 0
  stacks.value = stacksResult.data?.stacks ?? []
  targets.value = targetsResult.data ?? []
  loaded.value = true
})

const installed = (component: string) => deps.value.some((dep) => dep.component === component && dep.installed)
const applied = computed(() => stacks.value.some((stack) => (runtime.stacks[stack.name]?.applied_revision ?? 0) > 0))
const readyTargets = computed(() => targets.value.filter((target) => target.enabled && targetReady(target)))
const published = computed(() => readyTargets.value.some((target) => target.last_sequence && hasTime(target.last_publish_at) && !target.last_publish_error))

const steps = computed<Step[]>(() => [
  {
    key: 'host',
    title: '设置对外地址',
    detail: '订阅节点默认连接的域名或 IP。',
    done: !!settings.value?.basic.external_host,
    action: '去系统设置',
    to: { name: 'settings', query: { section: 'basic' } },
  },
  {
    key: 'deps',
    title: '安装 xray 和 mihomo',
    detail: 'Stack 运行所需的程序，安装时会先校验再替换。',
    done: installed('xray') && installed('mihomo'),
    action: '去安装依赖',
    to: { name: 'deps' },
  },
  {
    key: 'users',
    title: '创建第一个用户',
    detail: 'Stack 的入站引用用户，用户的凭据和订阅随之生成。',
    done: userCount.value > 0,
    action: '创建用户',
    to: { name: 'users', query: { create: '1' } },
  },
  {
    key: 'stacks',
    title: '创建第一个 Stack',
    detail: '一组独立运行的 xray / mihomo 进程，即一个代理实例。',
    done: stacks.value.length > 0,
    action: '创建 Stack',
    to: { name: 'stacks', query: { create: '1' } },
  },
  {
    key: 'apply',
    title: '应用配置',
    detail: '保存只写入数据库；应用后才会生成配置并启动服务。',
    done: applied.value && !runtime.pendingCount,
    action: runtime.pendingCount ? '查看待应用变更' : '去 Stack 列表',
    ...(runtime.pendingCount ? { onClick: () => (runtime.pendingDrawer = true) } : { to: { name: 'stacks' } }),
  },
  {
    key: 'publish',
    title: '发布订阅包',
    detail: readyTargets.value.length ? '把订阅内容签名后发送到订阅端，用户才能拉取订阅。' : '先添加订阅端并完成部署和测试，再签发订阅包；也可以下载文件离线导入。',
    done: published.value,
    optional: true,
    action: readyTargets.value.length ? '去发布' : '配置订阅端',
    to: { name: readyTargets.value.length ? 'sub-publish' : 'sub-publish-settings' },
  },
])

const required = computed(() => steps.value.filter((step) => !step.optional))
const doneCount = computed(() => steps.value.filter((step) => step.done).length)
const current = computed(() => steps.value.find((step) => !step.done)?.key)
const visible = computed(() => loaded.value && !dismissed.value && steps.value.some((step) => !step.done))
const requiredDone = computed(() => required.value.every((step) => step.done))
</script>

<template>
  <NCard v-if="visible" size="small" class="setup-card">
    <template #header>
      <div class="setup-head">
        <span>初始化清单</span>
        <NText depth="3" class="setup-count">已完成 {{ doneCount }} / {{ steps.length }}</NText>
      </div>
    </template>
    <template #header-extra>
      <NButton text size="small" @click="dismiss">{{ requiredDone ? '完成，不再显示' : '隐藏' }}</NButton>
    </template>
    <NProgress type="line" :percentage="Math.round((doneCount / steps.length) * 100)" :show-indicator="false" :height="4" style="margin-bottom: 8px" />
    <ol class="setup-steps">
      <li v-for="(step, index) in steps" :key="step.key" :class="{ done: step.done, current: current === step.key }">
        <NIcon :size="20" class="setup-icon" :aria-label="step.done ? '已完成' : '未完成'">
          <CheckmarkCircle v-if="step.done" />
          <EllipseOutline v-else />
        </NIcon>
        <div class="setup-text">
          <div class="setup-title">
            {{ index + 1 }}. {{ step.title }}
            <NTag v-if="step.optional" size="tiny" :bordered="false">按需</NTag>
          </div>
          <NText depth="3" class="setup-detail">{{ step.detail }}</NText>
        </div>
        <template v-if="!step.done">
          <NButton v-if="step.onClick" size="small" :type="current === step.key ? 'primary' : 'default'" @click="step.onClick">{{ step.action }}</NButton>
          <RouterLink v-else :to="step.to!" custom v-slot="{ navigate }">
            <NButton size="small" :type="current === step.key ? 'primary' : 'default'" @click="navigate">{{ step.action }}</NButton>
          </RouterLink>
        </template>
      </li>
    </ol>
  </NCard>
</template>

<style scoped>
.setup-head { display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap; }
.setup-count { font-size: 13px; font-weight: 400; }
.setup-steps { list-style: none; margin: 0; padding: 0; }
.setup-steps li {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 0;
  border-top: 1px solid var(--ps-border);
}
.setup-steps li:first-child { border-top: 0; }
.setup-icon { flex: none; color: var(--ps-muted); }
.done .setup-icon { color: var(--ps-accent); }
.done .setup-title { color: var(--ps-muted); }
.current .setup-title { font-weight: 600; }
.setup-text { flex: 1; min-width: 0; }
.setup-title { display: flex; align-items: center; gap: 6px; }
.setup-detail { display: block; font-size: 12px; }
</style>
