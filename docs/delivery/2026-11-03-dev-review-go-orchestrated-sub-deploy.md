# 订阅端统一部署与独立评审交付

交付日期：2026-11-03。

## 背景

管理端原有 SSH 通道只支持订阅包发布，订阅端安装、更新和卸载主要由 Shell 脚本完成。此次将离线和 SSH 部署统一到订阅端二进制自身，并让同一套 SSH 地址、用户和密钥同时承担受限端口转发与部署操作。SSH 用户需要支持非交互式 `sudo -n`，页面提供用户、公钥、`sshd_config`、sudoers 校验和 SSH reload 所需命令。

## 统一部署方案

- 新增 `proxystack-web sub deploy inspect|apply|rollback|uninstall`。首次安装、更新、回滚和卸载共用 `internal/subdeploy` 的归属检查、部署锁、原子替换、健康检查和失败恢复逻辑。
- 离线部署校验 Release 裸二进制 SHA-256 后执行 `sub deploy apply`；`deploy/install.sh --role sub` 降为下载和参数转换的薄包装。
- SSH 部署先检测远端 Linux 架构和 `sudo -n`，管理端按远端架构下载并校验 Release，通过 SSH 上传后调用同一个 `sub deploy apply --json`。发布 Secret 通过 stdin 配置，不出现在命令参数和日志中。
- 更新保留 `config.yaml` 和订阅包，旧二进制保存为 `.prev`；新版本启动或健康检查失败时恢复旧二进制和 unit。发布、部署、回滚、Secret 轮换及目标修改采用同一目标互斥。
- 普通卸载保留配置和数据，`--purge` 同时删除订阅端目录和服务账号。旧 `--migrate-sub` 接口不再支持，需要先用旧版本完成迁移。

## 主要变化

- CLI 与部署核心：新增 `cmd/proxystack-web/sub_deploy.go`、`internal/subdeploy/`，订阅端 `/health` 增加运行版本。
- SSH 与应用层：新增 `internal/subpublish/deploy.go`、`internal/app/subdeploy.go`，扩展目标部署状态、后台任务、审计和发布/部署互斥。
- API：新增 `POST /api/sub-publish/targets/{id}/deploy/inspect`、`POST /api/sub-publish/targets/{id}/deploy`、`POST /api/sub-publish/targets/{id}/deploy/rollback`，任务中心增加 `subscription-deploy` 类型。
- UI：导航调整为“SSH 发布与部署”；目标列表展示部署状态、当前及上一版本，并提供检测、安装/更新、回滚入口；配置页展示 `sudo -n` 和 sudoers 命令；离线页展示裸二进制安装、回滚和卸载命令。
- 脚本与文档：`deploy/install.sh`、`deploy/uninstall.sh` 复用新的本地部署入口，OpenAPI 类型、用户手册、规划文档和部署测试同步更新。

## 测试结果

以下验证均通过：

- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `shellcheck deploy/install.sh deploy/uninstall.sh tests/deploy/run.sh`
- 前端 `pnpm typecheck`、`pnpm test:subscription-publish`、`pnpm build`
- `tests/deploy/run.sh`：首次安装、更新、回滚、普通卸载、保留数据重装和 purge
- `go test -race -count=1 ./internal/app -run 'TestSubscriptionTargetCannotChange'`：目标修改互斥测试通过

全量 `internal/app` race 测试中的既有 `TestMigrationPackageRestoresAnotherInstance` 因 race 模式下三秒等待超时而失败，未报告数据竞争；本次新增互斥定向 race 测试已通过。

## 独立 Review 与修复

独立 Review 首轮发现并修复五项：sudoers 命令范围、SSH 取消传播、Secret 轮换互斥、更新失败时完整恢复、卸载停止失败处理。

二轮发现并修复三项：发布与部署互斥、SSH 测试退出状态分类、卸载后的服务状态校验。随后又修复了部署任务在加锁前读取目标配置导致的竞态。最终复核通过，无剩余阻断或警告问题。

## 真实离线测试

在测试订阅端 `10.2.9.172` 完成真实离线验证：

- 安装 `v0.3.6-pretest.1` 成功，`proxystack-web-sub.service` 为 active/enabled。
- `/health` 返回 HTTP 503、状态 `unavailable`，原因是尚未导入订阅包，属于预期行为；响应中的版本正确。
- purge 卸载成功，随后 `deploy inspect` 返回 `uninstalled`。
- 再次离线安装成功，当前测试安装予以保留，供后续 SSH 更新验证。

## Release 与 SSH 真实验证

- 代码提交 `daa7d0b` 已推送到 `main`，并创建 `v0.3.6` Tag。GitHub Release workflow 成功，amd64/arm64 裸二进制、归档及四个 SHA-256 文件均已发布。
- 测试管理端从 `v0.2.7` 升级到 `v0.3.6`，升级前已备份数据库和旧二进制，服务重启后保持 active/running。
- 管理端通过 SSH 正确识别现有订阅端 `v0.3.6-pretest.1`，随后任务将其更新到 `v0.3.6`；当前二进制和 `.prev` 所有权、服务 active/enabled、版本健康检查均正确。
- 将订阅端 purge 后，管理端检测为 `uninstalled`，再通过同一 SSH 目标完成 `v0.3.6` 首次安装。
- 目标连接测试的 TCP、Host Key、公钥认证、端口转发、健康检查和发布 Secret 全部通过；通过同一通道在线发布订阅包序号 7 后，订阅端 `/health` 为 `ok`，版本 `v0.3.6`，包含 2 个用户和 4 个节点。
- 测试结束后已 purge 订阅端、删除临时管理端目标并移除测试公钥条目，测试订阅端恢复为未安装状态；管理端 SSH 身份保留供后续正式配置使用。
