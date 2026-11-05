import { readonly, ref } from 'vue'

/** 与样式表一致的窄屏断点。 */
const query = window.matchMedia('(max-width: 767px)')
const mobile = ref(query.matches)
query.addEventListener('change', (event) => (mobile.value = event.matches))

/** 当前是否为窄屏（手机宽度），随窗口尺寸变化更新。 */
export function useMobile() {
  return readonly(mobile)
}
