# 上游订阅节点详情独立评审

- 日期：2026-11-17
- 基线：`d354d46`。
- 范围：本轮节点概要和详情服务、缓存读取、API、`ProvidersPanel.vue` 及相关测试；不扩大到既有 provider 更新功能。
- 方式：独立读取差异和新增文件，检查真实核心字段定义，反馈问题后复核修复，执行针对性验证。评审未修改实现。

## 发现与修复

| 等级 | 问题与触发条件 | 修复复核 |
| --- | --- | --- |
| P1，已修复 | 默认凭据脱敏遗漏 TLSMirror `primary-key` 等传输密钥。包含此类字段的节点会在 `reveal=false` 下暴露密钥，绕过明确显示凭据及审计流程。已核对 Mihomo v1.19.34 的 `TLSMirrorOptions.PrimaryKey` 为真实核心配置字段。 | `internal/app/provider_nodes.go:199` 对规范化后以 `key` 结尾且非 public 的字段保守遮蔽，并覆盖 seed、authstring、protocolparam 等字段。服务回归同时断言默认详情不含 primary-key/seed 明文、明确显示时保留原值，独立竞态测试通过。 |
| P2，已修复 | 详情顶部使用点击时的列表行快照，重新读取的 YAML 使用新响应；同名节点在自动更新中变更地址后，两处可能显示不同服务器或端口。 | `web/src/views/stack/ProvidersPanel.vue:82` 在响应版本检查后同步 `selectedNode = data.node`，摘要与 YAML 来自同一份详情响应。静态复核通过。 |

## 关键路径复核

- 缓存路径来自已应用的 provider 配置；保存新订阅 URL 但未应用时仍读取旧配置对应的缓存，已有服务测试覆盖。
- 来源必须属于指定 Stack，并且 Stack 正在运行、provider 已应用。详情节点必须同时存在于核心当前筛选后的成员集合与缓存配置中，被排除节点不能通过手工提交名字读取。
- 缓存路径先清洗，拒绝绝对路径、目录穿越及非 `providers/` 路径；`os.OpenInRoot` 将读取限制在所属 Stack 工作目录，逃逸符号链接被拒绝。读取上限为 16 MiB；缓存不可用或解析失败的错误不回显原内容。
- 缓存配置仅存在于服务层，每次请求重新解析，默认递归遮蔽后编码为 YAML；一次遮蔽不会污染下一次明确显示请求。
- 明确显示凭据先写 `AuditSecretView`，失败则不返回明文。API 沿用 session/CSRF，并在成功响应中返回 `Cache-Control: no-store`。
- 关闭详情、切换 Stack/来源及组件销毁时清空详情并取消请求；新请求同时递增版本，迟到响应不能覆盖新选择。隐藏凭据操作会立即丢弃旧详情，再请求遮蔽版本。

## 独立验证

以下均通过：

```text
go test ./internal/app ./internal/api -run 'ProviderNode' -count=1 -v
go test -race ./internal/app ./internal/api -run 'Provider(Node|Cache)' -count=1 -v
PSW_TEST_MIHOMO=/tmp/psw-mihomo go test ./internal/app -run '^TestNativeProviderDownloads$' -count=1 -v
git diff --check
```

覆盖凭据遮蔽与审计、未应用 URL、筛选成员限制、来源归属、停止状态、目录穿越、逃逸符号链接及坏缓存。真实 Mihomo v1.19.34 集成分别通过直连、带认证 HTTP/SOCKS5 路径生成缓存，再经应用服务读取节点概要和详情。

## 最终结论

两项实质发现均已修复，当前未发现剩余明确阻断问题。前端请求失效和摘要同步完成代码复核；本独立评审未重复浏览器交互验证或全量构建。非 Clash/Mihomo YAML 的缓存格式会按设计明确返回不支持节点详情的提示。
