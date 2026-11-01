# 订阅端安装隔离与迁移

## 背景与范围

本项目订阅端与 proxystack-go 的 pssub 共用 `/opt/proxystack-sub` 和 `proxystack-sub.service`。旧安装脚本仅凭配置文件存在即执行升级，旧卸载脚本也可能误操作 pssub。

## 实现

- 新安装使用 `/opt/proxystack-web-sub`、账号和组 `proxystack-web-sub`、`proxystack-web-sub.service`，默认端口继续为 9444。
- 安装先检查生效的 ExecStart、配置格式、目录和账号，再执行写操作；拒绝符号链接和归属不明的目录或服务。
- `.proxystack-web-sub` 标记在普通卸载后保留，用于重装和后续 `--purge`。卸载不再对 `proxystack-*` 广泛重置失败状态。
- 新增 `--role sub --migrate-sub`。校验本项目旧服务和配置，在 root 专属备份目录保存一致的数据副本、unit 和原服务状态。复制配置、订阅包、inbox 并调整权限，启动和 HTTP 检查通过后停用旧 unit，将原目录移入备份。
- 迁移失败时停用新服务、保留失败文件、恢复旧 unit、目录和运行／启用状态。订阅端专用的 CLI 软链接同步切换，管理端的链接保持原目标。
- `PLAN.md`、使用手册和原有端到端测试更新为新名称；CLI 调整见同日 Go 交付说明。

## 使用与限制

```bash
sudo bash deploy/install.sh --role sub --migrate-sub --binary ./proxystack-web
```

迁移保持配置原文和订阅链接，不允许通过同一命令覆盖公钥或监听配置。旧 service 缺失、配置无法识别、有符号链接或 systemd drop-in 时，需要先人工核对。pssub 不可被本项目迁移。备份不受卸载和 `--purge` 影响，运维账号的旧组成员关系需手动转入新组。

## 验证

- `bash -n deploy/install.sh deploy/uninstall.sh tests/deploy/run.sh`。
- `shellcheck deploy/install.sh deploy/uninstall.sh tests/deploy/run.sh`。
- `PSW_DEPLOY_IMAGE=psw-systemd:latest tests/deploy/run.sh`：在 Debian 12 的真实 systemd 容器中执行新安装、与 pssub 测试替身共存、升级、卸载重装与清理、未知目录／符号链接／同名服务保护、配置误识别拒绝、进程启动失败回滚、HTTP 检查失败且旧服务停用时的回滚、成功迁移。
- 使用真实 Ed25519 签名订阅包，对比迁移前后同一 token 的订阅 HTTP 内容，并检查公钥、监听端口、inbox、包文件属主、CLI 软链接及备份。
- `go test ./...`。

测试容器与产物由测试入口清理，未执行远端部署。原有涉及依赖下载和真实代理连接的完整业务端到端测试未在本次运行。
