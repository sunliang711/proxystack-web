import { computed, shallowReactive, watch } from 'vue'
import { api, type Schemas } from '../api/client'
import { session } from '../session'

export type EntityKind = 'stack' | 'user'
export interface EntityRecord { id?: number; name: string }
export interface EntityReference { kind: EntityKind; id?: number | string; name?: string }

const records = shallowReactive<Record<EntityKind, EntityRecord[]>>({ stack: [], user: [] })
let owner = session.admin
let pending: Partial<Record<EntityKind, Promise<void>>> = {}

export function findEntity(records: EntityRecord[], reference: EntityReference): EntityRecord | undefined {
  // 有稳定 ID 时不回退到同名对象，避免历史记录误指向后来新建的对象。
  if (reference.id !== undefined) {
    const id = Number(reference.id)
    return Number.isSafeInteger(id) && id > 0 ? records.find((record) => record.id === id) : undefined
  }
  return reference.name ? records.find((record) => record.name === reference.name && Number.isSafeInteger(record.id) && record.id! > 0) : undefined
}

async function refresh(kind: EntityKind) {
  if (owner !== session.admin) {
    owner = session.admin
    records.stack = []
    records.user = []
    pending = {}
  }
  if (!owner) return
  if (pending[kind]) return pending[kind]
  const currentOwner = owner
  // 同一批表格单元格共用请求；再次进入页面时更新目录，识别删除或改名。
  const request = (async () => {
    try {
      const result = kind === 'stack' ? await api.GET('/api/stacks') : await api.GET('/api/users')
      if (owner !== currentOwner) return
      if (!result.data) {
        records[kind] = []
        return
      }
      records[kind] = 'stacks' in result.data ? result.data.stacks : result.data.users
    } catch {
      if (owner === currentOwner) records[kind] = []
    }
  })()
  pending[kind] = request
  await request
  if (pending[kind] === request) delete pending[kind]
}

/** 列表接口返回的完整对象，供全局搜索按名称以外的字段匹配。 */
export const entityRecords = records as unknown as Readonly<{ stack: readonly Schemas['StackSummary'][]; user: readonly Schemas['User'][] }>

/** 重新读取目录；同一时刻的多次调用共用一个请求。 */
export function refreshEntities(kind: EntityKind) {
  return refresh(kind)
}

export function useEntityDirectory(kinds: () => EntityKind[]) {
  watch(() => [...new Set(kinds())].sort().join(','), (value) => {
    for (const kind of value.split(',').filter(Boolean) as EntityKind[]) refresh(kind)
  }, { immediate: true })
  return (reference: EntityReference) => findEntity(records[reference.kind], reference)
}

export function useEntityReference(reference: () => EntityReference) {
  const resolve = useEntityDirectory(() => [reference().kind])
  return computed(() => resolve(reference()))
}
