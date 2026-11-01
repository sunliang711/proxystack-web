# 带用户名的订阅链接与设置分组折叠

## 任务背景与实现

订阅链接增加用户名便于识别，系统设置分组支持折叠，减少浏览长页面的负担。

- 新链接为 `/s/<用户名>/<token>/<格式>`，支持 Clash、Premium Clash 和 Surge。用户名经过路径转义，仅用于识别，仍以随机 token 鉴权。
- 订阅端同时接受旧的 `/s/<token>/<格式>`。用户改名后旧标签仍可使用，Surge managed 链接使用当前用户名；重置 token 并发布导入后，旧 token 的两种路径均失效。
- 系统设置的十个分组首次全部折叠，浏览器按面板路径记住各分组及嵌套 policy 的展开状态。折叠保留表单输入；保存校验失败时自动展开错误所在分组。
- 本地缓存损坏或浏览器禁止存储时，仍能正常展开、折叠设置。

## 文件范围

- `internal/subscription/snapshot.go`：统一订阅路径生成与 Surge managed 地址。
- `internal/app/subscription.go`：管理端链接与预览地址。
- `internal/subserver/server.go`、`config.go`：兼容新旧路由及入口说明。
- `internal/app/subscription_test.go`：新旧路径、三种格式、改名及 token 失效验证。
- `web/src/components/SettingsSection.vue`、`web/src/composables/useSettingsSections.ts`、`web/src/views/SettingsView.vue`：折叠卡片、偏好存储及错误定位。
- `web/tests/settings-sections.test.mjs`、`web/package.json`：折叠状态测试及执行入口。
- `docs/user-guide.md`：操作说明与升级要求。

## 配置与部署

没有数据库、配置项或依赖变更。折叠偏好仅保存在当前浏览器的 localStorage，不包含设置内容。

使用带用户名的新链接，需要管理端与订阅端均升级；可先升级订阅端以兼容原有管理端链接，再升级管理端。已有旧链接继续可用。

本次仅进行本地提交，未推送或部署。

## 验证结果

- `go test ./...`：通过。
- `go test ./internal/app -run TestNamedSubscriptionLinks -count=1`：补充预览断言后通过。
- `npm run test:settings`：3 项通过，覆盖默认状态、缓存读写、损坏缓存、存储不可用及校验错误展开。
- `npm run build`：类型检查及构建通过。
- 浏览器验证：首次折叠、多段同时展开、刷新恢复、折叠保留输入、校验错误展开、用户详情三种链接带用户名；截图检查通过。
- `git diff --check`：通过。

## [Unreleased] - 2026-11-01

### Added

- 订阅链接增加用户名标签，并保留原 token 链接兼容。
- 系统设置支持默认折叠、浏览器记忆展开状态及校验错误自动展开。
