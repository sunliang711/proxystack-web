<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useEntityReference, type EntityKind } from '../composables/useEntityReference'

const props = defineProps<{ kind: EntityKind; id?: number | string; name?: string; label?: string; tab?: string }>()
const emit = defineEmits<{ navigate: [] }>()
const record = useEntityReference(() => props)
const label = computed(() => props.label ?? props.name ?? record.value?.name ?? `#${props.id}`)
const target = computed(() => record.value ? {
  name: props.kind, params: { id: record.value.id! }, ...(props.tab ? { query: { tab: props.tab } } : {}),
} : null)
</script>

<template>
  <RouterLink v-if="target" :to="target" class="entity-link" :title="`查看${kind === 'stack' ? ' Stack' : '用户'} ${record!.name} 的详情`" @click.stop="emit('navigate')"><slot>{{ label }}</slot></RouterLink>
  <span v-else><slot>{{ label }}</slot></span>
</template>

<style scoped>
.entity-link { color: var(--ps-accent); text-decoration: none; }
.entity-link:hover { text-decoration: underline; }
</style>
