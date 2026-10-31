# Stack 流量统计与界面交互调整

## [Unreleased] - 2026-10-31

### Added

- 总览支持可选用户筛选，展示总和及各 Stack 的今日、本月用量和实时上下行速率。
- 流量明细支持按 Stack 分组，同时返回所选范围的上下行合计；CSV 跟随当前分组和筛选。
- 用户详情增加各 Stack 的今日用量、最近 30 天用量与趋势，可筛选单个 Stack。
- 订阅模板预览支持下载当前用户和格式的配置，Clash/Premium 使用 `.yaml`，Surge 使用 `.conf`。

### Changed

- Stack 列表入站列展示协议数量摘要，点击展开完整名称、协议、监听地址及端口。
- Stack 详情页「流量图示」改名为「流量拓扑」。

## 统计口径与影响范围

- `GET /api/traffic/overview` 新增可选 `user_id`；未指定时按入站统计，指定时按该用户及其档案统计。
- `GET /api/traffic/query` 和 CSV 导出新增 `group_by=stack|subject`，默认 `subject` 保持接口兼容。
- 明细返回 `total`、`by_stack`；总览和用户用量接口增加 `by_stack`；实时数据增加按用户的速率。
- 继续使用现有 `stack_id` 明细，不修改数据库结构、不升级依赖、不更改额度和自动暂停规则。
- 用户详情的本周期额度是全部 Stack 的合计；Stack 分项展示今日与最近 30 天已保留明细，不将其误标为完整计费周期用量。
- 已删除 Stack 的历史记录显示为「已删除 Stack #ID」。

## 文件变更

- 后端：`internal/app/traffic.go`、`internal/app/traffic_summary.go`、`internal/api/traffic.go`。
- 测试：现有流量测试及新增 `traffic_summary_test.go`、API `traffic_test.go`。
- 前端：总览、流量明细、用量卡片、模板预览、入站摘要组件、Stack 列表和详情标签。
- API 类型：重新生成 `web/openapi.json`、`web/src/api/schema.d.ts`。

## 验证

- `go test ./...` 通过。
- `vue-tsc --noEmit` 通过，Vite 生产构建通过。
- 新增统计测试覆盖四种时间粒度、档案合并、同名入站、Stack 和用户筛选、空数据、删除历史、CSV、未落库流量及实时速率。
- 浏览器验证总览的全部用户与指定用户筛选，无流量用户显示为零；按 Stack 合计与接口总数一致。
- 浏览器验证流量明细按用户筛选、按 Stack/对象切换，用户详情显示各 Stack 的今日与近 30 天统计。
- 入站详情逐项比对原摘要，名称、协议、监听地址与端口全部保留；详情页拓扑标签正常。
- 三种订阅格式均点击下载，实际文件 SHA-256 与预览接口响应一致；切换预览用户也正常。
- 已检查页面运行时错误列表为空，远端管理服务无异常重启，四个代理服务均处于 active；发布命令仅重启管理服务。
- 使用远端 nico 用户在 de1、usa11 上的实际流量验证跨 Stack 总和，用户详情选择 de1 后仅展示该 Stack 的统计。

## 部署

- 测试机：`10.2.78.11`，管理面板：`http://10.2.78.11:9443/`。
- 构建版本：`v0.0.6-stack-stats`，基于 `617a6f6` 加本次未提交修改。
- Linux amd64 静态二进制；上传后校验 SHA-256、运行 `--version`，替换并重启管理服务。
- 最终二进制 SHA-256：`b6edcbc977b7e14fa677d7d867cade2d8d7c9eea73889501393025389edbcebc`。
- 原版本备份：`/opt/proxystack-web/bin/proxystack-web.before-stack-stats-20261031T073901Z`。
