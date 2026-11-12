<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NIcon, NModal } from 'naive-ui'
import { SearchOutline } from '@vicons/ionicons5'
import { entityRecords, refreshEntities } from '../composables/useEntityReference'
import { sectionSearch, settingsSections, tabOfSection, settingsTabs } from '../composables/useSettingsSections'
import { extraDestinations, navGroups } from '../navigation'
import { pushRecent, resolveRecent, searchEntries, searchKindLabels, type SearchEntry } from '../util/command-search'

/** 全局搜索：按名称跳转到用户、Stack、页面和系统设置分组。 */
const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [value: boolean] }>()
const router = useRouter()

const query = ref('')
const active = ref(0)
const input = ref<HTMLInputElement | null>(null)
const list = ref<HTMLElement | null>(null)

const RECENT_KEY = 'psw-search-recent'
function loadRecent(): string[] {
  try {
    const value = JSON.parse(localStorage.getItem(RECENT_KEY) ?? '[]')
    return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}
const recent = ref<string[]>(loadRecent())
function remember(id: string) {
  recent.value = pushRecent(recent.value, id)
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(recent.value))
  } catch {
    // 存不下时只在本次会话记住。
  }
}

const pageEntries = computed<SearchEntry[]>(() => {
  const meta = new Map(router.getRoutes().map((route) => [route.name, route.meta]))
  const pages = navGroups.flatMap((group) => group.items.map((item): SearchEntry => ({
    id: `page:${item.name}`,
    kind: 'page',
    title: item.label,
    subtitle: group.label,
    keywords: [meta.get(item.name)?.description ?? ''],
    to: { name: item.name },
  })))
  const extras = extraDestinations.map((item): SearchEntry => ({
    id: `page:${item.key}`,
    kind: 'page',
    title: item.label,
    subtitle: item.group,
    keywords: item.keywords,
    to: item.to,
  }))
  return [...pages, ...extras]
})

const entries = computed<SearchEntry[]>(() => {
  const users = entityRecords.user.filter((user) => user.id).map((user): SearchEntry => ({
    id: `user:${user.id}`,
    kind: 'user',
    title: user.name,
    subtitle: [user.remark, user.enabled ? '' : '已停用'].filter(Boolean).join(' · '),
    to: { name: 'user', params: { id: user.id! } },
  }))
  const stacks = entityRecords.stack.map((stack): SearchEntry => ({
    id: `stack:${stack.id}`,
    kind: 'stack',
    title: stack.name,
    subtitle: [stack.role === 'auto' ? 'auto' : '', ...(stack.labels ?? []), stack.enabled ? '' : '已停用'].filter(Boolean).join(' · '),
    to: { name: 'stack', params: { id: stack.id } },
  }))
  // 每个分组一条，分组内每个字段再各一条，字段结果标明所在分组。
  const settings = settingsSections.flatMap((name): SearchEntry[] => {
    const { title, fields, keywords } = sectionSearch[name]
    const tabLabel = settingsTabs.find((t) => t.name === tabOfSection(name))!.label
    const to = { name: 'settings', query: { section: name } }
    return [
      { id: `setting:${name}`, kind: 'setting', title, subtitle: `系统设置 · ${tabLabel}`, keywords: [...fields, ...keywords], to },
      ...fields.map((field): SearchEntry => ({ id: `setting:${name}:${field}`, kind: 'setting', title: field, subtitle: `系统设置 · ${tabLabel} · ${title}`, to })),
    ]
  })
  return [...users, ...stacks, ...pageEntries.value, ...settings]
})

/** 未输入时显示最近访问和全部页面；输入后按类别分组显示匹配结果。 */
const groups = computed<{ key: string; label: string; entries: SearchEntry[] }[]>(() => {
  if (query.value.trim()) return searchEntries(entries.value, query.value).map((group) => ({ key: group.kind, ...group }))
  const recentEntries = resolveRecent(recent.value, entries.value)
  return [
    ...(recentEntries.length ? [{ key: 'recent', label: '最近访问', entries: recentEntries }] : []),
    { key: 'pages', label: '页面', entries: pageEntries.value.filter((entry) => !recentEntries.includes(entry)) },
  ]
})
const flat = computed(() => groups.value.flatMap((group) => group.entries))
const indexOf = (entry: SearchEntry) => flat.value.indexOf(entry)

watch(query, () => { active.value = 0 })
watch(() => props.show, async (show) => {
  if (!show) return
  query.value = ''
  active.value = 0
  recent.value = loadRecent()
  // 每次打开时重新读取目录，识别其他标签页或其他管理员新建、删除和改名的对象。
  void refreshEntities('user')
  void refreshEntities('stack')
  await nextTick()
  input.value?.focus()
})

function move(delta: number) {
  const count = flat.value.length
  if (!count) return
  active.value = (active.value + delta + count) % count
  void nextTick(() => list.value?.querySelector(`[data-index="${active.value}"]`)?.scrollIntoView({ block: 'nearest' }))
}

function open(entry: SearchEntry | undefined) {
  if (!entry) return
  remember(entry.id)
  emit('update:show', false)
  // 目标页面有未保存修改时，离开确认可能取消这次跳转。
  void router.push(entry.to).catch(() => undefined)
}

function onKeydown(event: KeyboardEvent) {
  // 输入法组字期间的回车和方向键交给输入法。
  if (event.isComposing || event.keyCode === 229) return
  const ctrlOnly = event.ctrlKey && !event.metaKey && !event.altKey
  if (event.key === 'ArrowDown' || (ctrlOnly && event.key === 'n')) {
    event.preventDefault()
    move(1)
  } else if (event.key === 'ArrowUp' || (ctrlOnly && event.key === 'p')) {
    event.preventDefault()
    move(-1)
  } else if (event.key === 'Enter') {
    event.preventDefault()
    open(flat.value[active.value])
  }
}
</script>

<template>
  <NModal :show="show" :auto-focus="false" @update:show="(value: boolean) => emit('update:show', value)">
    <div class="palette" role="dialog" aria-modal="true" aria-label="全局搜索">
      <div class="search-row">
        <NIcon :size="18" class="search-icon"><SearchOutline /></NIcon>
        <input
          ref="input"
          v-model="query"
          class="search-input"
          type="text"
          placeholder="搜索用户、Stack、页面或设置项"
          autocomplete="off"
          spellcheck="false"
          role="combobox"
          aria-expanded="true"
          aria-controls="command-palette-list"
          :aria-activedescendant="flat.length ? `command-palette-option-${active}` : undefined"
          @keydown="onKeydown"
        />
        <kbd class="esc">esc</kbd>
      </div>
      <div id="command-palette-list" ref="list" class="results" role="listbox" aria-label="搜索结果">
        <template v-for="group in groups" :key="group.key">
          <div class="group-label" role="presentation">{{ group.label }}</div>
          <div
            v-for="entry in group.entries"
            :id="`command-palette-option-${indexOf(entry)}`"
            :key="entry.id"
            class="option"
            :class="{ active: indexOf(entry) === active }"
            role="option"
            :aria-selected="indexOf(entry) === active"
            :data-index="indexOf(entry)"
            @mousemove="active = indexOf(entry)"
            @click="open(entry)"
          >
            <span class="option-title">{{ entry.title }}</span>
            <span v-if="entry.subtitle" class="option-subtitle">{{ entry.subtitle }}</span>
            <!-- 最近访问混合了各类条目，标出类别；搜索结果已按类别分组。 -->
            <span v-if="group.key === 'recent'" class="option-kind">{{ searchKindLabels[entry.kind] }}</span>
            <span class="option-enter" aria-hidden="true">↵</span>
          </div>
        </template>
        <div v-if="!flat.length" class="empty">没有匹配的用户、Stack、页面或设置项</div>
      </div>
      <div class="footer" aria-hidden="true">
        <span><kbd>↑</kbd><kbd>↓</kbd> 选择</span>
        <span><kbd>↵</kbd> 打开</span>
        <span><kbd>esc</kbd> 关闭</span>
      </div>
    </div>
  </NModal>
</template>

<style scoped>
.palette {
  width: min(640px, calc(100vw - 32px));
  margin: 12vh auto auto;
  display: flex;
  flex-direction: column;
  max-height: min(560px, 76vh);
  background: var(--ps-surface);
  border: 1px solid var(--ps-border);
  border-radius: 12px;
  box-shadow: 0 16px 48px rgba(0, 0, 0, 0.28);
  color: var(--ps-text);
  overflow: hidden;
}
.search-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 16px;
  border-bottom: 1px solid var(--ps-border);
}
.search-icon { color: var(--ps-muted); flex: none; }
.search-input {
  flex: 1;
  min-width: 0;
  height: 52px;
  border: 0;
  outline: none;
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: 15px;
}
.search-input::placeholder { color: var(--ps-muted); }
.results {
  flex: 1;
  overflow-y: auto;
  padding: 6px 8px 8px;
}
.group-label {
  padding: 10px 10px 4px;
  font-size: 11px;
  letter-spacing: 1px;
  color: var(--ps-muted);
}
.option {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 9px 10px;
  border-radius: 8px;
  cursor: pointer;
  min-width: 0;
}
.option.active { background: var(--ps-accent-soft); }
.option.active .option-title { color: var(--ps-accent); }
.option-title { flex: none; max-width: 60%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 14px; }
.option-subtitle { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; color: var(--ps-muted); }
.option-kind { flex: none; margin-left: auto; font-size: 11px; color: var(--ps-muted); }
.option-kind + .option-enter { margin-left: 0; }
.option-enter { flex: none; margin-left: auto; font-size: 12px; color: var(--ps-muted); visibility: hidden; }
.option.active .option-enter { visibility: visible; }
.empty { padding: 28px 10px; text-align: center; font-size: 13px; color: var(--ps-muted); }
.footer {
  display: flex;
  gap: 16px;
  padding: 8px 16px;
  border-top: 1px solid var(--ps-border);
  font-size: 12px;
  color: var(--ps-muted);
}
kbd {
  display: inline-block;
  min-width: 18px;
  margin-right: 3px;
  padding: 0 4px;
  border: 1px solid var(--ps-border);
  border-radius: 4px;
  font: inherit;
  font-size: 11px;
  line-height: 16px;
  text-align: center;
  background: var(--ps-surface-raised);
}
.esc { flex: none; margin: 0; }
@media (max-width: 640px) {
  .palette { margin-top: 8vh; }
  .footer { display: none; }
}
</style>
