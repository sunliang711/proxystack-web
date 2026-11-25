<script setup lang="ts">
import { computed, ref } from 'vue'
import { NButton, NDropdown, NIcon, NInput, NRadioButton, NRadioGroup, NSpace, NText, type DropdownOption } from 'naive-ui'
import { SearchOutline } from '@vicons/ionicons5'

export interface UserPickerOption {
  name: string
  remark?: string
  profiles?: { name: string }[]
}

type FilterMode = 'all' | 'selected' | 'unselected'

const DEFAULT_PROFILE_KEY = '\0default'

const props = withDefaults(defineProps<{
  users: UserPickerOption[]
  selectedProfiles?: Record<string, string>
  emptyText?: string
  selectAllLabel?: string
  clearAllLabel?: string
  onSelectAll?: () => void
  onClearAll?: () => void
}>(), {
  emptyText: '暂无用户',
  selectAllLabel: '添加全部',
  clearAllLabel: '删除全部',
})

const value = defineModel<string[]>('value', { required: true })
const emit = defineEmits<{
  'update:profile': [user: string, profile: string]
}>()

const query = ref('')
const filter = ref<FilterMode>('all')

const selectedSet = computed(() => new Set(value.value))
const profileEnabled = computed(() => props.selectedProfiles !== undefined)

/** 已知用户按原顺序展示，同时保留草稿中可能残留的未知已选用户，方便取消勾选。 */
const allItems = computed<UserPickerOption[]>(() => {
  const known = new Set(props.users.map((user) => user.name))
  const extras: UserPickerOption[] = []
  for (const name of selectedSet.value) {
    if (!known.has(name)) extras.push({ name })
  }
  return [...props.users, ...extras]
})

const normalizedQuery = computed(() => query.value.trim().toLowerCase())
const hasQuery = computed(() => normalizedQuery.value.length > 0)
const isFiltered = computed(() => hasQuery.value || filter.value !== 'all')

function currentProfile(name: string): string {
  return props.selectedProfiles?.[name] ?? ''
}

function showProfilePicker(item: UserPickerOption): boolean {
  if (!profileEnabled.value || !selectedSet.value.has(item.name)) return false
  return (item.profiles?.length ?? 0) > 0 || currentProfile(item.name) !== ''
}

function profileLabel(name: string): string {
  return currentProfile(name) || 'default'
}

function profileMenuOptions(item: UserPickerOption): DropdownOption[] {
  const current = currentProfile(item.name)
  const names = new Set((item.profiles ?? []).map((profile) => profile.name).filter(Boolean))
  if (current) names.add(current)
  return [
    { key: DEFAULT_PROFILE_KEY, label: current === '' ? '✓ default' : 'default' },
    ...[...names].map((name) => ({
      key: name,
      label: current === name ? `✓ ${name}` : name,
    })),
  ]
}

function selectProfile(user: string, key: string | number) {
  emit('update:profile', user, key === DEFAULT_PROFILE_KEY ? '' : String(key))
}

const visibleItems = computed(() => {
  const keyword = normalizedQuery.value
  return allItems.value.filter((item) => {
    const selected = selectedSet.value.has(item.name)
    if (filter.value === 'selected' && !selected) return false
    if (filter.value === 'unselected' && selected) return false
    if (!keyword) return true
    if (item.name.toLowerCase().includes(keyword) || (item.remark ?? '').toLowerCase().includes(keyword)) return true
    if (!profileEnabled.value) return false
    if (currentProfile(item.name).toLowerCase().includes(keyword)) return true
    return (item.profiles ?? []).some((profile) => profile.name.toLowerCase().includes(keyword))
  })
})

const selectedCount = computed(() => selectedSet.value.size)
const knownCount = computed(() => props.users.length)
const missingUsers = computed(() => props.users.filter((user) => !selectedSet.value.has(user.name)))
const visibleMissing = computed(() => visibleItems.value.filter((item) => !selectedSet.value.has(item.name)))
const visibleSelected = computed(() => visibleItems.value.filter((item) => selectedSet.value.has(item.name)))

const emptyMessage = computed(() => {
  if (!allItems.value.length) return props.emptyText
  if (hasQuery.value) return '没有匹配的用户'
  if (filter.value === 'selected') return '尚未选择任何用户'
  if (filter.value === 'unselected') return '已全部选择'
  return props.emptyText
})

function toggle(name: string) {
  if (selectedSet.value.has(name)) {
    value.value = value.value.filter((item) => item !== name)
  } else {
    value.value = [...value.value, name]
  }
}

function selectVisible() {
  if (!visibleMissing.value.length) return
  value.value = [...value.value, ...visibleMissing.value.map((item) => item.name)]
}

function clearVisible() {
  if (!visibleSelected.value.length) return
  const drop = new Set(visibleSelected.value.map((item) => item.name))
  value.value = value.value.filter((name) => !drop.has(name))
}

function selectAll() {
  if (!missingUsers.value.length) return
  if (props.onSelectAll) {
    props.onSelectAll()
    return
  }
  value.value = [...value.value, ...missingUsers.value.map((user) => user.name)]
}

function clearAll() {
  if (!selectedCount.value) return
  if (props.onClearAll) {
    props.onClearAll()
    return
  }
  value.value = []
}
</script>

<template>
  <div class="user-picker">
    <div class="picker-toolbar">
      <NInput
        v-model:value="query"
        size="small"
        clearable
        :placeholder="profileEnabled ? '搜索用户名、备注或档案' : '搜索用户名或备注'"
        class="picker-search"
        :input-props="{ 'aria-label': '搜索用户' }"
      >
        <template #prefix><NIcon><SearchOutline /></NIcon></template>
      </NInput>
      <NRadioGroup v-model:value="filter" size="small" class="picker-filter">
        <NRadioButton value="all">全部</NRadioButton>
        <NRadioButton value="selected">已选</NRadioButton>
        <NRadioButton value="unselected">未选</NRadioButton>
      </NRadioGroup>
    </div>

    <div v-if="visibleItems.length" class="picker-grid" role="group" aria-label="用户列表">
      <div
        v-for="item in visibleItems"
        :key="item.name"
        class="picker-item"
        :class="{ selected: selectedSet.has(item.name) }"
      >
        <button
          type="button"
          role="checkbox"
          :aria-checked="selectedSet.has(item.name)"
          class="picker-toggle"
          :title="item.remark ? `${item.name}（${item.remark}）` : item.name"
          @click="toggle(item.name)"
        >
          <span class="picker-check" aria-hidden="true">{{ selectedSet.has(item.name) ? '✓' : '' }}</span>
          <span class="picker-label">
            <span class="picker-name">{{ item.name }}</span>
            <span v-if="item.remark" class="picker-remark">{{ item.remark }}</span>
          </span>
        </button>
        <NDropdown
          v-if="showProfilePicker(item)"
          trigger="click"
          size="small"
          placement="bottom-end"
          :options="profileMenuOptions(item)"
          @select="(key: string | number) => selectProfile(item.name, key)"
        >
          <button
            type="button"
            class="picker-profile"
            :class="{ custom: !!currentProfile(item.name) }"
            :title="`切换档案（当前：${profileLabel(item.name)}）`"
            :aria-label="`切换 ${item.name} 的档案`"
          >
            <span class="picker-profile-text">{{ profileLabel(item.name) }}</span>
            <span class="picker-caret" aria-hidden="true">▾</span>
          </button>
        </NDropdown>
      </div>
    </div>
    <div v-else class="picker-empty">
      <NText depth="3">{{ emptyMessage }}</NText>
    </div>

    <div class="picker-footer">
      <NText depth="3" class="picker-summary">
        已选 {{ selectedCount }} / 共 {{ knownCount }}<template v-if="isFiltered"> · 显示 {{ visibleItems.length }}</template>
      </NText>
      <NSpace align="center" :size="6">
        <template v-if="hasQuery">
          <NButton size="tiny" secondary :disabled="!visibleMissing.length" @click="selectVisible">全选匹配</NButton>
          <NButton size="tiny" secondary :disabled="!visibleSelected.length" @click="clearVisible">取消匹配</NButton>
        </template>
        <NButton size="tiny" secondary :disabled="!missingUsers.length" @click="selectAll">
          {{ !knownCount ? emptyText : missingUsers.length ? selectAllLabel : '已全部添加' }}
        </NButton>
        <NButton size="tiny" secondary :disabled="!selectedCount" @click="clearAll">{{ clearAllLabel }}</NButton>
      </NSpace>
    </div>
  </div>
</template>

<style scoped>
.user-picker {
  width: 100%;
  border: 1px solid var(--ps-border);
  border-radius: 8px;
  background: var(--ps-surface-raised);
  padding: 8px;
}
.picker-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 8px;
}
.picker-search {
  flex: 1 1 160px;
  min-width: 0;
}
.picker-filter {
  flex-shrink: 0;
}
.picker-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(132px, 1fr));
  gap: 6px;
  max-height: 240px;
  overflow-y: auto;
  padding-right: 2px;
}
.picker-item {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  width: 100%;
  padding: 2px 4px 2px 2px;
  border: 1px solid var(--ps-border);
  border-radius: 6px;
  background: var(--ps-surface);
  color: var(--ps-text);
  transition: border-color 0.15s ease, background-color 0.15s ease;
}
.picker-item:hover {
  border-color: var(--ps-accent);
}
.picker-item.selected {
  border-color: var(--ps-accent);
  background: var(--ps-accent-soft);
}
.picker-toggle {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
  padding: 4px 4px 4px 6px;
  border: none;
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: 13px;
  line-height: 1.3;
  text-align: left;
  cursor: pointer;
}
.picker-check {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 15px;
  border: 1px solid var(--ps-border);
  border-radius: 4px;
  background: var(--ps-surface);
  font-size: 11px;
  font-weight: 700;
  line-height: 1;
}
.picker-item.selected .picker-check {
  border-color: var(--ps-accent);
  background: var(--ps-accent);
  color: var(--ps-bg);
}
.picker-label {
  display: flex;
  align-items: baseline;
  gap: 4px;
  min-width: 0;
  flex: 1;
  overflow: hidden;
}
.picker-name {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 500;
}
.picker-remark {
  flex: 0 2 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--ps-muted);
}
.picker-profile {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 2px;
  max-width: 68px;
  padding: 2px 5px;
  border: 1px solid var(--ps-border);
  border-radius: 4px;
  background: var(--ps-surface);
  color: var(--ps-muted);
  font: inherit;
  font-size: 11px;
  line-height: 1.2;
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease, background-color 0.15s ease;
}
.picker-profile:hover {
  border-color: var(--ps-accent);
  color: var(--ps-text);
}
.picker-profile.custom {
  border-color: var(--ps-accent);
  background: var(--ps-accent);
  color: var(--ps-bg);
  font-weight: 600;
}
.picker-profile-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.picker-caret {
  flex-shrink: 0;
  font-size: 10px;
  opacity: 0.8;
}
.picker-empty {
  padding: 18px 8px;
  text-align: center;
  font-size: 13px;
}
.picker-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--ps-border);
}
.picker-summary {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
</style>
