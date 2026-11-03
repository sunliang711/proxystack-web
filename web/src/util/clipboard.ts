// Clipboard API 在 HTTP 页面或浏览器策略限制下可能存在但调用失败，失败后使用用户手势下的传统复制接口。
export async function copyText(text: string): Promise<void> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return
    }
  } catch {
    // Fall through to the legacy copy path.
  }

  const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const input = document.createElement('textarea')
  input.value = text
  input.readOnly = true
  input.style.cssText = 'position: fixed; left: -9999px; top: 0; opacity: 0; pointer-events: none'
  ;(focused?.closest('[role="dialog"]') ?? document.body).appendChild(input)
  try {
    input.select()
    if (!document.execCommand('copy')) throw new Error('Copy failed')
  } finally {
    input.remove()
    focused?.focus({ preventScroll: true })
  }
}
