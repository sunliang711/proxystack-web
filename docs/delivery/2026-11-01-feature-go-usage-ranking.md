# 总览用量排行日月切换

## 需求与实现

总览原来的“本月用量排行”改为“用量排行”，卡片右上角增加“日 / 月”切换，首次进入页面默认选择“日”。两种排行都遵循顶部的用户筛选。

总览接口一次返回今日和本月的前十名，切换无需重新请求。日、月统计按系统时区的自然日和自然月计算，分别汇总全部 Stack 的用户及其档案流量，合并内存中未落库的部分后各自排序和截断。同用量按用户 ID 稳定排序。

## 文件与接口

- `internal/app/traffic.go`：`TrafficOverview` 新增 `top_users_today`。
- `internal/app/traffic_summary.go`：今日排行聚合与日月独立排序。
- `internal/app/traffic_summary_test.go`、`traffic_test.go`：日月差异、时区边界、用户筛选、档案合并、未落库数据与独立前十的验证。
- `web/src/views/DashboardView.vue`：标题、日月切换、排行数据及空状态。
- `web/src/router.ts`：同步总览页面说明。
- `web/openapi.json`、`web/src/api/schema.d.ts`：重新生成接口定义。

`GET /api/traffic/overview` 增加 `top_users_today`，原 `top_users` 继续返回本月排行。没有数据库、配置或依赖变更。

## ChangeLog

### [Unreleased] - 2026-11-01

- Changed：总览用量排行支持日、月切换，默认按日显示。

## 验证与部署

部署目标为 `root@10.2.78.11` 的 `proxystack-web` 服务；采用 Linux amd64 构建，上传校验后原子替换二进制，保留部署前备份用于回滚。

- `go test ./internal/app ./internal/api` 通过，新增排行边界测试使用 UTC 时钟配合系统时区验证，单独复测通过。
- 前端 58 项测试通过，生产构建及类型检查通过，`git diff --check` 通过。
- 部署后实际接口同时返回今日和本月数组，页面默认显示日榜，空状态分别显示“今日还没有用量”和“本月还没有用量”。
- 浏览器隔离响应验证两周期不同排序、切换不增加请求，以及指定用户后两周期显示对应的不同用量；没有写入测试流量。390px 手机宽度下检查切换控件与表格布局。
- 远端服务状态为 `active`，本地及远端 SHA-256：`dda378a223e2057600fa0694aaba3271118834f670f5e47694b42dfa38bdfab6`。
- 回滚备份：`/opt/proxystack-web/bin/proxystack-web.before-usage-ranking-20261101-072906`。
