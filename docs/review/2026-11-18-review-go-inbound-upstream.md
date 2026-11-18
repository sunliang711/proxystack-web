# 通用入站上游引用独立评审

日期：2026-11-18。评审由未参与后端、前端实现的独立 agent 执行。

## 最终结论

通过。评审发现的 1 个 P2 问题已修复并复核，当前未发现未解决的 P1/P2 问题。

## 范围与基线

- 本次工作区差异中的配置模型、编译与校验、跨实体操作、导出、引用图、Mihomo 生成、SQLite 迁移与 sqlc 查询、服务层成员同步和 API 摘要。
- 前端上游表单、成员选择、聚合判定、拓扑，以及相关 Go、组件和拓扑回归测试。
- 依据 `code-review-go` 技能及项目 Go 后端、安全、API 规范，重点检查行为正确性、凭据处理、兼容性、事务与引用完整性；保持项目既有技术栈。
- 未审查或修改既有未跟踪的 `.claude/` 内容。

## 发现与处理

| 等级 | 位置 | 问题与影响 | 处理结果 |
| --- | --- | --- | --- |
| P2 | `web/src/views/stack/StackEditView.vue` 的 `memberOptions` | 通过待编辑的 `members` 保留没有默认 SOCKS5 relay 的手工引用成员，会在取消勾选时立即移除该选项，导致保存前无法重新选择。 | 已改为依据 Stack 中现有上游引用保留选项；独立复核实现，并重新执行取消后仍可重选的组件回归测试，通过。 |

主实现另外补齐了选定 Stack 导出的连接用户依赖：`IncludeReferencedUsers=true` 时收集通用引用的 `upstream.User`。独立复核该实现，并执行包含 JSON/YAML 往返的 `TestInboundUpstreamExport`，通过；引用的目标 Stack 沿用原有规则，不自动附带。

## 核对结果

- `xray-inbound` 与旧 `xray-socks5` 共存；旧类型继续只允许 SOCKS5，旧生成路径保留。
- SOCKS5/HTTP 复用入站级认证；VMess/Shadowsocks 使用目标入站已关联的用户档案，多用户缺少选择或选择失效会被校验拦截。
- VMess 的 raw/TCP 名称转换及 WS/gRPC 参数、Shadowsocks 的 SS2022 服务端与用户密钥组合均有回归覆盖；HTTP 不输出 UDP 转发字段。
- 连接地址来自本地监听地址并规范化通配地址，不使用订阅发布地址。
- 新引用进入依赖图和环检测；成员同步、改名、克隆、拓扑与聚合识别涵盖新旧两类引用。
- 用户和档案通过外键保存；整体保存先清理上游再重建用户／档案，未发现新增外键破坏保存顺序的问题。API 摘要只增加用户引用，不包含 UUID、密码。
- 用户／档案改名同步上游选择；删除档案时与入站一起回落 default。删除仍被上游显式引用的用户会被全局校验保护，需要先调整上游引用，不会静默切换至其他账户。

## 独立执行的验证

- `go test ./internal/configdoc ./internal/store ./internal/graph ./internal/generator/mihomo`：通过。
- `go test ./internal/configdoc -run 'TestInboundUpstream' -count=1`：通过，包含协议、用户选择、引用操作与导出测试。
- `go test ./internal/api -run TestInboundUpstreamAPI -count=1`：通过。
- `npm run typecheck`：通过。
- `node --test tests/upstreams-enabled.test.mjs tests/stack-topology.test.mjs tests/inbound-reference-members.test.mjs`：12 项通过；成员选项修复后再次单独执行成员页面测试，通过。
- `git diff --check`：通过。

主实现另报告 `go test ./...` 全部通过、前端构建通过。全量前端测试为 130 项中 129 项通过，1 项失败来自设置通知分组的数据库大小告警阈值搜索索引；该失败涉及的设置实现与测试未包含本次差异。

## 残余验证边界

- 本次验证以生成配置、API、SQLite 和组件回归为主，未使用真实 Xray/Mihomo 进程建立四种协议的网络连接，未进行线上部署。
- 用户选择用于目标入站的实际认证；若该用户在目标 Stack 被暂停或限额阻断，其引用连接也会受到相同约束。
