# 用户内网订阅节点

## 任务背景

普通订阅节点使用系统设置中的对外地址。局域网用户通过公网地址回连本机 Stack 时可能经过 Hairpin NAT，客户端入站链路出现高重传，而管理端诊断测速从服务器内部发起，不经过该链路。

本次增加可选的内网节点地址，并允许管理员按用户授权。授权用户继续使用原订阅链接，同时获得本机 Stack 普通节点的内网副本；公网节点保持不变。

## 编排方案

- 主 Agent 负责需求确认、实现、测试、评审问题修复和最终集成。
- 独立 Review Agent 在实现稳定后审查节点边界、迁移兼容、订阅拓扑漂移和测试覆盖。
- 文档 Agent 仅根据最终实现与评审结论生成本交付文档。

## 实现方案

### 配置与授权

- 系统基本设置新增 `internal_host`，接受域名或 IP，未配置时不启用内网节点能力。
- 用户新增 `internal_nodes` 开关，默认关闭；开启时要求已经配置 `internal_host`。
- 内网节点名称在原展示名后追加固定后缀 ` [LAN]`，节点 ID 和 tag 使用独立的 `:lan` 后缀。
- 现有对外地址、订阅 URL、token 和公网节点不变。

### 节点生成边界

- 只复制本机 Stack 已应用版本生成的普通订阅节点。
- 内网副本仅替换 `server`，端口、协议、凭据和传输参数沿用公网节点。
- 直连 upstream、手工节点和远端节点源不生成内网副本。
- 用户逐 Stack 暂停在复制前过滤，因此公网和内网节点同步移除。
- LAN 副本只进入最终用户订阅，不进入节点导出接口，避免内网地址传播到其他管理实例。

### 冲突降级

配置保存时会校验当前拓扑追加 LAN 副本后的节点名称和 ID。实际发布时节点来自已应用 revision，可能与当前配置存在拓扑差异，因此运行时还会以实际普通节点、直连节点和手工节点作为保留集合再次检查。

若 LAN 副本与现有名称或 ID 冲突：

- 保留原普通、直连或手工节点；
- 仅跳过冲突的 LAN 副本；
- 记录 subscription build warning；
- 不因单个副本冲突阻断整份订阅的预览、签发和发布。

远端节点继续使用既有合并规则处理重名。

## 文件与配置变更

### 后端

- `internal/configdoc/document.go`：增加 `BasicSettings.InternalHost` 和 `User.InternalNodes`。
- `internal/configdoc/internal_subscription.go`：生成授权用户的 LAN 节点副本。
- `internal/configdoc/validate.go`、`internal/configdoc/subscription.go`：校验内网地址、授权依赖和节点冲突。
- `internal/app/subscription.go`：在实际已应用节点集上生成 LAN 副本，隔离冲突，并保持节点导出不含 LAN 节点。
- `internal/store/config.go`、`internal/store/settings.go`：读写新增设置和用户字段。
- `internal/store/queries/config.sql`、`internal/store/dbq/`：同步 SQL 查询与生成代码。

### 数据库

- 新增迁移 `00024_user_internal_nodes.sql`。
- `users.internal_nodes` 为非空整数列，默认值为 `0`。
- 从 schema 23 升级时，已有用户保持未授权状态。

### 前端与接口

- `web/src/views/SettingsView.vue`：基本设置增加“内网节点地址”。
- `web/src/views/UserDetailView.vue`：用户订阅设置增加“内网节点”开关；未配置地址时禁用并提供设置入口。
- `web/src/util/user-tabs.ts`：将 `internal_nodes` 纳入订阅标签页的未保存与错误状态。
- `web/src/composables/useSettingsSections.ts`：补充生效说明和搜索索引。
- `web/openapi.json`、`web/src/api/schema.d.ts`：重新生成接口定义。
- `docs/user-guide.md`：补充 Hairpin NAT 场景和用户授权说明。

未新增第三方依赖。

## 测试结果

### Go

- `go test ./...`：修复全部评审问题后最终全量通过。
- `internal/app`、`internal/configdoc`、`internal/store` 定向测试通过。
- 新增覆盖：
  - 授权隔离、稳定 ID/tag、固定 `[LAN]` 后缀和原节点不变；
  - 内网地址格式及授权依赖校验；
  - 当前拓扑节点名称碰撞；
  - Clash、Premium Clash、Surge 三种订阅格式；
  - 直连和手工节点不复制；
  - LAN 节点不进入节点导出；
  - 已应用拓扑与当前拓扑不一致时的旧普通节点冲突；
  - 已应用拓扑与当前手工节点冲突；
  - schema 23 升级到 24 后默认关闭，并验证字段读写。

### 前端

- `npm run typecheck`：通过。
- `npm run build`：通过。
- `test:inbounds`、`test:subscription-publish`、`test:publish-flow`：通过。
- `test:settings` 有一项既有失败：`notify` 分组的“数据库大小告警阈值”未加入全局搜索索引，与本次功能无关；按最小变更原则未处理。

## 独立评审

### 问题一：内网节点进入节点导出

初版将 LAN 副本写入 `snapshot.local`，节点导出接口会把内网地址传播到远端节点源。

处理：`snapshot.local` 只保留原始普通节点和既有直连节点；LAN 副本只加入最终订阅节点，并增加节点导出断言。

### 问题二：已应用拓扑中的名称冲突

当前配置校验无法覆盖尚未 apply 的拓扑差异。已应用 revision 中的旧节点可能与新 LAN 副本重名，导致整份订阅构建失败。

处理：运行时使用实际普通、直连和手工节点预留名称与 ID；冲突时仅跳过 LAN 副本并记录 warning。分别增加旧普通节点冲突和手工节点冲突回归测试。

### 复审结论

独立复审未发现新的阻断或警告问题；未发现凭据泄漏、鉴权缺失或注入风险。

## 风险与后续建议

- 用户离开内网后，`[LAN]` 节点不可达，这是同时下发公网与内网节点的预期行为。
- 内置自动测速或负载均衡组会包含 LAN 节点，客户端在外网时会把它判为不可用，但公网节点仍可使用。
- 若发生名称冲突，该 LAN 副本不会生成；管理员可根据 subscription build warning 调整节点展示名。
- 修改 `internal_host` 或用户授权后，需要按现有流程重新发布订阅包，在线订阅端导入新包后生效。
