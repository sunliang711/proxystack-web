import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

const settings = await readFile(new URL('../src/views/SubPublishSettingsView.vue', import.meta.url), 'utf8')
const publish = await readFile(new URL('../src/views/SubPublishView.vue', import.meta.url), 'utf8')
const clipboard = await readFile(new URL('../src/util/clipboard.ts', import.meta.url), 'utf8')

test('SSH 配置支持发布和部署并提供分阶段测试', () => {
  assert.match(settings, /permitopen="127\.0\.0\.1:\$\{target\.remote_port\}"/)
  assert.doesNotMatch(settings, /command="\/bin\/false"/)
  assert.match(settings, /PermitOpen 127\.0\.0\.1:\$\{target\.remote_port\}/)
  assert.match(settings, /\/etc\/ssh\/sshd_config\.d\/proxystack-web-sub\.conf/)
  assert.match(settings, /sudo sshd -t && sudo systemctl reload ssh/)
  assert.doesNotMatch(settings, /ForceCommand \/bin\/false/)
  assert.match(settings, /sudo -n \/bin\/true/)
  assert.match(settings, /\/deploy\/inspect/)
  assert.match(settings, /\/deploy\/rollback/)
  assert.match(settings, /远端主机固定为 127\.0\.0\.1/)
  assert.match(settings, /testResult\.result\.stages/)
  assert.match(settings, /1 检测部署 → 2 部署 → 3 测试 → 4 配置发布 Secret → 5 签发订阅包/)
  assert.match(settings, /disabled: row\.deploy_state !== 'installed'/)
  assert.match(settings, /row\.deploy_state === 'installed' \? '更新' : '部署'/)
  assert.match(settings, /第 4 步：配置发布 Secret（已自动完成）/)
})

test('添加订阅端时说明 Host Key 来源和获取方式', () => {
  assert.match(settings, /v-if="!draft\.id" label="SSH 用户创建"/)
  assert.match(settings, /sudo useradd --create-home --shell \/bin\/bash/)
  assert.match(settings, /该用户与运行订阅服务的/)
  assert.match(settings, /proxystack-web-sub/)
  assert.match(settings, /这是订阅端 SSH 服务的 Host Key，不是管理端生成的登录公钥/)
  assert.match(settings, /sudo ssh-keygen -lf \$\{publicKeyFile\} -E sha256/)
  assert.match(settings, /从输出中只复制完整的/)
})

test('完整配置可以重复查看 Secret，普通说明不拼接掩码', () => {
  assert.match(settings, /\/api\/sub-publish\/targets\/\{id\}\/setup/)
  assert.match(settings, /title="订阅端完整配置（不含 Secret）"/)
  assert.match(settings, /查看 Secret/)
  assert.match(settings, /将显示明文发布 Secret，查看操作会记录审计日志/)
  assert.match(settings, /每次查看都会记录审计日志/)
  assert.match(settings, /首次通过 SSH 安装或更新时，管理端会在部署完成后自动配置此 Secret/)
  assert.match(settings, /离线安装或手工重置时，先完成订阅端安装/)
  assert.match(settings, /proxystack-web-sub-cleanup-/)
  assert.match(settings, /sshd_config\.d\/proxystack-web-sub\.conf/)
  assert.match(settings, /sudo userdel -r \$\{target\.ssh_user\}/)
  assert.doesNotMatch(settings, /tokenCommand\([^)]*\).*MASKED/s)
})

test('发布页同时保留 SSH 在线发布和统一离线部署', () => {
  assert.match(publish, /SSH 在线发布/)
  assert.match(publish, /离线签发下载/)
  assert.match(publish, /target_ids: selected\.value/)
  assert.match(publish, /检测部署 → 部署 → 测试 → 配置发布 Secret → 签发订阅包/)
  assert.match(publish, /function targetReady\(target: PublishTarget\)/)
  assert.match(publish, /只有部署完成且测试通过的目标可以选择/)
  assert.match(publish, /再次发布会生成新的序号/)
  assert.match(publish, /router\.push\(\{ name: 'sub-publish-settings' \}\)/)
  assert.match(publish, /releases\/latest\/download/)
  assert.match(publish, /sha256sum -c/)
  assert.match(publish, /deploy apply --sha256/)
  assert.doesNotMatch(publish, /curl[^\n]+\|\s*sudo bash/)
})

test('复制命令在 Clipboard API 被拒绝时使用兼容降级路径', () => {
  assert.match(clipboard, /navigator\.clipboard\?\.writeText/)
  assert.match(clipboard, /document\.execCommand\('copy'\)/)
  assert.match(clipboard, /focused\?\.focus\(\{ preventScroll: true \}\)/)
})
