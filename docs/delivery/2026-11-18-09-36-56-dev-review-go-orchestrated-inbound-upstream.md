# 通用入站上游引用交付说明

日期：2026-11-18。状态：实现完成，独立评审通过。

## 背景与实现

原来的 Mihomo 引用上游只支持其他 Stack 的 Xray SOCKS5 入站。本次新增 `xray-inbound`，可引用 SOCKS5、HTTP、VMess、Shadowsocks；原有 `xray-socks5` 配置和生成路径继续兼容，仍限定 SOCKS5。

- 引用使用目标入站的本机可达监听地址和端口，通配地址规范化为回环地址，不使用订阅发布地址。
- SOCKS5、HTTP 读取入站级认证。VMess 和带用户的 Shadowsocks 可指定 `user`、`profile`；空档案表示 default，单用户可自动解析，多用户未选择或选择失效时拒绝保存。无用户的 Shadowsocks 使用入站自身密码。
- VMess 支持 TCP、WebSocket、gRPC，并将 Xray 的 `raw` 名称转换为 Mihomo 的 `tcp`；Shadowsocks 按现有逻辑组合 SS2022 服务端与用户密钥；HTTP 不生成 UDP 转发字段。
- 通用引用进入依赖图、环检测、聚合判定和拓扑；Stack、入站、用户、档案改名同步引用，克隆按原规则改写指向自身的引用。
- auto 成员快捷新增仍使用默认 SOCKS5 relay。已有手工通用引用会保留在成员列表中，保存成员不替换其入站或账户；取消勾选后，保存前仍可重新选回。
- 选定 Stack 导出且启用“附带引用用户”时，纳入上游显式选择的连接用户；引用目标 Stack 沿用既有规则，不自动附带。JSON/YAML 保留新类型和用户选择。

## 编排与文件范围

主 agent 负责后端实现、集成、测试和评审修复；前端 agent 负责表单、成员和拓扑；未参与实现的独立 agent 完成评审和复核，最后由文档 agent 整理本说明。

| 范围 | 主要文件与变更 |
| --- | --- |
| 配置与领域 | `internal/configdoc/{document,compile,validate,ops,export}.go`、`internal/domain/{models,upstream_refs}.go`：类型、选择校验、引用操作与导出 |
| 生成与依赖 | `internal/generator/mihomo/{config,inbound_proxy}.go`、`internal/graph/dependencies.go`：协议转换、本机连接与依赖图 |
| 存储 | `internal/store/config.go`、`queries/config.sql`、`dbq/`、迁移 `00027_mihomo_upstream_user_refs.sql`：新增 `ref_user_id`、`ref_profile_id` 外键并同步 sqlc 代码 |
| 服务与 API | `internal/app/config.go`、`internal/api/config.go`：成员保留、入站用户摘要；摘要不包含 UUID 或密码 |
| 前端 | `UpstreamsTab.vue`、`StackEditView.vue`、`util/flow.ts`、OpenAPI 与类型产物：四协议及用户／档案选择、兼容旧引用、成员和拓扑 |
| 测试与说明 | 新增 Go 协议／选择／API／存储／导出回归，扩展前端组件与拓扑测试，更新 `docs/user-guide.md` |

未引入或升级依赖。SQLite 新迁移随项目既有迁移流程执行。

## 验证结果

| 验证 | 结果 |
| --- | --- |
| `go test ./...` | 全部通过 |
| 最终导出补丁后，配置、存储、API、app 四个包重跑 | 通过 |
| 前端 `npm run build`（包含类型检查） | 通过 |
| 成员修复后 `npm run typecheck` | 通过 |
| 上游、拓扑、成员相关前端测试 | 12 项通过 |
| 全量前端测试 | 130 项中 129 项通过；1 项既有失败，见下文 |
| `git diff --check` | 通过 |

全量前端唯一失败为 `web/tests/settings-sections.test.mjs:32`：通知设置的“数据库大小告警阈值”未加入搜索索引。相关 `SettingsView.vue`、`useSettingsSections.ts` 和该测试均与 HEAD 无差异，本次未修改。

## 独立评审与处理

[独立评审报告](../review/2026-11-18-review-go-inbound-upstream.md)结论为通过，当前无未解决的 P1/P2 问题。

评审发现 1 个 P2：手工引用且没有默认 SOCKS5 relay 的成员取消勾选后，选项立即消失。已改为依据 Stack 中现有引用保留选项，补充取消后可重选回归，并由 reviewer 独立复核通过。导出连接用户依赖的补充也已独立复核，导出及 JSON/YAML 往返测试通过。

## 使用与验证边界

进入 Stack 编辑页 → 上游 → 添加或编辑上游 → 选择“引用其他 stack 的入站” → 选择目标 Stack、入站及适用的用户／档案 → 暂存修改 → 保存；按现有应用流程使运行配置生效。

后续修复已纠正首次交付中的用户删除遗漏：删除用户时同步清空相关上游的 `user`、`profile`，随后按剩余入站用户解析；剩一个用户时自动选择，无用户的 Shadowsocks 使用入站密码。无用户的 VMess 或多个候选用户仍会触发校验，需要先调整引用或入站。删除档案时，入站和上游选择一起回落 default。所选用户在目标 Stack 上被暂停或限额阻断时，引用连接也受相同约束。

本次已验证配置生成、API、SQLite 和组件行为，未使用真实 Xray/Mihomo 进程建立四种协议的网络连接，未部署到线上。

## [Unreleased]

### Added

- Mihomo 上游支持引用其他 Stack 的 HTTP、VMess、Shadowsocks 入站及其用户档案，并兼容现有 SOCKS5 引用。
