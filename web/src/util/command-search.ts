import type { RouteLocationRaw } from 'vue-router'

/** 全局搜索的结果类别，也是同分时的排列顺序。 */
export type SearchKind = 'user' | 'stack' | 'page' | 'setting'

export const searchKindLabels: Record<SearchKind, string> = { user: '用户', stack: 'Stack', page: '页面', setting: '系统设置' }
const kindOrder: SearchKind[] = ['user', 'stack', 'page', 'setting']

export interface SearchEntry {
  /** 稳定标识，用于「最近访问」，例如 user:3、page:deps、setting:notify。 */
  id: string
  kind: SearchKind
  title: string
  subtitle?: string
  /** 标题以外参与匹配的文字，例如备注、标签、页面说明。 */
  keywords?: string[]
  to: RouteLocationRaw
}

export interface SearchGroup {
  kind: SearchKind
  label: string
  entries: SearchEntry[]
}

const normalize = (text: string) => text.normalize('NFKC').toLowerCase()
const boundary = /[\s\-_./·（(]/

/** 字符按顺序出现即可，用于 sbp 命中 sub-publish 这类缩写。 */
function isSubsequence(term: string, text: string) {
  let at = 0
  for (const char of text) {
    if (char === term[at]) at++
    if (at === term.length) return true
  }
  return false
}

/** 单个关键词的得分，0 表示未命中：标题完全相同 > 标题开头 > 标题中某个词的开头 > 标题包含 > 其他字段开头 > 其他字段包含 > 标题缩写。 */
function termScore(term: string, title: string, keywords: string[]) {
  if (title === term) return 100
  if (title.startsWith(term)) return 80
  const at = title.indexOf(term)
  if (at > 0 && boundary.test(title[at - 1]!)) return 70
  if (at > 0) return 60
  if (keywords.some((keyword) => keyword.startsWith(term))) return 40
  // 单个字符在长说明里几乎总能找到，只按开头匹配，避免结果被无关条目淹没。
  if (term.length >= 2 && keywords.some((keyword) => keyword.includes(term))) return 30
  if (term.length >= 2 && /^[a-z0-9]+$/.test(term) && isSubsequence(term, title)) return 10
  return 0
}

/** 按空格拆成多个关键词，全部命中才算匹配；得分为各关键词之和。 */
export function scoreEntry(entry: SearchEntry, query: string) {
  const terms = normalize(query).split(/\s+/).filter(Boolean)
  if (!terms.length) return 0
  const title = normalize(entry.title)
  const keywords = [entry.subtitle ?? '', ...(entry.keywords ?? [])].filter(Boolean).map(normalize)
  let total = 0
  for (const term of terms) {
    const score = termScore(term, title, keywords)
    if (!score) return 0
    total += score
  }
  return total
}

/**
 * 按类别分组的搜索结果：组内按得分、标题长度和标题排序，
 * 组之间按组内最高分排序，最相关的结果总在最上面；每组最多 limit 条。
 */
export function searchEntries(entries: SearchEntry[], query: string, limit = 8): SearchGroup[] {
  const scored = entries
    .map((entry) => ({ entry, score: scoreEntry(entry, query) }))
    .filter((item) => item.score > 0)
  const groups = kindOrder
    .map((kind) => {
      const items = scored
        .filter((item) => item.entry.kind === kind)
        .sort((a, b) => b.score - a.score || a.entry.title.length - b.entry.title.length || a.entry.title.localeCompare(b.entry.title))
      return { kind, best: items[0]?.score ?? 0, entries: items.slice(0, limit).map((item) => item.entry) }
    })
    .filter((group) => group.entries.length)
  return groups
    .sort((a, b) => b.best - a.best || kindOrder.indexOf(a.kind) - kindOrder.indexOf(b.kind))
    .map(({ kind, entries }) => ({ kind, label: searchKindLabels[kind], entries }))
}

/** 把「最近访问」里记下的标识换成当前仍存在的条目；已删除的用户或 stack 自动略过。 */
export function resolveRecent(ids: string[], entries: SearchEntry[]) {
  const byId = new Map(entries.map((entry) => [entry.id, entry]))
  return ids.map((id) => byId.get(id)).filter((entry): entry is SearchEntry => !!entry)
}

/** 新访问的条目排到最前，去重并截断。 */
export function pushRecent(ids: string[], id: string, max = 6) {
  return [id, ...ids.filter((item) => item !== id)].slice(0, max)
}
