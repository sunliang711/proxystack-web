/** Mihomo 原生节点的示例配置，字段与 mihomo proxy 一致；注释里的值需要换成服务端的实际参数。 */
export interface NodeSample {
  key: string
  label: string
  /** Surge 订阅能否输出这个节点。 */
  surge: boolean
  yaml: string
}

export const nodeSamples: NodeSample[] = [
  {
    key: 'vless-reality',
    label: 'VLESS + Reality',
    surge: false,
    yaml: `type: vless
server: 203.0.113.10
port: 443
uuid: 00000000-0000-0000-0000-000000000000 # 服务端的 UUID
network: tcp
udp: true
tls: true
flow: xtls-rprx-vision
servername: www.microsoft.com # Reality 伪装的目标域名
client-fingerprint: chrome
reality-opts:
  public-key: REPLACE_WITH_PUBLIC_KEY # 服务端 Reality 公钥
  short-id: 0123456789abcdef
`,
  },
  {
    key: 'vless-ws',
    label: 'VLESS + WS + TLS',
    surge: false,
    yaml: `type: vless
server: proxy.example.com
port: 443
uuid: 00000000-0000-0000-0000-000000000000 # 服务端的 UUID
network: ws
udp: true
tls: true
servername: proxy.example.com
ws-opts:
  path: /ws
  headers:
    Host: proxy.example.com
`,
  },
  {
    key: 'trojan',
    label: 'Trojan',
    surge: false,
    yaml: `type: trojan
server: proxy.example.com
port: 443
password: REPLACE_WITH_PASSWORD # 服务端的密码
udp: true
sni: proxy.example.com
`,
  },
  {
    key: 'hysteria2',
    label: 'Hysteria2',
    surge: false,
    yaml: `type: hysteria2
server: proxy.example.com
port: 443
password: REPLACE_WITH_PASSWORD # 服务端的认证密码
sni: proxy.example.com
up: 30 Mbps
down: 200 Mbps
`,
  },
  {
    key: 'vmess-ws',
    label: 'VMess + WS + TLS',
    surge: true,
    yaml: `type: vmess
server: proxy.example.com
port: 443
uuid: 00000000-0000-0000-0000-000000000000 # 服务端的 UUID
alterId: 0
cipher: auto
udp: true
tls: true
servername: proxy.example.com
network: ws
ws-opts:
  path: /ws
  headers:
    Host: proxy.example.com
`,
  },
  {
    key: 'ss',
    label: 'Shadowsocks 2022',
    surge: true,
    yaml: `type: ss
server: 203.0.113.10
port: 8388
cipher: 2022-blake3-aes-256-gcm
password: REPLACE_WITH_BASE64_KEY # 服务端的 base64 密钥
udp: true
`,
  },
]
