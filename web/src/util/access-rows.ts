import type { Schemas } from '../api/client'

type AccessDomainCount = Schemas['AccessDomainCount']

/** 访问排行的表格行：按服务归类时服务行带 children（各主域名），key 在整张表内唯一。 */
export type AccessRow = AccessDomainCount & { key: string; children?: AccessRow[] }

/** 服务行（合并了多个主域名）。 */
export function isServiceRow(row: AccessDomainCount): boolean {
  return (row.members?.length ?? 0) > 0
}

/** 把接口返回的排行转成表格行：服务行的 key 用服务名，主域名行用域名，避免代表域名与成员域名重复。 */
export function accessRows(rows: AccessDomainCount[]): AccessRow[] {
  return rows.map((row) => isServiceRow(row)
    ? { ...row, key: `service:${row.service}`, children: (row.members ?? []).map((member) => ({ ...member, key: `domain:${member.domain}` })) }
    : { ...row, key: `domain:${row.domain}` })
}

/** 一级行的名次（服务内的主域名没有名次）。 */
export function accessRanks(rows: AccessRow[]): Map<string, number> {
  return new Map(rows.map((row, index) => [row.key, index + 1]))
}

/**
 * 在接口返回的排行里更新主域名的黑名单状态（一级行和服务内的主域名都更新）。表格行是这份数据的副本，
 * 要改原数据才能触发重新渲染。
 */
export function setListed(rows: AccessDomainCount[], domain: string, listed: AccessDomainCount['listed']) {
  for (const row of rows) {
    if (!isServiceRow(row) && row.domain === domain) row.listed = listed
    for (const member of row.members ?? []) {
      if (member.domain === domain) member.listed = listed
    }
  }
}
