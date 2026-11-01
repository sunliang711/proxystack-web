# Xray 访问日志开关

## 行为与入口

- 系统设置 → 默认值 → Xray 记录访问日志：全局开关，默认开启，保持旧版本行为。
- Stack 详情 → Xray → 记录访问日志：继承全局 / 开 / 关，新建和旧 Stack 均默认继承。
- Stack 显式覆盖优先于全局。关闭时生成 `log.access: "none"`，开启时沿用 Xray 默认访问日志输出。
- 访问日志开关不修改 `loglevel`、流量统计或用户凭据；Xray 的 `loglevel: "none"` 仍会关闭全部日志。
- 设置保存后，通过现有应用配置流程重启对应 Xray 后生效。

## 配置、存储与接口

全局配置为 `settings.defaults.xray.access_log`（布尔值，省略时开启）；Stack 配置为 `xray.access_log`（`inherit` / `on` / `off`，省略时继承）。已有设置和 Stack API 承载新增字段，OpenAPI 和前端类型已重新生成。

迁移 `00008_xray_access_log.sql` 为 `stacks` 增加 `xray_access_log` 列，默认值为 `inherit`，并约束三态枚举。全局设置沿用原有 JSON 存储；未新增依赖。

## 验证和评审

- `go test ./...` 通过。
- 覆盖继承与覆盖优先级、旧文档默认值、JSON/YAML 往返、数据库持久化、API 校验及旧客户端省略字段。
- 从第 7 版真实旧表结构升级的迁移测试通过，保留原日志级别。
- 前端生产构建及类型检查通过。
- 独立 agent review 完成，未发现明确需要修复的问题。
- 测试机的隔离 Xray 实例在 `error` 级别分别开启和关闭访问日志，VMess 请求、上下行统计、热增删均正常；关闭时不输出访问日志，临时进程与文件已清理。

## 发布与回滚

部署前备份数据库和管理服务 binary，启动服务时自动执行迁移。默认设置不产生 Xray 配置变化。回滚涉及数据库版本时使用部署前备份；若已修改设置并应用，也需恢复相应生成配置和运行状态。

## ChangeLog

### Added

- 增加全局 Xray 访问日志开关和 Stack 级三态覆盖。
