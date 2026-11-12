import { reactive } from 'vue'
import { api, type Schemas } from '../api/client'

/** 每日停服时段的当前状态：顶栏、总览和用户列表共用，由主布局定时刷新。 */
export const pauseWindow = reactive<{ status: Schemas['PauseWindowStatus'] | null }>({ status: null })

const INTERVAL = 30_000
let timer: number | undefined

export async function refreshPauseWindow() {
  const { data } = await api.GET('/api/pause-window')
  if (data) pauseWindow.status = data
}

export function startPauseWindow() {
  void refreshPauseWindow()
  window.clearInterval(timer)
  timer = window.setInterval(() => void refreshPauseWindow(), INTERVAL)
}

export function stopPauseWindow() {
  window.clearInterval(timer)
  timer = undefined
}
