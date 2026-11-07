<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { NButton, NIcon, useDialog, type DialogReactive } from 'naive-ui'
import { TimeOutline } from '@vicons/ionicons5'
import { checkSession, session } from '../session'

// 会话快到期时在顶栏提醒：剩余 30 分钟内显示，剩余 5 分钟时再弹一次对话框，到期后由重新登录对话框接手。
const WARN = 30 * 60_000
const URGENT = 5 * 60_000

const dialog = useDialog()
const now = ref(Date.now())
const timer = window.setInterval(() => (now.value = Date.now()), 15_000)
onBeforeUnmount(() => window.clearInterval(timer))

/** 会话在闲置到期和绝对到期中较早的一个失效；绝对到期只能重新登录，闲置到期操作一下就会续期。 */
const deadline = computed(() => (session.expiresAt ? Math.min(session.expiresAt, session.maxExpiresAt) : 0))
const absolute = computed(() => session.maxExpiresAt <= session.expiresAt)
const remaining = computed(() => (deadline.value ? deadline.value - now.value : Infinity))
const visible = computed(() => !!session.admin && !session.reauth && remaining.value <= WARN)
const minutes = computed(() => {
  const value = Math.ceil(Math.max(0, remaining.value) / 60_000)
  return value <= 1 ? '不到 1 分钟' : `${value} 分钟`
})
const label = computed(() => (absolute.value ? `会话 ${minutes.value}后到期` : `${minutes.value}后因闲置退出`))
const reason = computed(() => (absolute.value ? '登录满 7 天后必须重新登录，未保存的修改不受影响。' : '24 小时没有操作会自动退出。'))
const hint = computed(() => `${reason.value}${absolute.value ? '点击现在重新登录。' : '点击继续使用。'}`)

function act() {
  if (absolute.value) session.reauth = 'renew'
  else void checkSession()
}

// 每个到期时间只弹一次紧急提醒、只做一次到期检查；续期或重新登录后到期时间变化，重新计算。
let urgentFor = 0
let expiredFor = 0
let urgent: DialogReactive | null = null
function closeUrgent() {
  urgent?.destroy()
  urgent = null
}
// 到期时间变化（续期或重新登录）或弹出重新登录对话框时，先前的提醒已不再准确。
watch(deadline, () => {
  now.value = Date.now()
  closeUrgent()
})
watch(() => session.reauth, (value) => { if (value) closeUrgent() })
onBeforeUnmount(closeUrgent)
watch(remaining, (value) => {
  if (!session.admin || session.reauth) return
  if (value <= 0 && expiredFor !== deadline.value) {
    expiredFor = deadline.value
    void checkSession()
  } else if (value <= URGENT && value > 0 && urgentFor !== deadline.value) {
    urgentFor = deadline.value
    urgent = dialog.warning({
      title: absolute.value ? '会话即将到期' : '即将因闲置退出',
      content: () => `${label.value}。${reason.value}`,
      positiveText: absolute.value ? '现在重新登录' : '继续使用',
      negativeText: '稍后',
      onPositiveClick: act,
      onAfterLeave: () => (urgent = null),
    })
  }
})
</script>

<template>
  <NButton v-if="visible" type="warning" secondary size="small" class="expiry-button" :title="hint" :aria-label="`${label}。${hint}`" @click="act">
    <template #icon><NIcon><TimeOutline /></NIcon></template>
    <span class="expiry-label">{{ label }}</span>
  </NButton>
</template>

<style scoped>
@media (max-width: 640px) {
  .expiry-label { display: none; }
  .expiry-button :deep(.n-button__icon) { margin: 0; }
}
</style>
