# 通用入站引用后续修复独立评审

日期：2026-11-18。评审由未参与本轮实现的独立 agent 执行。

## 结论

通过。当前补丁中未发现未解决的 P1/P2 问题。用户指出的删除引用遗漏及各项前端、档案表示问题均有代码依据，本轮修复与其建议相符。

此前报告将显式引用用户的删除失败描述为引用保护，这个判断不当：项目的 `DeleteUser` 已有统一清理用户引用的流程，本次应修复该流程遗漏的上游连接身份，而非将遗漏视为预期行为。

## 审查范围与基线

- 配置操作：`internal/configdoc/ops.go`、`codec.go` 及对应回归测试。
- 持久化：`internal/store/config.go`、`upstream_refs_test.go`。
- 服务事务与快照：新增 `internal/app/inbound_upstream_test.go`，并核对 `DeleteUser`、`mutateConfig`、`writeRevision` 调用顺序。
- 前端：`web/src/views/stack/UpstreamsTab.vue`、`web/tests/upstreams-enabled.test.mjs`。
- 遵循 `code-review-go` 技能及项目 Go 后端、安全、API 规范；重点核对行为、凭据、旧配置兼容性和回归覆盖。未修改实现源码或无关的 `.claude/` 文件。

## 核实与修复检查

| 原问题 | 独立复核结果 |
| --- | --- |
| 删除用户后上游仍保存该用户／档案 | `RemoveUserRefs` 只清理匹配用户的引用型上游两项身份字段，与入站、直连用户等引用一起进入原有删除事务；配置与服务测试覆盖 VMess 剩一个用户、Shadowsocks 剩零用户时删除成功并可生成配置。 |
| 零用户 VMess 无法暂存但缺少原因 | 表单新增明确的 `NAlert`，提示先为目标 VMess 入站添加用户；原有阻止暂存的校验继续生效。 |
| 目标列未显示连接身份 | 引用目标链接下显示所选 `user / profile`，空档案显示 `default`；仅展示身份名称，没有新增密码或 UUID 展示。 |
| 打开旧引用静默改变类型 | `open` 保留原类型；单选框通过计算属性显示统一入口，修改名称或启用状态后仍保存 `xray-socks5`。选中非 SOCKS5 目标时才升级为通用类型，切换到外部节点仍清除连接身份。 |
| `default` 在内存、快照、数据库间不一致 | `Stack.Normalize` 在服务校验和修订快照之前归一为空；直接调用 `SaveDocument` 时也同步归一调用方文档。服务测试同时比较保存返回、修订 JSON 与数据库重载结果，存储测试覆盖未预先调用 Normalize 的情况。 |

## 独立执行的验证

- `go test ./internal/configdoc ./internal/store ./internal/app -run 'TestRemoveInboundUpstreamUserRefs|TestInboundUpstreamDefaultProfileNormalization|TestUpstreamUserReferenceRoundTrip|TestDeleteInboundUpstreamUser|TestInboundUpstreamDefaultProfileSnapshot' -count=1`：通过。
- `node --test tests/upstreams-enabled.test.mjs`：7 项通过，覆盖空 VMess 提示、连接身份显示、旧类型普通编辑与协议切换。
- `npm run typecheck`：通过。
- `go test ./internal/configdoc -run TestInboundUpstreamUserSelection -count=1`：通过，原有多用户选择校验继续生效。
- `git diff --check`：通过。

主实现另报告最终 `go test ./...` 全部通过、`npm run build` 通过、上游／拓扑／成员相关前端 14 项通过；本评审独立执行的范围如上。

## 语义与验证边界

- 清空被删除的连接身份后，沿用现有解析规则：剩一个用户时可自动选择，Shadowsocks 无用户时使用入站密码；VMess 无用户或存在多个候选用户时仍拒绝无效／不明确的配置。没有增加绕过全局校验的删除路径。
- 本轮未新增依赖、数据库迁移、认证接口或日志输出。
- 本次独立验证为配置、SQLite、服务事务和组件测试，未实际启动 Xray/Mihomo 建立网络连接；前端提示没有进行真实浏览器视觉验收。
