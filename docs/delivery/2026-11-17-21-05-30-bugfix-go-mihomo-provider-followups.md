# Mihomo 上游订阅复查修复

## 核对结论

1. 前端 `relaysOnly` / `mixedAuto` 未考虑 provider：属实，已修复。
2. SQLite 写事务内执行 `mihomo -t`，并共用校验目录：属实，已修复。
3. `MapDomainPath` 缺少 `clash.providers` 映射：属实，已修复。
4. `omitted` 导出清空 provider URL 导致导入失败：不属实。`ApplySecrets` 只清理 `Regenerate != nil` 的本机凭据，provider URL 的 Regenerate 为 nil，作为外部凭据保留。未改变外部凭据的导入导出策略，新增完整往返测试验证。

## 变更

- `web/src/views/stack/StackEditView.vue`：带 provider 的 Stack 不再显示为纯 relay，auto 角色下视为混有外部来源，与后端分类一致。
- `internal/app/config.go`：仅在新增/修改非空正则需要调用原生核心时采用分阶段提交。首个短事务准备并校验候选配置，事务外执行原生检查；第二个写事务重新读取完整配置，与原配置 JSON 比较，一致才原子写配置、修订和审计。冲突作为全局校验错误返回，要求重新加载重试，不自动重放修改回调，也不覆盖并发保存。
- `internal/app/provider_config.go`：检测需要原生校验的变更；每次 Checker 调用使用独立随机目录名。无原生校验需求的原有修改路径仍保持单事务。
- `internal/configdoc/validate.go`：将图依赖错误的 `clash.providers` 转为 `mihomo.providers`，支持前端定位标签页及来源行。
- `internal/app/provider_validation_test.go`：慢校验期间的运行数据写入、并发配置修改冲突和并行校验目录隔离回归。
- `internal/configdoc/providers_test.go`：禁用的下载代理 Stack 实际触发的依赖错误定位，以及 omitted 导出、YAML 往返、覆盖/替换导入回归。

无数据库结构、依赖或 API schema 变更。

## 验证

- `go test ./...`：通过。
- `go vet ./...`：通过。
- `PSW_TEST_MIHOMO=/tmp/psw-mihomo go test -race ./internal/app ./internal/api ./internal/configdoc -run 'Provider|NativeProvider' -count=1`：通过，包含真实 Mihomo v1.19.34 原生下载和正则检查。
- 慢 Checker 尚未释放时，另一个数据库写入已完成；与校验并发的用户配置保存得到保留，旧候选提交被拒绝。
- 两个校验能同时进入 Checker，使用不同目录，提交时只允许仍符合原快照的候选成功。
- omitted 导出保留 URL，并且覆盖导入和整份替换导入都通过配置校验。
- 从实际 Stack 编辑页源码执行分类表达式：纯 relay、relay+provider、provider-only、auto 混合外部来源等 6 场景通过。
- 前端类型检查/生产构建通过，流向测试 4 项通过。
- `git diff --check`：通过。

## 交付边界

并发修改配置时选择明确报冲突，用户需重新加载后保存；运行状态写入不会因快照比较被视为配置冲突。原生检查仍使用已有 30 秒子进程超时，但不占用 SQLite 写锁。

保留此前工作区全部改动及用户已有 `.claude/`；本次按用户要求提交并创建本地 `v0.11.4` 标签，未推送或部署。独立复审见 `docs/review/2026-11-17-review-go-mihomo-provider-followups.md`。

## ChangeLog

### [Unreleased] Fixed

- 修复订阅来源参与前端 relay/auto 分类、原生校验持锁及目录共享、依赖错误定位缺失三个问题。
