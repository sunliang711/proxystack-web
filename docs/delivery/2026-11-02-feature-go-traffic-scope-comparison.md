# 总览流量双口径对比

## 任务背景

总览原来的“今日流量”和“本月流量”按所有 Stack 的入站计数汇总，跨 Stack relay 会在来源和目标 Stack 各计数一次；用量排行按用户计数汇总，两者不能直接相加比较。页面需要同时展示两种口径，并明确显示差额。

## 实现方案

- 保留 `today`、`month` 作为各 Stack 入站合计，兼容现有接口调用方。
- `GET /api/traffic/overview` 新增 `user_today`、`user_month`，返回能归属到具体用户及其档案的用量。
- `by_stack` 中同步新增 `user_today`、`user_month`，用于定位差额所在 Stack。
- 总览在未指定用户时展示入站、用户可归属及有符号口径差额；指定用户时继续只展示该用户及其档案的流量。
- 差额使用有符号值，不把两套独立 Xray 计数器因采集时点产生的小幅负差错误截断为零。

## 文件变更

- `internal/app/traffic.go`：扩展总览响应结构。
- `internal/app/traffic_summary.go`：按日、月和 Stack 聚合用户可归属流量，合并未落库增量。
- `internal/app/traffic_summary_test.go`、`internal/api/traffic_test.go`：覆盖双口径、用户筛选、档案合并、未知用户和空响应。
- `web/src/views/DashboardView.vue`：增加双口径统计卡片和 Stack 差额明细。
- `web/src/router.ts`、`docs/user-guide.md`：同步页面说明。
- `web/openapi.json`、`web/src/api/schema.d.ts`：更新接口定义。

## 数据与依赖变更

- 无数据库迁移。
- 无配置项变更。
- 无新增或升级依赖。

## ChangeLog

### [Unreleased] - 2026-11-02

- Added：总览支持对比各 Stack 入站和用户可归属流量，并显示 relay / 未归属口径差额。

## 验证

- `go test ./internal/app ./internal/api`
- `go test ./...`
- `pnpm run gen:api`
- `pnpm run build`
- `pnpm run "/^test:/"`
- `git diff --check`
