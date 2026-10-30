<script setup lang="ts">
import { ref } from 'vue'
import { NButton, NCard, NSpace, NText } from 'naive-ui'
import ExportDialog from '../components/ExportDialog.vue'
import ImportDialog from '../components/ImportDialog.vue'

const showExport = ref(false)
const showImport = ref(false)
</script>

<template>
  <NSpace vertical :size="12" style="max-width: 860px">
    <NCard title="导出全部配置" size="small">
      <NText depth="3" tag="p">
        导出系统设置（不含面板监听、TLS 和管理员）、全部用户和 stack，可在另一台实例导入。流量数据、审计日志和管理员账号不随配置导出。
      </NText>
      <NButton type="primary" @click="showExport = true">导出</NButton>
    </NCard>
    <NCard title="导入配置" size="small">
      <NText depth="3" tag="p">
        支持合并或替换全部。导入只写入配置并生成修订版本，不会自动应用；替换全部前会自动备份数据库。
      </NText>
      <NButton @click="showImport = true">导入</NButton>
    </NCard>
  </NSpace>
  <ExportDialog v-model:show="showExport" scope="all" />
  <ImportDialog v-model:show="showImport" scope="all" />
</template>
