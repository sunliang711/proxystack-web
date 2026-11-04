import { onBeforeUnmount, ref, watchEffect } from 'vue'

/** 详情页可以用实体名称覆盖路由里的通用标题；为 null 时使用路由标题。 */
export const pageTitleOverride = ref<string | null>(null)

export function usePageTitle(source: () => string | null | undefined) {
  const stop = watchEffect(() => {
    pageTitleOverride.value = source() || null
  })
  onBeforeUnmount(() => {
    stop()
    pageTitleOverride.value = null
  })
}
