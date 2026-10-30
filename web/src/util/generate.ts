const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'

function randomBytes(n: number): Uint8Array {
  const bytes = new Uint8Array(n)
  crypto.getRandomValues(bytes)
  return bytes
}

export function randomString(length = 24): string {
  const out: string[] = []
  while (out.length < length) {
    for (const b of randomBytes(length)) {
      // 拒绝采样，避免取模偏差。
      if (b < 256 - (256 % alphabet.length) && out.length < length) {
        out.push(alphabet[b % alphabet.length])
      }
    }
  }
  return out.join('')
}

export function randomUUID(): string {
  if (crypto.randomUUID) {
    return crypto.randomUUID()
  }
  const b = randomBytes(16)
  b[6] = (b[6] & 0x0f) | 0x40
  b[8] = (b[8] & 0x3f) | 0x80
  const hex = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

const ss2022KeyLengths: Record<string, number> = {
  '2022-blake3-aes-128-gcm': 16,
  '2022-blake3-aes-256-gcm': 32,
  '2022-blake3-chacha20-poly1305': 32,
}

/** shadowsocks 密钥：2022 系列为对应长度的 base64 PSK，其他为随机密码。 */
export function shadowsocksKey(method: string | undefined): string {
  const length = method ? ss2022KeyLengths[method] : undefined
  if (!length) {
    return randomString(24)
  }
  return btoa(String.fromCharCode(...randomBytes(length)))
}

export function isSS2022(method: string | undefined): boolean {
  return !!method && method in ss2022KeyLengths
}
