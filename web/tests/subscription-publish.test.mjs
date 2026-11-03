import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

const settings = await readFile(new URL('../src/views/SubPublishSettingsView.vue', import.meta.url), 'utf8')
const publish = await readFile(new URL('../src/views/SubPublishView.vue', import.meta.url), 'utf8')

test('SSH 配置固定转发到订阅端回环地址并提供分阶段测试', () => {
  assert.match(settings, /permitopen=\"127\.0\.0\.1:\$\{target\.remote_port\}\"/)
  assert.match(settings, /command=\"\/bin\/false\"/)
  assert.match(settings, /PermitOpen 127\.0\.0\.1:\$\{target\.remote_port\}/)
  assert.match(settings, /ForceCommand \/bin\/false/)
  assert.match(settings, /远端主机固定为 127\.0\.0\.1/)
  assert.match(settings, /testResult\.result\.stages/)
})

test('添加订阅端时说明 Host Key 来源和获取方式', () => {
  assert.match(settings, /这是订阅端 SSH 服务的 Host Key，不是管理端生成的登录公钥/)
  assert.match(settings, /sudo ssh-keygen -lf \$\{publicKeyFile\} -E sha256/)
  assert.match(settings, /从输出中只复制完整的/)
})

test('完整配置可以重复查看 Secret，普通说明不拼接掩码', () => {
  assert.match(settings, /\/api\/sub-publish\/targets\/\{id\}\/setup/)
  assert.match(settings, /完整配置包含明文发布 Secret/)
  assert.match(settings, /可从目标列表的“完整配置”再次查看/)
  assert.match(settings, /每次查看都会记录审计日志/)
  assert.doesNotMatch(settings, /tokenCommand\([^)]*\).*MASKED/s)
})

test('发布页同时保留 SSH 在线发布和离线下载', () => {
  assert.match(publish, /SSH 在线发布/)
  assert.match(publish, /离线签发下载/)
  assert.match(publish, /target_ids: selected\.value/)
  assert.match(publish, /再次发布会生成新的序号/)
  assert.match(publish, /router\.push\(\{ name: 'sub-publish-settings' \}\)/)
  assert.match(publish, /curl -fsSL https:\/\/raw\.githubusercontent\.com\/sunliang711\/proxystack-web\/refs\/heads\/main\/deploy\/install\.sh/)
  assert.match(publish, /sudo bash -s -- --role sub --source/)
})
