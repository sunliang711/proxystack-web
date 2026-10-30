/** 表单下拉框的固定选项。 */
export const listenOptions = ['0.0.0.0', '127.0.0.1', '::'].map((v) => ({ label: v, value: v }))

export const ssMethods = [
  'aes-128-gcm',
  'aes-256-gcm',
  'chacha20-ietf-poly1305',
  '2022-blake3-aes-128-gcm',
  '2022-blake3-aes-256-gcm',
  '2022-blake3-chacha20-poly1305',
].map((v) => ({ label: v, value: v }))

export const xrayLogLevels = ['debug', 'info', 'warning', 'error', 'none']
export const mihomoLogLevels = ['debug', 'info', 'warning', 'error', 'silent']

export const ruleTypes = [
  'DOMAIN', 'DOMAIN-SUFFIX', 'DOMAIN-KEYWORD', 'DOMAIN-REGEX', 'GEOSITE',
  'IP-CIDR', 'IP-CIDR6', 'IP-SUFFIX', 'IP-ASN', 'GEOIP',
  'SRC-IP-CIDR', 'SRC-PORT', 'DST-PORT', 'IN-PORT', 'IN-TYPE',
  'PROCESS-NAME', 'PROCESS-PATH', 'NETWORK', 'RULE-SET', 'AND', 'OR', 'NOT',
]

export const ipRuleTypes = ['IP-CIDR', 'IP-CIDR6', 'IP-SUFFIX', 'IP-ASN', 'GEOIP', 'SRC-IP-CIDR']

export const protocolLabels: Record<string, string> = {
  vmess: 'VMess',
  shadowsocks: 'Shadowsocks',
  socks5: 'SOCKS5',
  http: 'HTTP',
}

export const regions: { label: string; value: string }[] = [
  ['US', '美国'], ['JP', '日本'], ['HK', '香港'], ['TW', '台湾'], ['SG', '新加坡'], ['KR', '韩国'],
  ['GB', '英国'], ['DE', '德国'], ['FR', '法国'], ['NL', '荷兰'], ['CA', '加拿大'], ['AU', '澳大利亚'],
  ['RU', '俄罗斯'], ['IN', '印度'], ['TR', '土耳其'], ['AE', '阿联酋'], ['BR', '巴西'], ['CN', '中国'],
  ['MY', '马来西亚'], ['TH', '泰国'], ['VN', '越南'], ['PH', '菲律宾'], ['ID', '印度尼西亚'], ['IT', '意大利'],
  ['ES', '西班牙'], ['SE', '瑞典'], ['CH', '瑞士'], ['PL', '波兰'], ['AR', '阿根廷'], ['MX', '墨西哥'],
].map(([code, name]) => ({ label: `${code} ${name}`, value: code }))

export const templateVariables = ['.stack', '.inbound', '.protocol', '.port', '.user', '.profile', '.remark']
export const templateFunctions = ['toUpper', 'toLower', 'trim', 'replace']
