# Mihomo provider 四项跟进修复独立复查

- 日期：2026-11-17
- 范围：Stack 编辑页分类、原生正则校验的事务与目录隔离、provider 依赖错误路径、omitted 导出及导入行为。
- 方式：独立读取实现及原调用闭包，检查事务和错误转换，执行针对性测试；未修改实现。

## 结论

本轮未发现需要继续修复的明确问题，未发现阻断项。

## 逐项复查

| 项目 | 复查结论 |
| --- | --- |
| 编辑页分类 | `web/src/views/stack/StackEditView.vue:202` 的 `relaysOnly` 在存在 provider 时返回 false；`mixedAuto` 同时识别动态来源与静态外部上游，与后端分类方向一致。 |
| 校验移出写事务 | `internal/app/config.go:145` 的准备事务只执行一次修改闭包、规范化和文档校验。没有新增或修改的非空正则时，沿用单事务持久化；需要原生校验时，candidate 尚未写入配置、修订或审计，事务结束后才执行 Checker。 |
| 并发提交与原子性 | `internal/app/config.go:179` 在第二个写事务中重读完整文档，与 `before` 的确定性 JSON 比较；一致才通过同一个 `persist` 写配置、修订和审计。SQLite 的 immediate 写事务保证比较与保存期间不会穿插其他写入。变化时返回全局 `ValidationError`，不重放闭包、不覆盖并发结果、不执行保存后的暂停同步或自动发布。检查了保存、克隆、回滚及导入等调用方的捕获变量，未发现此次分阶段处理引入闭包重放或索引错位。 |
| 校验目录隔离 | `internal/app/provider_config.go:31` 每次 Checker 调用使用随机、文件路径安全的独立名称；并行校验不会共享临时配置目录。 |
| 错误定位 | `internal/configdoc/validate.go` 将 `clash.providers` 映射为 `mihomo.providers`。真实禁用下载依赖所产生的问题能定位至 `stacks[1].mihomo.providers[0]`，没有沿用静态上游的 `.inbound` 后缀。 |
| omitted 导出与导入 | `ApplySecrets` 的 omitted 分支只清空 `Regenerate != nil` 的本机凭据。provider URL 的 `Regenerate` 为 nil，作为外部凭据保留。因此“omitted 会清空 URL”的判断不成立，无需修改 importer。新增测试覆盖 omitted 导出、YAML 编解码、merge overwrite 与 replace 计划，并确认 URL 保留、结果有效。 |

## 独立验证

以下均通过：

```text
go test ./internal/app -run '^TestProviderValidation' -count=1 -v
go test -race ./internal/app -run '^TestProviderValidation' -count=1 -v
PSW_TEST_MIHOMO=/tmp/psw-mihomo go test ./internal/app -run '^TestNativeProviderRegexValidation$' -count=1 -v
go test ./internal/configdoc -run 'Provider.*(Path|Omitted|Relay)|MapDomainPath' -count=1 -v
git diff --check
```

并发测试验证慢 Checker 期间其他数据库写入可以完成；并发配置保存会导致旧 candidate 明确失败且保留新配置；并行校验名称不同，并仅允许一个基于相同初始文档的 candidate 成功提交。真实核心回归使用 Mihomo v1.19.34，确认分阶段处理没有改变原生正则的有效与无效判定。

复查没有扩大到此前已通过的全量测试或其他既有功能。配置冲突后要求重新加载并重试是本方案的预期行为。
