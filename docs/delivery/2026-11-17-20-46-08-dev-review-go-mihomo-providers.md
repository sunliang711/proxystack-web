# Stack Mihomo 上游订阅交付

## 目标与结果

Stack 可以通过 Mihomo 原生 HTTP provider 使用订阅中的动态节点。支持直连、跟随系统下载代理、手动刷新和按来源自动刷新；代理组使用 `use` 引用来源，可以混合静态节点。包含和排除筛选均使用 Mihomo 原生正则表达式。

## 使用方式

1. 在 Stack 编辑页的「Mihomo → 上游 → 上游订阅」添加来源。
2. 填写名称和 Clash/Mihomo 订阅地址，选择直连或系统下载代理；按需填写包含/排除正则和加入的代理组。
3. 自动更新可关闭；开启时周期为 60～2592000 秒，默认 3600 秒。
4. 暂存后保存并应用。之后的节点刷新通过「立即更新」或定时触发完成，无需重启 Stack。
5. 来源列表展示当前节点、最近尝试、最近成功和错误。节点详情只返回名称及协议，不返回连接凭据。

修改来源地址、筛选、下载方式或代理组引用需要应用；自动更新开关和周期保存后影响面板调度。系统下载代理变更会令相关 Stack 生成物出现待应用变更。手动更新读取已应用配置，页面有未保存修改时禁用更新按钮。

## 实现与影响范围

- `internal/configdoc`：来源字段、分组引用、默认值、校验、身份与克隆隔离、脱敏和加密导出。
- `internal/store`：迁移 `00025_mihomo_providers.sql`，来源配置及更新状态；代理组增加 `provider_use`。sqlc 查询代码已重新生成。
- `internal/domain`、`internal/graph`、`internal/generator/mihomo`：原生 provider、内部下载代理节点、分组 `use`、跨 Stack 下载依赖与循环检查。
- `internal/mihomoapi`：读取 provider 元数据、触发原生更新；屏蔽核心错误中可能包含的凭据。
- `internal/app`：自动/手动统一更新、请求合并、与 apply 互斥、读取已应用 controller 凭据、取消及关停排空。
- `internal/api`：沿用 session/CSRF 的状态和更新接口；摘要增加来源名称。
- 前端：来源表单、节点查看、更新状态、代理组选择、列表拓扑和详情流向；OpenAPI 与 TypeScript 类型已重新生成。
- `README.md`：补充功能入口。

新增 API：

```text
GET  /api/stacks/{id}/providers
POST /api/stacks/{id}/providers/{provider_id}/update
```

无新增 Go/npm 依赖、框架或环境变量。现有无来源配置的生成物保持兼容。

## 更新与失败语义

- 原生 provider 使用 `interval: 0`，周期调度由面板负责，避免两套定时器。
- 关闭自动更新后仍可手动更新。没有缓存的首次启动仍会初始化拉取。
- 获取方式在每个来源独立选择；系统下载代理复用自定义 HTTP/SOCKS5 或指定 Stack 的入口及认证。
- 自定义 SOCKS5 模式要求启用系统设置的远端 DNS，避免静默改变现有本地解析选项的含义。
- 不自动从代理回退到直连。自引用和跨 Stack 循环会被配置校验拦截。
- 更新失败保留核心中的旧节点和上次成功时间。URL 改变使用不同缓存路径，避免误用另一份订阅。
- 一个来源的并发更新共享一次核心调用。状态查询最多 5 秒；更新整体最多 45 秒，等待 apply 锁时也可取消。
- 关停时关闭 provider 新任务入口并排空专属工作组，不改变其他既有手动任务的退出语义。
- 面板停机期间不执行自动刷新，独立运行的 Mihomo 继续使用已缓存节点。

正则表达式交给已安装的 Mihomo 校验；配置非空筛选前需要安装核心。复用了核心引擎，因此支持原生有效的高级语法，不以 Go/JavaScript 的正则规则代替。

## 验证

- `go test ./...`：通过。
- `go vet ./...`：通过。
- `PSW_TEST_MIHOMO=/tmp/psw-mihomo go test -race ./internal/app ./internal/api ./internal/configdoc ./internal/store -run 'Provider|NativeProvider' -count=1`：通过。
- 原生 Mihomo v1.19.34 集成：直连、带认证 HTTP 和 SOCKS5、远端域名解析、包含/排除筛选、失败保留、手动热更新及缓存写入通过。
- 原生正则回归：`a{1001}` 有效、`(?P<region>HK)` 无效、高级排除模式校验通过。
- 克隆、存储身份保留、跨 Stack ID 唯一性、停止 Stack、并发请求、待应用凭据隔离、锁等待取消等测试通过。
- 前端类型检查和生产构建：通过；动态拓扑测试 4 项通过。
- 前端全量测试：117 项中 116 项通过，1 项既有失败。`settings-sections.test.mjs` 的设置搜索索引缺少“数据库大小告警阈值”；该测试及两个输入文件均与 HEAD 完全一致，HEAD 中也存在标签但缺少索引。本次未修改无关设置功能。
- ego-browser 临时实例验收：新增来源、选择系统下载代理、关闭自动更新、保存后重载、URL 仍为密码框且为遮蔽值、自动更新保持关闭，均已观察确认。
- `git diff --check`：通过。

## 独立评审

见 `docs/review/2026-11-17-review-go-mihomo-providers.md`。独立 agent 发现的克隆身份、正则引擎、动态流向展示问题均已修复并复验；收尾另补了 provider 专属排空和可取消锁等待。以最终评审报告为准。

## 部署与验证边界

部署新二进制启动时沿用现有迁移流程执行数据库迁移；修改后的 Stack 仍需应用配置。本次按用户要求提交并创建本地 `v0.11.4` 标签，未推送或部署。

已验证核心版本为 v1.19.34；旧版本兼容性依赖实际安装核心的配置检查。未在本环境实测 Linux/systemd 跨 Stack 冷启动时序，依赖图排序和循环检测由测试覆盖。

## ChangeLog

### [Unreleased] Added

- Stack 上游订阅：原生 provider、直连/系统下载代理、自动/手动更新、原生正则筛选、动态代理组引用和运行状态。
