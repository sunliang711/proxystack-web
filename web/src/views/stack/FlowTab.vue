<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { NText } from 'naive-ui'
import type { Schemas } from '../../api/client'
import FlowDiagram from '../../components/FlowDiagram.vue'
import { stackFlow, type FlowNode } from '../../util/flow'

const props = defineProps<{ stack: Schemas['Stack']; summaries: Schemas['StackSummary'][] }>()
const router = useRouter()

// 按编辑中的内容实时绘制，未保存的修改也会反映出来。
const graph = computed(() => stackFlow(props.stack, props.summaries))

function open(node: FlowNode) {
  if (node.stackId) router.push({ name: 'stack', params: { id: node.stackId } })
}
</script>

<template>
  <div class="flow-tab">
    <NText depth="3" class="intro">
      按当前编辑中的配置绘制（含未保存的修改）。流动的线表示流量方向；虚线是 select 组里未默认选中的备选成员；变淡的上游没有被代理组或规则引用，流量不会经过它。悬停节点可以突出与它相连的路径，点击其他 stack 可以跳转。
    </NText>
    <FlowDiagram :nodes="graph.nodes" :edges="graph.edges" :columns="graph.columns" @select="open" />
  </div>
</template>

<style scoped>
.intro {
  display: block;
  margin-bottom: 12px;
  font-size: 13px;
  line-height: 1.7;
}
</style>
