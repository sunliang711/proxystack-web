# Mihomo 上游订阅独立代码评审

- 日期：2026-11-17
- 方式：由独立 review agent 读取工作区差异、检查新增文件、复现问题并复验修复。
- 范围：本次 provider 配置和存储、配置生成、controller 客户端、更新与调度、HTTP API、Vue 页面和流向图；不涉及已有 `.claude/` 内容。
- 基线：`code-review-go` 的安全、规范、质量三维检查；沿用仓库现有 Go/Chi/Huma/sqlc/Vue 架构。

## 发现与修复复核

| 等级 | 发现 | 触发条件与影响 | 修复及复核 |
| --- | --- | --- | --- |
| P1，已修复 | 克隆 Stack 没有清空 provider ID | 克隆已保存的订阅来源后，`SaveDocument` 以相同 ID upsert，来源会从原 Stack 迁移到克隆 Stack，原代理组引用悬空。独立存储复现得到原 Stack 0 个来源、克隆 Stack 1 个来源。 | `internal/configdoc/ops.go:261` 在克隆时清 ID；`internal/configdoc/validate.go:88` 校验跨 Stack ID 唯一性。`TestProviderCloneKeepsOriginalOwnership` 经过 Service 克隆并重新读取数据库，验证原来源保留且克隆获得新 ID。独立复跑通过。 |
| P2，已修复 | Go 与 Mihomo 正则引擎被混用 | 原生有效的 `a{1001}` 被 Go 校验拒绝；Go 有效的 `(?P<region>HK)` 被直接接受，但真实核心拒绝。会导致错误拒绝或保存后无法应用。 | `internal/app/provider_config.go:18` 将所有新增或修改的非空表达式交给已安装核心校验；包含和排除使用原生字段名。缺少核心时明确要求安装，未变表达式不重复检查。`TestNativeProviderRegexValidation` 使用真实 v1.19.34 通过上述正反例及排除表达式回归。 |
| P2，已修复 | 拓扑与详情流向遗漏动态上游 | provider-only Stack 会被列表拓扑归类为直连，详情图没有 `group.use` 的出口；混合 relay 和动态出口也可能沿用纯 relay 统计分类。 | `internal/api/config.go:624` 输出来源名称；`web/src/util/flow.ts` 增加动态出口分类、来源节点和引用边；`internal/configdoc/ops.go:191` 排除混合动态出口的纯 relay 分类。新增前端流向与 Go 分类测试，独立复跑通过。 |

## 交付前补充复审

最终整理新增的调度器收尾、轻量预检查及超时控制也已独立复审：

- 初版通过 `WaitJobs()` 等待关停，会把既有 `context.WithoutCancel` 的手动作业一起等待，可能延迟关停到其长超时结束。现改为 provider 专属 `WaitGroup`，在同一互斥锁下关闭新工作准入后排空，自动 pass 和手动 flight 均被跟踪，不改变其他手动作业的退出语义。
- 初版 `runtimeMu.Lock()` 不响应上下文，45 秒总超时不能中断锁等待。现为 provider 路径使用可取消的 `TryLock` 等待；状态查询整体 5 秒，更新整体 45 秒，前置元数据查询也在更新预算内。`TestProviderLockWaitRespectsCancellation` 用阻塞中的 apply 验证两个入口均按 80 毫秒请求期限退出。
- 未配置自动更新来源时，调度器只做 provider 表预检查，不再每 5 秒读取用户和 Stack 全量配置。元数据读取另有 5 秒上限。

独立竞态测试通过；另用临时评审测试验证了未完成的 provider 工作会阻止排空，而 provider 完成后不会等待无关 scheduler 作业。该临时测试已移除，未改动实现。

## 重点检查

- 来源配置持久化、全量保存保留运行状态、删除级联清理、导入和克隆身份隔离。
- 代理组允许只引用 provider，并可混合静态成员；配置生成保持旧 Stack 的输出兼容。
- 直连显式指定 `DIRECT`；系统代理转换为独立内部节点，复用 HTTP/SOCKS5 认证；指定 Stack 纳入依赖图。
- 手动与自动更新共享并发去重流程；实际更新与 apply 互斥；controller 连接读取已应用配置凭据。
- 更新失败保留上次成功状态；原生核心缓存保持；停止 Stack 和未应用来源不能直接刷新。
- API 沿用 session/CSRF；订阅 URL、更新错误、预览、差异及 provider 错误日志脱敏。
- 检查核心 v1.19.34 源码，代理组配置的健康检查 URL/周期会注册到引用的 provider；没有单独生成 provider `health-check` 不妨碍该版本的自动组检测。

## 独立执行的验证

```text
go test ./internal/configdoc ./internal/store ./internal/app ./internal/api -run Provider -count=1
PSW_TEST_MIHOMO=/tmp/psw-mihomo go test ./internal/app -run '^TestNativeProvider' -count=1
go test -race ./internal/app ./internal/mihomoapi -run 'Provider|SchedulerRunsDueJobs' -count=1
node --test tests/stack-topology.test.mjs  # 在 web/ 下执行
```

以上通过。真实核心集成包含直连、带认证 HTTP 代理、带认证 SOCKS5 代理、筛选、失败保留旧节点、成功刷新和缓存写入。

## 结论与验证边界

初次评审的三项发现和交付前补充复审的生命周期问题均已修复并完成针对性复验，当前未发现剩余明确阻断问题。

独立原生核心验证针对 Mihomo v1.19.34；旧版本兼容性依赖安装核心的配置检查。Linux/systemd 的实际跨 Stack 启动时序未在本次独立评审环境中运行。自动调度由管理服务负责，管理服务停止期间不会执行周期更新，运行中的核心仍可继续使用已有节点。
