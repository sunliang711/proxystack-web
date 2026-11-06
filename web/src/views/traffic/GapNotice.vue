<script setup lang="ts">
import { computed } from 'vue'
import { NAlert, NCollapse, NCollapseItem, NText } from 'naive-ui'
import type { Schemas } from '../../api/client'
import { formatTime } from '../../util/format'
import { gapKindLabel, gapSummary } from '../../util/traffic-stats'

const props = defineProps<{ gaps: Schemas['TrafficGap'][]; scope?: string }>()
const summary = computed(() => gapSummary(props.gaps))
const lost = computed(() => props.gaps.some((gap) => gap.kind === 'lost'))
</script>

<template>
  <NAlert v-if="gaps.length" :type="lost ? 'warning' : 'info'" :bordered="false" class="gap-notice">
    {{ scope ?? '该范围内' }}有 {{ summary.count }} 段数据不完整，共约 {{ summary.minutes }} 分钟<template v-if="lost">，其中有流量未能采集，数字可能偏少</template><template v-else>，这些时段的流量计入了恢复采集的时刻</template>。
    <NCollapse class="gap-list">
      <NCollapseItem title="查看明细" name="gaps">
        <ul>
          <li v-for="(gap, index) in gaps" :key="index">
            <NText strong>{{ gap.stack }}</NText>
            · {{ gapKindLabel(gap.kind) }}
            · {{ formatTime(gap.from) }} ～ {{ gap.open ? '至今' : formatTime(gap.to) }}
            <NText depth="3">· {{ gap.reason }}</NText>
          </li>
        </ul>
      </NCollapseItem>
    </NCollapse>
  </NAlert>
</template>

<style scoped>
.gap-list { margin-top: 6px; }
.gap-list ul { margin: 0; padding-left: 18px; font-size: 13px; line-height: 1.8; }
</style>
