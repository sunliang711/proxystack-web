# 面板左上角显示当前版本

## 目标与实现

左上角显示「管理面板 · 当前版本」，读取正在运行的后端 `/api/system/info`，避免前端写死版本。
悬停显示完整版本、提交号、构建时间；点击进入系统页。侧栏收起时，Logo 保留同样的提示和入口。
较长版本使用省略号，完整内容可以悬停查看；窄屏提示允许换行。

版本解析统一由 `internal/version.Current()` 提供：

| 构建方式 | 版本规则 |
| --- | --- |
| GitHub Actions 发布或手工注入 | 优先保留 `-ldflags -X` 注入的版本、提交号和时间 |
| 本地 `make build` | 保留现有 Makefile 从 Git 生成的版本 |
| 普通 `go build` | 从 Go 构建信息读取提交号，生成 `dev+<短提交号>` |
| 普通构建包含未提交修改 | 追加 `.dirty` |
| 缺少版本和 VCS 信息 | 使用 `dev`，提交与构建时间为 `unknown` |

前端将开发标识显示为 `dev · <短提交号> · dirty`。缺失的提交或时间在提示中显示「未知」。
版本请求加载时显示「读取版本中」，失败时显示「版本未知」。Go 内嵌的 `vcs.time` 是提交时间，不作为构建时间。

CLI、系统接口、健康检查、更新检查和备份元信息统一使用同一个版本解析入口，避免不同位置显示不一致。

## 影响范围

- `internal/version/version.go`：构建信息解析与降级规则。
- `internal/version/version_test.go`：正式版本优先、本地构建、缺失信息、dirty 和时间语义的表驱动测试。
- `internal/api/system.go`、`internal/api/api.go`：系统信息和接口文档版本读取。
- `internal/api/system_version_test.go`：系统接口版本字段回归。
- `cmd/proxystack-web/{main,info,serve}.go`：CLI 与启动日志版本读取。
- `internal/app/{health,deps,backup}.go`：现有版本使用位置保持一致。
- `web/src/layouts/MainLayout.vue`：版本显示、悬停详情、系统页入口和加载失败提示。

复用现有接口及响应字段，不增加数据库表、环境配置、依赖或外部服务。

## 验证结果

- `go test ./...`：通过。
- 前端 `vue-tsc --noEmit`、`vite build`：通过。
- `git diff --check`：通过。
- 实际普通构建的 `--version`：`dev+dfd39ad.dirty`，包含完整提交号，构建时间为 `unknown`。
- 实际 `-buildvcs=false` 构建的 `--version`：`dev (unknown, unknown)`。
- 显式注入构建的 `--version`：保留注入版本、提交和时间，不因工作区有修改而覆盖正式版本。
- `make build`：保留 Git 生成的版本信息。
- 在隔离的本地临时实例中完成浏览器验证：真实版本显示、悬停详情、点击跳转系统页、侧栏收起后的 Logo 提示，以及模拟正式版本和 503 后的「版本未知」与刷新恢复。
- 360px 窄屏截图与浮层边界检查通过：提示显示在 Logo 下方，完整提交号换行，浮层保持在屏幕内。

## 边界与交付

关闭 Go 的 VCS 记录或使用不含 Git 信息的源码包，且没有手工注入版本时，只能显示 `dev`；这是预期降级。
版本信息保存在二进制中，构建完成后不依赖运行机器上的 Git 仓库。
本次完成本地实现与验证。

## ChangeLog

### [Unreleased] · Added

- 面板左上角增加当前版本、构建详情提示与系统页入口；普通本地构建自动补充提交号和未提交修改标识。
