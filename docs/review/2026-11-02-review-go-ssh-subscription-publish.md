# SSH 在线发布订阅包独立评审

## 审查范围

- 管理端 SSH 发布身份、订阅端目标配置、连通性测试和在线发布任务。
- SSH Host Key 算法及指纹校验、正向转发目标限制、Secret 生命周期和日志脱敏。
- 订阅端 `HEAD/POST /api/v1/subscription-bundles/import` 的鉴权、来源绑定、大小限制、并发导入和原子替换。
- 管理页面的在线/离线发布入口、一次性 Secret 展示和订阅端配置说明。
- OpenAPI、CLI、用户手册及新增自动化测试。

## 业务背景与最终方案

原流程需要在管理端签发并下载订阅包，再人工复制到订阅端导入。本次保留离线流程，并新增由管理端主动连接多个订阅端的 SSH 在线发布方式。

管理端生成一套全局 Ed25519 SSH 客户端密钥，每个订阅端目标独立配置 SSH 地址、用户、Host Key、公网不可见的回环 HTTP 端口和发布 Secret。管理端使用 `golang.org/x/crypto/ssh` 建立 `direct-tcpip` 正向转发，只访问订阅端 `127.0.0.1:<port>`，不启动外部 `ssh -L`，也不使用 SFTP。

订阅端在现有 HTTP 服务增加导入接口。Secret 通过 Bearer Header 传递，订阅端仅保存 SHA-256 哈希，并将 Secret 与受信任来源绑定。在线发布一次只签发一份原始 bundle，并发投递给多个目标；单个目标失败不回滚已成功目标。

## 首轮评审发现与修复

### P1：Host Key 类型未参与 SSH 算法协商

首轮实现仅在 `HostKeyCallback` 中比较 `key.Type()` 和 SHA-256 指纹，没有设置 `ssh.ClientConfig.HostKeyAlgorithms`。当 SSH 服务同时提供多种 Host Key，而管理员配置的不是客户端默认首选类型时，客户端可能先协商另一种类型，再被回调拒绝，导致正确配置无法连接。

修复内容：

- 增加受支持 Host Key 类型白名单：Ed25519、ECDSA P-256 和 RSA。
- 将配置的类型映射到实际协商算法并写入 `HostKeyAlgorithms`。
- RSA 仅协商 `rsa-sha2-512`、`rsa-sha2-256`，不回退到 SHA-1 `ssh-rsa` 签名算法；回调仍按 RSA 公钥类型和指纹核验。
- API 保存目标时拒绝不支持的 Host Key 类型。
- 增加进程内 SSH Server 集成测试，验证服务器同时提供 Ed25519 和 ECDSA 时能够协商管理员明确配置的类型。

该 P1 已修复并通过复审。

## 最终复审发现与修复

### 并发操作可能回写旧状态

连接测试或在线投递在网络请求执行期间，管理员可能修改目标地址、Host Key、来源、Secret 或启用状态。原实现完成请求后只按目标 ID 回写最近测试或发布状态，旧请求结果可能被写到已经变化的新配置上，造成页面显示错误的“测试通过”或发布序号。

修复后，发起测试和投递时保留完整目标快照；回写前使用 `samePublishTargetConfig` 比较 SSH 地址、用户、Host Key、远端端口、来源、Secret 和启用状态。只有当前配置仍与请求快照一致时才更新状态。配置已变化时跳过回写，在线发布任务日志明确记录状态未更新。新增并发回归测试覆盖测试期间修改目标和投递期间修改目标两个场景。

### Secret 轮换说明与真实语义不一致

原页面曾表述为“轮换后旧 Secret 立即失效”。实际过程是管理端先保存并立即使用新 Secret，而订阅端仍保存旧 Secret 哈希，直到管理员在订阅端执行 `set-publish-token`。在两端更新之间，管理端在线发布会失败，旧 Secret 对订阅端仍然有效。

修复后，后端注释、确认提示、成功提示、一次性 Secret 弹窗和用户手册统一说明真实切换过程；轮换同时清空旧的连接测试状态，要求管理员紧接着更新订阅端并重新测试。没有引入双 Secret 并存或隐式宽限期。

## 其他安全检查

- `authorized_keys` 使用 `restrict`、`command="/bin/false"` 和 `permitopen="127.0.0.1:<port>"`。
- `sshd_config` 示例使用 `AllowTcpForwarding local`、`PermitOpen`、`ForceCommand /bin/false`，并禁用 TTY、X11 和 Agent Forwarding。
- Host Key 同时校验公钥类型和 SHA-256 指纹，不采用首次连接自动信任。
- HEAD 探测除验证 Secret 外，还校验 `X-Proxystack-Source` 与目标来源一致。
- 创建和轮换 Secret 的响应设置 `Cache-Control: no-store`；列表接口只返回掩码。
- 订阅端鉴权先于请求体读取，限制 bundle 为 64 MiB，禁止 HTTP 重定向。
- HTTP、CLI 和 inbox 导入共用跨进程文件锁，同序号幂等、旧序号拒绝，并通过原子替换避免并发回退。
- 目标名称拒绝控制字符，避免任务日志和审计记录被换行或终端控制符污染。

## 最终验证

| 检查 | 结果 |
| --- | --- |
| `go test ./...` | 通过 |
| `go test -race ./internal/subserver ./internal/app ./internal/subpublish -run 'Test(OnlineBundleImport\|ImportConcurrentDoesNotRollBack\|SSHPublishIdentity\|OnlinePublish\|ConnectNegotiates\|TargetTestDoesNotOverwrite)' -count=1` | 通过 |
| `go vet ./internal/subpublish ./internal/subserver ./internal/app ./internal/api ./cmd/proxystack-web` | 通过 |
| `pnpm run typecheck`（`web` 目录） | 通过 |
| `pnpm run build`（`web` 目录） | 通过 |
| `pnpm run test:subscription-publish`（`web` 目录） | 通过 |
| `git diff --check` | 通过 |

## 复审结论

首轮 P1 和最终复审问题均已修复，未发现剩余阻断问题，代码可以提交。

## 低风险残余风险

1. 投递期间目标被直接删除时，状态不会错误写入其他目标或重新创建目标，但任务日志暂不提示“状态未更新”。
2. 尚无基于真实 OpenSSH `sshd` 的端到端自动化测试。当前使用进程内 SSH Server 覆盖算法协商；页面生成的 `authorized_keys` 和 `sshd_config` 片段仍需在部署环境执行 `sshd -t` 并完成实际连接测试。
