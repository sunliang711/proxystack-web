# 订阅端 CLI 默认目录与服务提示

本次 Go 改动配合订阅端安装隔离：`internal/subserver.DefaultDir` 改为 `/opt/proxystack-web-sub`，配置文件注释、服务重启日志和 `proxystack-web sub config` 的操作提示改为 `proxystack-web-sub`。`--dir` 和 `PSW_SUB_DIR` 仍支持显式指定目录，不自动读取或迁移 pssub 的目录。

未改变配置结构、订阅包格式、订阅 HTTP 接口、默认监听地址和端口，没有新增依赖或数据库变更。

通过 `go test ./...`；额外通过真实 systemd 容器中的 CLI 配置校验、订阅包导入及迁移前后 HTTP 内容对比。安装保护和迁移细节见 `2026-11-01-feature-shell-sub-isolation.md`。

## ChangeLog

### [Unreleased] - 2026-11-01

- Added：订阅端安装／卸载归属检查、显式迁移及失败回滚，新增隔离 systemd 容器中的部署测试入口。
- Changed：订阅端默认目录、账号和服务统一使用 `proxystack-web-sub`，同步 CLI 提示、文档与端到端测试。
