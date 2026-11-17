# 上游订阅节点详情交付

## 结果与使用

进入 Stack → Mihomo → 上游 → 上游订阅 → 节点，列表显示节点名称、协议、服务器和端口；点击「详情」可查看只读 YAML，其中保留 TLS、SNI、传输及协议选项。

默认遮蔽认证字段和传输密钥。「显示完整配置」会经带 session/CSRF 的请求获取原值并写入查看凭据审计；「隐藏凭据」、关闭或切换节点时清除此前明文。列表本身不返回连接凭据。

## 实现

- 新增 `internal/app/provider_nodes.go`：按需读取已应用 provider 的最近成功缓存，并以核心当前筛选后的成员限制节点访问。缓存限制为所属 Stack 的 `providers/` 目录，使用 `os.OpenInRoot` 防止目录穿越和逃逸符号链接；读取上限 16 MiB。
- 扩展 `internal/api/providers.go`：节点概要 GET、单节点详情 POST。成功响应设置 `Cache-Control: no-store`。
- 扩展 `ProvidersPanel.vue`：地址列、详情面板、只读 YAML、明文显示/隐藏、请求取消与版本检查。成功响应同时刷新顶部摘要，避免节点更新后摘要和 YAML 不一致。
- 同步 OpenAPI、TypeScript 类型、README 和相关服务/API/原生核心测试。
- 不修改数据库结构、依赖或现有业务流量配置。

新增接口：

```text
GET  /api/stacks/{id}/providers/{provider_id}/nodes
POST /api/stacks/{id}/providers/{provider_id}/nodes/detail
```

详情请求体 `name` 是概要接口返回的节点 key，`reveal` 控制是否明确显示凭据。错误不会回显原始缓存内容、路径或订阅凭据。

## 验证

- Go 全量测试、vet 通过。
- ProviderNode/ProviderCache 服务及 API 竞态检查通过。
- 真实 Mihomo v1.19.34 集成通过：通过直连、带认证 HTTP/SOCKS5 拉取生成的缓存，能由应用服务读取节点地址及配置。
- 覆盖默认递归脱敏、primary-key/seed 等传输密钥、明确显示及审计、未应用 URL 隔离、已过滤成员限制、错误归属、停止状态、目录穿越、逃逸符号链接和坏缓存。
- 前端类型检查和生产构建通过。
- ego-browser 临时实例验收：节点地址/端口、默认遮蔽、明确显示及再次隐藏；列表加载后修改同名节点地址，详情顶部与 YAML 均显示新地址；TLS 信息可见、认证密码及传输密钥默认不显示。
- 独立 agent 发现的传输密钥脱敏和详情摘要一致性两项问题均已修复并复验，见 `docs/review/2026-11-17-review-go-provider-node-details.md`。

## 边界

详情读取当前已应用的 Clash/Mihomo YAML 缓存；尚未应用、Stack 停止、缓存缺失或非 YAML 格式时明确提示。节点详情是读取时的缓存快照，打开详情不会触发订阅下载或修改节点。

发布版本按现有补丁号顺延为 `v0.11.5`，提交与标签推送到 origin/main。保留原有未跟踪 `.claude/` 内容，不纳入提交。

## ChangeLog

### [v0.11.5] Added

- 上游订阅节点地址、端口和配置详情查看，支持默认凭据遮蔽与明确显示审计。
