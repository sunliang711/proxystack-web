import type { Schemas } from '../api/client'

export type UserTab = 'basic' | 'subscription' | 'credentials' | 'access' | 'usage'

/**
 * 用户详情页的标签页及各自包含的需要保存的字段。
 * 启用开关和逐 stack 暂停当场生效，不计入未保存修改；接入、用量两页没有需要保存的字段。
 */
export const userTabs: { name: UserTab; label: string; fields: (keyof Schemas['User'])[] }[] = [
  { name: 'basic', label: '基本', fields: ['name', 'remark', 'note', 'quota_bytes', 'reset_mode', 'reset_day', 'daily_limit_bytes', 'daily_limit_action', 'expire_at'] },
  { name: 'subscription', label: '订阅', fields: ['sub_token', 'display_template'] },
  { name: 'credentials', label: '凭据', fields: ['uuid', 'ss_password', 'tag', 'profiles'] },
  { name: 'access', label: '接入', fields: [] },
  { name: 'usage', label: '用量', fields: [] },
]

/** 校验错误的字段路径（例如 profiles[0].uuid）所在的标签页；未知字段归入「基本」。 */
export function userTabFor(path: string): UserTab {
  const key = path.split(/[.[]/)[0]
  return userTabs.find((tab) => tab.fields.includes(key as keyof Schemas['User']))?.name ?? 'basic'
}
