<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { NAlert, NButton, NGrid, NGridItem, NSpace, NSpin, NText } from 'naive-ui'
import { api } from '../../api/client'
import CodeEditor from '../../components/CodeEditor.vue'

const props = defineProps<{ stackId: number; dirty: boolean; version: number }>()
const xray = ref('')
const mihomo = ref('')
const errors = ref<string[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  const { data } = await api.GET('/api/stacks/{id}/preview', { params: { path: { id: props.stackId } } })
  loading.value = false
  xray.value = data?.xray ?? ''
  mihomo.value = data?.mihomo ?? ''
  errors.value = data?.errors ?? []
}
onMounted(load)
watch(() => [props.stackId, props.version], load)
</script>

<template>
  <NSpin :show="loading">
    <NSpace justify="space-between" align="center" style="margin-bottom: 16px">
      <NText depth="3">预览按已保存的配置生成{{ dirty ? '，当前有未保存的修改' : '' }}。</NText>
      <NButton size="small" @click="load">刷新</NButton>
    </NSpace>
    <NAlert v-if="errors.length" type="error" style="margin-bottom: 16px">
      <div v-for="(e, i) in errors" :key="i">{{ e }}</div>
    </NAlert>
    <NGrid :cols="2" :x-gap="24" :y-gap="24" responsive="screen" item-responsive>
      <NGridItem span="2 l:1">
        <NText strong tag="div" class="section-label">xray JSON</NText>
        <CodeEditor v-if="xray" :value="xray" language="json" readonly min-height="400px" />
        <NText v-else depth="3" tag="div">Xray 未启用</NText>
      </NGridItem>
      <NGridItem span="2 l:1">
        <NText strong tag="div" class="section-label">mihomo YAML</NText>
        <CodeEditor v-if="mihomo" :value="mihomo" language="yaml" readonly min-height="400px" />
        <NText v-else depth="3" tag="div">Mihomo 未启用</NText>
      </NGridItem>
    </NGrid>
  </NSpin>
</template>
