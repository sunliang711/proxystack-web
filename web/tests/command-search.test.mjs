import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let search

before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  search = await server.ssrLoadModule('/src/util/command-search.ts')
})
after(async () => server?.close())

const entry = (id, kind, title, extra = {}) => ({ id, kind, title, to: '/', ...extra })
const entries = [
  entry('user:1', 'user', 'alice', { subtitle: '财务部', keywords: [] }),
  entry('user:2', 'user', 'malice'),
  entry('user:3', 'user', 'bob', { subtitle: 'alice 的备用号' }),
  entry('stack:1', 'stack', 'hk-edge', { subtitle: 'auto · 香港' }),
  entry('page:sub-publish', 'page', '发布订阅包', { subtitle: '订阅', keywords: ['签名后通过 SSH 发送到订阅端'] }),
  entry('page:sub-publish-settings', 'page', 'SSH 发布与部署', { subtitle: '订阅' }),
  entry('setting:basic', 'setting', '基本', { subtitle: '系统设置 · 基本', keywords: ['时区', 'timezone'] }),
  entry('setting:notify', 'setting', '通知', { subtitle: '系统设置 · 流量与通知', keywords: ['smtp', '收件人'] }),
]
const titles = (groups) => groups.flatMap((group) => group.entries.map((item) => item.title))

test('标题开头优先于标题包含，标题优先于备注', () => {
  const groups = search.searchEntries(entries, 'alice')
  assert.deepEqual(titles(groups), ['alice', 'malice', 'bob'])
})

test('多个关键词需要全部命中，大小写和全角不敏感', () => {
  assert.deepEqual(titles(search.searchEntries(entries, 'HK 香港')), ['hk-edge'])
  assert.deepEqual(titles(search.searchEntries(entries, 'ＳＭＴＰ')), ['通知'])
  assert.deepEqual(titles(search.searchEntries(entries, 'hk 不存在')), [])
})

test('按设置项名称找到分组，按页面说明找到页面', () => {
  assert.deepEqual(titles(search.searchEntries(entries, '时区')), ['基本'])
  assert.deepEqual(titles(search.searchEntries(entries, '签名')), ['发布订阅包'])
})

test('单个字符只匹配标题和其他字段的开头，不匹配说明中间', () => {
  assert.equal(search.scoreEntry(entries[4], 's'), 0)
  assert.ok(search.scoreEntry(entries[4], 'ss') > 0)
  assert.ok(search.scoreEntry(entries[6], 't') > 0)
})

test('标题中词的开头命中排在普通包含之前', () => {
  assert.deepEqual(titles(search.searchEntries(entries, 'edge')), ['hk-edge'])
  assert.equal(search.scoreEntry(entries[3], 'edge') > search.scoreEntry(entries[1], 'lice'), true)
})

test('英文缩写按字符顺序匹配标题，但得分最低', () => {
  assert.ok(search.scoreEntry(entry('x', 'page', 'sub-publish'), 'sbp') > 0)
  assert.equal(search.scoreEntry(entry('x', 'page', 'sub-publish'), 'psb'), 0)
  assert.equal(search.scoreEntry(entry('x', 'page', '发布订阅包'), '布包'), 0)
})

test('最相关的类别排在最前，同分按用户、Stack、页面、设置的顺序', () => {
  const groups = search.searchEntries(entries, 'ssh')
  assert.deepEqual(groups.map((group) => group.kind), ['page'])
  assert.deepEqual(titles(groups), ['SSH 发布与部署', '发布订阅包'])
  const mixed = search.searchEntries([entry('s', 'setting', 'x'), entry('u', 'user', 'x')], 'x')
  assert.deepEqual(mixed.map((group) => group.label), ['用户', '系统设置'])
})

test('空查询没有结果，每组按上限截断', () => {
  assert.deepEqual(search.searchEntries(entries, '   '), [])
  const many = Array.from({ length: 20 }, (_, i) => entry(`user:${i}`, 'user', `u${i}`))
  assert.equal(search.searchEntries(many, 'u', 5)[0].entries.length, 5)
})

test('最近访问去重置顶，已删除的对象自动略过', () => {
  assert.deepEqual(search.pushRecent(['a', 'b', 'c'], 'b'), ['b', 'a', 'c'])
  assert.deepEqual(search.pushRecent(['a', 'b', 'c'], 'd', 3), ['d', 'a', 'b'])
  assert.deepEqual(search.resolveRecent(['user:9', 'stack:1', 'user:1'], entries).map((item) => item.id), ['stack:1', 'user:1'])
})
