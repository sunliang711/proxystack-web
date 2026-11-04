# Stack 每日流量阈值

## 任务背景

为 Stack 增加每日流量阈值。当天上行和下行合计达到阈值后，自动停止该 Stack 并沿用现有每日阈值邮件通知；阈值为 0 时不限制。

## 实现方案

- 在 Stack 配置中增加 `daily_limit_bytes`，按系统设置时区的自然日统计。
- 新增 `stack_auto_stop` 表保存当天的自动停止状态，避免把管理员配置中的 `desired_running` 改掉。
- 流量采集周期内同时判定 Stack 阈值，包含尚未落库的内存增量。
- 当前日仍达到阈值时拒绝手动启动；调高阈值或设为 0 后允许手动启动。
- 跨自然日自动解除停止状态并按原期望状态恢复运行。
- 复用 `daily_limit` 通知事件、SMTP 配置和通知队列。

## 文件变更

- `internal/configdoc`：Stack 字段及校验。
- `internal/store`：数据库迁移、配置读写和查询。
- `internal/app`：阈值判定、自动停止、恢复、通知和启动校验。
- `web/src/views/stack/StackEditView.vue`：Stack 基本设置增加每日阈值。
- `web/src/components/UnitStatus.vue`：展示每日阈值自动停止状态。
- `web/openapi.json`、`web/src/api/schema.d.ts`：同步接口类型。

## 测试结果

- `go test ./internal/app ./internal/configdoc ./internal/store` 通过。
- 新增测试覆盖达到阈值停止、通知、手动启动拒绝、调高阈值后手动启动和跨日自动恢复。
- 当前环境未安装 `npm`，未能运行前端 typecheck 和生产构建。

## 风险与后续建议

- 阈值功能依赖现有流量采集；关闭流量采集时不会产生新的阈值判定。
- 邮件发送仍受现有 SMTP 配置和通知事件开关控制，停止 Stack 不依赖邮件发送成功。
