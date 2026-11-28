<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NCard, NEmpty, NSpace, NSpin, NTag, NText, useDialog, useMessage } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { clearSession, session } from '../session'
import { formatTime } from '../util/format'

const router = useRouter()
const dialog = useDialog()
const message = useMessage()
const sessions = ref<Schemas['ActiveSessionView'][]>([])
const loading = ref(false)
const revoking = ref('')
const error = ref('')

async function load() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const { data, error: problem } = await api.GET('/api/auth/sessions')
    if (data) sessions.value = data.sessions
    else error.value = problemMessage(problem, '加载活跃会话失败')
  } catch {
    error.value = '加载活跃会话失败，请检查网络后重试'
  } finally {
    loading.value = false
  }
}

async function revoke(item: Schemas['ActiveSessionView']) {
  if (revoking.value) return false
  revoking.value = item.id
  error.value = ''
  try {
    const { response, error: problem } = await api.DELETE('/api/auth/sessions/{id}', { params: { path: { id: item.id } } })
    if (!response.ok) {
      error.value = problemMessage(problem, '注销会话失败')
      return false
    }
    message.success('会话已注销')
    if (item.current) {
      clearSession()
      await router.replace({ name: 'login' })
    } else {
      await load()
    }
    return true
  } catch {
    error.value = '注销会话失败，请检查网络后重试'
    return false
  } finally {
    revoking.value = ''
  }
}

function confirmRevoke(item: Schemas['ActiveSessionView']) {
  if (!item.current) {
    void revoke(item)
    return
  }
  dialog.warning({
    title: '注销当前会话',
    content: '注销当前会话将立即退出登录，确认继续吗？',
    positiveText: '确认注销',
    negativeText: '取消',
    onPositiveClick: () => revoke(item),
  })
}

onMounted(load)
// 开启两步验证会撤销其他设备的会话；重新登录后也需要刷新列表。
watch(() => [session.admin?.totp_enabled, session.reauth], () => {
  if (session.admin && !session.reauth) void load()
})
</script>

<template>
  <NCard id="active-sessions" title="活跃会话" size="small">
    <template #header-extra>
      <NButton size="small" :loading="loading" :disabled="!!revoking" @click="load">刷新</NButton>
    </template>
    <NText depth="3" tag="p" style="margin-top: 0">查看当前账号在各设备上的登录，发现不认识的来源可立即注销。最后活跃时间仅随实际操作更新。</NText>
    <NAlert v-if="error" type="error" :bordered="false" style="margin-bottom: 12px">{{ error }}</NAlert>
    <NSpin :show="loading">
      <NSpace v-if="sessions.length" vertical :size="10">
        <div v-for="item in sessions" :key="item.id" class="session-row">
          <div class="session-info">
            <NSpace align="center" :size="8">
              <NText strong code>{{ item.client_ip || '未知来源' }}</NText>
              <NTag v-if="item.current" type="success" size="small" :bordered="false">当前会话</NTag>
            </NSpace>
            <NText depth="3" tag="div" class="session-agent">{{ item.user_agent || '未知客户端' }}</NText>
            <NText depth="3" tag="div" class="session-times">最后活跃：{{ formatTime(item.last_seen_at) }} · 过期：{{ formatTime(item.expires_at) }}</NText>
          </div>
          <NButton size="small" :loading="revoking === item.id" :disabled="!!revoking || loading" @click="confirmRevoke(item)">注销</NButton>
        </div>
      </NSpace>
      <NEmpty v-else-if="!loading && !error" description="无活跃会话" />
    </NSpin>
  </NCard>
</template>

<style scoped>
.session-row { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 12px; border: 1px solid var(--ps-border); border-radius: 8px; }
.session-info { min-width: 0; }
.session-agent { overflow-wrap: anywhere; font-size: 12px; margin-top: 4px; }
.session-times { font-size: 12px; margin-top: 4px; }
.session-row > :deep(.n-button) { flex-shrink: 0; }
</style>
