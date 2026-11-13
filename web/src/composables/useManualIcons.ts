import { computed, ref } from 'vue'
import { api, getCSRFToken, handleUnauthorizedResponse, panelBase, problemMessage, type Schemas } from '../api/client'

export type IconKind = 'domain' | 'service'
export type ManualIcon = Schemas['ManualIcon']

/**
 * 手动设置的图标列表，全部页面共用。version 由列表算出（个数和最近的修改时间），放进图标地址：图标在浏览器里缓存一天，
 * 手动图标增删改后地址变化，浏览器重新请求。ready 表示列表已加载（成功或失败），图标在此之后才开始请求，避免版本号变化导致重复请求。
 */
const icons = ref<ManualIcon[]>([])
const ready = ref(false)
const version = computed(() => `${icons.value.length}-${icons.value.reduce((latest, icon) => (icon.updated_at > latest ? icon.updated_at : latest), '')}`)
let loading: Promise<void> | null = null

async function reload() {
  const { data } = await api.GET('/api/access/icons')
  if (data) icons.value = data
  ready.value = true
  return Boolean(data)
}

/** 第一次使用时加载列表；加载失败时下次使用再试。 */
function ensureLoaded() {
  loading ??= reload().then((ok) => {
    if (!ok) loading = null
  })
  return loading
}

/** 按服务端的规则近似规范化名称：小写、去掉末尾的点；主域名去掉 www.（更深的子域名由服务端归并）。 */
function key(kind: IconKind, name: string) {
  const value = name.trim().toLowerCase().replace(/\.$/, '')
  return kind === 'domain' ? value.replace(/^www\./, '') : value
}

async function changed() {
  await reload()
}

/** 上传图片作为手动图标，失败时返回错误信息。 */
async function upload(kind: IconKind, name: string, file: File): Promise<string | null> {
  const form = new FormData()
  form.append('kind', kind)
  form.append('name', name)
  form.append('file', file)
  const response = await fetch(`${panelBase}api/access/icons/upload`, {
    method: 'POST', body: form, headers: { 'X-CSRF-Token': getCSRFToken() }, credentials: 'same-origin',
  })
  if (handleUnauthorizedResponse(response)) return '登录已失效'
  if (!response.ok) {
    const body = await response.json().catch(() => null)
    return body?.errors?.[0]?.message ?? problemMessage(body, '上传失败')
  }
  await changed()
  return null
}

/** 让面板按下载代理下载图片地址作为手动图标，失败时返回错误信息。 */
async function fetchFromURL(kind: IconKind, name: string, url: string): Promise<string | null> {
  const { error } = await api.POST('/api/access/icons/fetch', { body: { kind, name, url } })
  if (error) return error.errors?.[0]?.message ?? problemMessage(error, '获取失败')
  await changed()
  return null
}

/** 删除手动图标，返回错误信息或 null。 */
async function remove(items: { kind: IconKind; name: string }[]): Promise<string | null> {
  const { error } = await api.POST('/api/access/icons/delete', { body: { items } })
  if (error) return problemMessage(error, '删除失败')
  await changed()
  return null
}

export function useManualIcons() {
  void ensureLoaded()
  return {
    icons,
    version,
    ready,
    reload: changed,
    has: (kind: IconKind, name: string) => icons.value.some((icon) => icon.kind === kind && icon.name === key(kind, name)),
    upload,
    fetchFromURL,
    remove,
  }
}
