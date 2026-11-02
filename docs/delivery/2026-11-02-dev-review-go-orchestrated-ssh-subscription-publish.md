# SSH 在线发布订阅包开发与独立评审交付

交付日期：2026-11-02。主 Agent 完成方案设计、实现、评审问题修复和最终验证；独立 Agent 执行方案与代码评审、修复后复审，并整理本交付记录。

## 任务背景

管理端原有发布方式只能签发并下载订阅包，管理员需要把文件复制到订阅端再手工导入。目标是在保留离线发布的同时，增加管理端直接向多个订阅端在线发布的能力，并满足以下约束：

- 使用 SSH 正向转发，不要求订阅端维护反向隧道。
- 不使用 SFTP，订阅包通过订阅端 HTTP 接口导入。
- 一个管理端可以配置并选择多个订阅端。
- 管理页面可分别测试 SSH、转发、订阅端健康状态和发布鉴权。
- 订阅端导入接口使用独立 Secret 鉴权，页面提供完整配置说明。

## 最终方案

- 管理端首次使用时生成全局 Ed25519 SSH 客户端密钥，私钥 seed 只保存在管理端，页面和 API 仅返回公钥及指纹。同一公钥可手工安装到多台订阅端。
- 每个订阅端目标保存独立 SSH 参数、严格校验的 Host Key 类型与 SHA-256 指纹、固定回环目标端口、来源名、启用状态和独立发布 Secret。
- 管理端通过 `golang.org/x/crypto/ssh` 的 `direct-tcpip` 访问订阅端 `127.0.0.1:<port>`，转发目标不可配置为其他主机。
- 订阅端提供 `HEAD/POST /api/v1/subscription-bundles/import`。HEAD 用于测试 Secret 和来源绑定；POST 接收原始签名 bundle，鉴权成功后再读取请求体。
- Secret 使用 Bearer Header 传递。管理端保存投递所需明文，订阅端只保存 SHA-256 哈希；创建和轮换时明文只返回一次，响应禁止缓存。
- 在线发布一次签发一份 bundle，最多并发投递四个目标。目标结果独立记录，部分失败不回滚已成功目标；离线签发下载流程保持不变。
- 订阅端使用跨进程文件锁、序号检查和原子替换统一保护 HTTP、CLI 与 inbox 三种导入方式。

## 主要实现范围

### 管理端后端

- 新增 SSH 发布身份和目标的读取、创建、修改、删除、Secret 轮换与连通性测试。
- 新增在线发布后台任务、多目标并发控制、逐目标结果记录和审计日志。
- 目标配置变化时清除已失效的测试和发布状态；异步任务只在目标快照仍匹配时回写状态。
- 限制来源名必须与管理端当前订阅来源一致，目标名称禁止控制字符。

### SSH 发布客户端

- 严格校验 Host Key 类型和指纹，并按配置类型限制算法协商。
- SSH 转发远端固定为回环地址，HTTP 客户端禁止重定向。
- 分阶段报告 TCP、Host Key、SSH 认证、正向转发、健康检查和发布鉴权结果。
- 投递响应校验来源、序号和导入状态。

### 订阅端

- 来源配置增加 `publish_token_hash`，校验格式和跨来源唯一性。
- CLI 增加 `sub source set-publish-token` 和 `clear-publish-token`；Token 从隐藏输入或 stdin 读取，不进入命令参数和输出。
- 在线导入接口支持来源绑定、64 MiB 上限、Content-Type 校验、错误脱敏和成功后立即 reload。
- HTTP 服务增加请求头、请求体和空闲超时。

### 管理页面与文档

- 新增“SSH 发布设置”页面，支持密钥生成、多个目标管理、Secret 轮换、连接测试及可复制的动态配置说明。
- “发布订阅包”页面提供“SSH 在线发布”和“离线签发下载”两种模式，可多选目标并跳转任务详情。
- 配置说明包含受限 `authorized_keys`、`sshd_config`、发布 Secret 配置命令和 Host Key 独立核验步骤。
- 同步更新 OpenAPI、TypeScript schema、规划文档和用户手册。

## 独立评审与修复

### 首轮 P1：Host Key 算法协商

首轮评审发现，客户端虽然在回调中校验 Host Key 类型和指纹，但未限制 SSH 算法协商。服务器提供多种 Host Key 时，正确配置的非默认类型可能无法连接。

修复后按公钥类型设置 `HostKeyAlgorithms`，明确支持 Ed25519、ECDSA P-256 和 RSA SHA-2；API 拒绝不支持的类型，并新增进程内 SSH Server 集成测试验证多 Host Key 下的指定算法协商。该问题复审通过。

### 最终复审：并发旧状态回写

最终复审发现，测试或投递期间若目标配置被修改，完成后的旧请求结果可能按 ID 回写到新配置。

修复后，异步操作携带完整目标快照，回写前比较 SSH 地址、用户、Host Key、远端端口、来源、Secret 和启用状态。配置不一致时跳过状态更新并记录任务日志。新增两个并发测试，分别覆盖测试期间修改目标和投递期间修改目标。

### 最终复审：Secret 轮换语义

原提示错误地声称旧 Secret 会在管理端轮换后立即失效。实际是管理端立即使用新 Secret，订阅端继续接受旧 Secret，直到执行 `set-publish-token` 更新哈希。

页面确认文案、成功提示、后端注释和用户手册已统一修正：两端更新之间在线发布会失败，订阅端更新后旧 Secret 才失效。轮换后清除旧测试状态，要求重新配置并测试。

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

## 评审结论

首轮 P1 与最终复审发现均已修复，独立复审未发现新的阻断问题。实现满足多目标 SSH 正向转发、公开接口 Secret 鉴权、在线/离线双模式、状态审计和详细配置说明要求，可以提交。

## 低风险残余风险

1. 投递期间目标被直接删除时不会发生错误状态回写，但任务日志暂不提示该目标的状态未更新。
2. 尚无真实 OpenSSH `sshd` 端到端自动化测试。进程内 SSH Server 已覆盖算法协商，部署时仍需对生成的 `authorized_keys` 和 `sshd_config` 执行 `sshd -t` 并进行实际连接测试。
