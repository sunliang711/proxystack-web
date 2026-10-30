# proxystack-web 方案

## 1. 目标与边界

proxystack-web 是一个只运行在 Linux（systemd）上的单进程服务，自带 Web 前端。首次安装由一条安装脚本完成，之后的全部运维操作都在 Web 端进行：配置用户和 stack、生成并下发 xray/mihomo 配置、启停服务、查看日志、管理依赖二进制、提供订阅、监控流量、执行定时任务。

- 配置的唯一来源是 SQLite 数据库。
- 不兼容任何现有配置文件和目录布局。配置在 Web 端手工录入，或从本项目导出的文件导入（见 5.5）。
- 功能覆盖参考项目的全部功能点（见第 3 节映射表），在此基础上新增流量监控、配额与到期、定时任务、配置版本与回滚、操作审计。
- 不支持 macOS/launchd 作为生产平台。开发阶段允许在 macOS 上以「开发模式」运行（服务管理和 xray 统计接口使用假实现），便于前后端联调。

### 1.1 参考项目

功能点、字段定义、校验规则和生成器逻辑以参考项目为准：

```text
/Users/eagle/Sync/proxy/proxystack-go
```

该项目是 CLI 形态（`psctl` 管理 agent，`pssub` 提供订阅 HTTP 服务），配置存放在 YAML 文件中。参考项目的文档生成于 2026-06，之后代码又有演进（例如 inbound 的 `ws_opts`、`grpc_opts`、xray policy 的 levels/system、用户启停热生效），**字段与校验以代码为准，文档用于理解意图**。查阅指引见第 13 节。

流量监控的参考实现：

```text
~/.local/apps/init/tools/xray/traffic
```

它是一个 Python 脚本，每小时通过 `xray api statsquery -reset=true` 采集增量，写入 SQLite，再按日、月聚合。本方案沿用其数据维度、聚合层级、保留策略和时区处理，采集方式改进见第 8 节。

## 2. 总体架构

### 2.1 进程与服务

| systemd unit | 运行身份 | 职责 |
| --- | --- | --- |
| `proxystack-web.service` | `proxystack-web` | 管理服务本体：Web 管理端、订阅端、调度器、流量采集器 |
| `psw-xray@<stack>.service` | `proxystack-web` | 单个 stack 的 xray 进程（模板 unit） |
| `psw-mihomo@<stack>.service` | `proxystack-web` | 单个 stack 的 mihomo 进程（模板 unit） |

设计要点：

- **代理进程不是管理服务的子进程。** xray/mihomo 由 systemd 模板 unit 承载，管理服务重启、升级或崩溃都不会中断代理流量。
- **管理服务不以 root 运行。** 安装脚本一次性写入一条 polkit 规则，只允许 `proxystack-web` 用户对 `psw-xray@*.service` 和 `psw-mihomo@*.service` 执行 start/stop/restart。管理服务通过 systemd D-Bus 接口操作这些 unit，不调用 `systemctl` 命令行。
- **不使用 `systemctl enable`。** 每个 stack 的期望运行状态存放在数据库中。管理服务启动时执行一次对账：期望运行而未运行的 unit 拉起，期望停止而在运行的 unit 停止。因此不需要授予写 `/etc/systemd/system` 的权限。
- **unit 文件是静态的。** 安装脚本写入两个模板 unit，`ExecStart` 指向固定路径下的二进制和 `runtime/generated/{xray,mihomo}/%i.*` 配置文件。新增 stack 不需要新增 unit 文件。模板 unit 带 `AmbientCapabilities=CAP_NET_BIND_SERVICE`，允许以普通用户监听 1024 以下端口。
- **日志读取。** 安装脚本把 `proxystack-web` 用户加入 `systemd-journal` 组，管理服务以 `journalctl -o json` 读取相关 unit 的日志，通过 SSE 推给前端。
- unit 名前缀 `psw-` 与参考项目的 `proxystack-*` 区分开，两者可以在同一台机器上共存，便于手工迁移。

### 2.2 目录布局

全部数据位于 `/opt/proxystack-web`，属主为 `proxystack-web:proxystack-web`，权限 `0750`，文件 `0640`：

```text
/opt/proxystack-web/
├── bin/                 proxystack-web、xray、mihomo
├── geo/                 geoip/geosite 数据
├── data/
│   ├── proxystack.db    SQLite（WAL 模式）
│   └── tls/             面板证书
├── runtime/
│   ├── generated/xray/<stack>.json
│   ├── generated/mihomo/<stack>.yaml
│   └── mihomo/<stack>/  mihomo 工作目录
├── backups/             数据库备份
└── downloads/           下载缓存
```

只有 unit 文件（`/etc/systemd/system`）、polkit 规则（`/etc/polkit-1/rules.d`）和 `/usr/local/bin/proxystack-web` 软链接位于该目录之外，均由安装脚本写入。

### 2.3 进程内模块

```text
HTTP 层（管理端 / 订阅端两个监听器）
   │
应用服务层（所有业务操作的唯一入口，返回结构化结果）
   ├── 配置仓储（SQLite，事务，版本快照）
   ├── 领域核心（模型、校验、引用图、生成器）——移植自参考项目
   ├── 运行时（生成物 diff、写盘、apply、热更新用户）
   ├── 服务管理（systemd D-Bus、日志）
   ├── 依赖管理（下载、校验、替换 xray/mihomo/geo、自更新）
   ├── 订阅（渲染 clash / premium clash / surge）
   ├── 流量采集器（xray gRPC StatsService）
   ├── 调度器（定时任务注册表、执行记录）
   └── 事件总线（unit 状态、任务进度、实时流量 → SSE）
```

所有修改类操作在应用服务层串行化：写配置走数据库事务；apply、依赖替换、用户启停共用一把进程内运行时锁，避免并发写生成物或并发重启同一 unit。耗时操作（下载、apply）作为「后台作业」执行，前端通过 SSE 查看进度。

### 2.4 技术选型

| 领域 | 选型 | 说明 |
| --- | --- | --- |
| 语言 | Go 1.25 | 与参考项目一致，便于移植 |
| HTTP | chi + Huma v2 | Huma 由 Go 类型生成 OpenAPI 3.1，前端据此生成 TS 类型 |
| 数据库 | SQLite，驱动 modernc.org/sqlite | 纯 Go 实现，保持静态编译 |
| 迁移与查询 | goose + sqlc | 迁移脚本内嵌；查询代码生成，类型安全 |
| systemd | github.com/coreos/go-systemd/v22/dbus | start/stop/restart、状态订阅 |
| xray API | xray-core 的 stats/proxyman command gRPC 客户端 | 直接 gRPC，替代参考项目调用 `xray api` 命令行 |
| 调度 | robfig/cron/v3 | 进程内 cron |
| 订阅模板 | pongo2（沿用参考项目） | 内置模板沿用 |
| 前端 | Vue 3 + TypeScript + Vite | |
| 组件库 | Naive UI | 表单控件齐全，TS 友好，内置暗色主题 |
| 图表 | ECharts | 流量趋势、实时速率 |
| 代码编辑 | CodeMirror 6 | YAML/JSON/模板编辑与只读预览 |
| API 客户端 | openapi-typescript + openapi-fetch | 由 OpenAPI 文档生成 |
| 打包 | `go:embed` 内嵌前端构建产物 | 单二进制发布 |

## 3. 功能映射

参考项目的每个命令在 Web 端都有对应入口，下表为完整映射。「不再需要」的条目是因为配置改存数据库或平台收窄为 Linux。

### 3.1 psctl

| 参考命令 | Web 端对应 |
| --- | --- |
| `setup local` / `setup all` | 安装脚本 + 首次启动初始化（建库、生成管理员密码、证书） |
| `setup deps`、`update mihomo\|xray\|geo\|all` | 「依赖」页：查看版本、选择版本、下载安装、冒烟验证、替换后重启受影响 stack、失败自动回滚（见 11.3） |
| `update self` | 「系统」页：检查新版本、一键自更新 |
| `version [mihomo\|xray\|geo]` | 「系统」页与页脚显示各组件版本 |
| `add NAME --template ...` | 「Stack」页：新建向导（选择模板、名称、成员、自动分配端口） |
| `clone SOURCE TARGET` | Stack 详情页「克隆」：复制全部子项、重新分配端口、只改写指向源 stack 自身资源的引用 |
| `config`（全局） | 「系统设置」页表单 |
| `config NAME` | Stack 编辑页表单 |
| `example ...` | 不再需要：表单直接给出默认值、可选值说明和模板预填 |
| `list` | 「Stack」列表：启用状态、角色、运行状态、xray/mihomo 端点、loopback 标识 |
| `member list/add/remove` | auto 角色 stack 的「成员」多选控件，保存时同步 upstream 和代理组 |
| `user list/enable/disable` | 「用户」页：每个用户在每个 stack 上的启停开关，热生效 |
| `remove NAME [--purge]` | Stack 删除：先停服务，再删除配置和生成物 |
| `validate` | 每次保存时在事务内全量校验，错误定位到字段 |
| `check` | 「待应用变更」面板：显示生成物 create/update/delete 的 diff 和将重启的服务 |
| `render model/xray/clash/sub` | Stack 详情页「生成预览」（xray JSON、mihomo YAML 只读视图）；订阅页「订阅预览」 |
| `export-config sub\|premium_sub\|surge_sub USER` | 用户详情页「订阅预览」，三种格式切换 |
| `start/stop/restart/status/enable/disable [TARGET]` | Stack 列表和详情页的操作按钮，支持整 stack 或单独 xray/mihomo；enable/disable 对应「期望运行状态」开关 |
| `logs [TARGET] -f` | Stack 详情页「日志」标签：实时滚动、按组件过滤、暂停、关键字过滤 |
| `service install/uninstall` | 不再需要：模板 unit 由安装脚本一次性写入 |
| `sub export` | 不再需要：订阅由同一进程直接提供；跨机器场景见「远端节点源」 |
| `sub validate-inputs` | 手工节点和远端节点源保存时校验 |
| `export` / `import`（原生备份） | 「备份」页与各列表页的配置导入导出（见 5.5）；整机迁移包导出与恢复（见 5.6）；另有数据库定时备份 |
| `add --from-file` | Stack 列表「导入」（见 5.5） |
| `doctor` | 「健康检查」页 |
| `ipinfo STACK` | Stack 详情页「出口 IP 检测」按钮（经该 stack 的 mihomo socks 监听查询） |
| `uninstall` | 卸载脚本 |

### 3.2 pssub

| 参考命令 | Web 端对应 |
| --- | --- |
| `serve` | 内置订阅监听器，独立端口 |
| `config` / `config show` / `config check` | 「系统设置 → 订阅」表单；token 改为每用户独立（见第 7 节） |
| `import BUNDLE` / `clear` | 「远端节点源」：从其他 proxystack-web 实例拉取节点（定时任务） |
| `input list/show/validate/edit/clone/remove` | 「手工节点」页：增删改查、克隆、导入导出 |
| `input set-host` | 手工节点批量操作「批量设置 server」 |
| `doctor` | 并入「健康检查」页 |
| `/health` | 订阅端保留 `/health` |

### 3.3 新增功能

- 流量监控：实时速率、按用户/inbound/出站维度的小时/日/月/年统计、CSV 导出。
- 用户配额与到期：超额或到期自动停用（热生效），周期重置自动恢复。
- 定时任务：内置任务 + 执行记录 + 手动触发。
- 配置版本：每次保存生成一个修订版本，可查看任意两个版本的差异并回滚。
- 操作审计：记录谁在何时对什么对象做了什么操作。
- 全量迁移：整机数据打包导出，在新机器安装时或 Web 端一次恢复，订阅链接与流量历史保持连续。
- 延迟测试：通过 mihomo controller 对上游节点测速。
- 通知（后续）：服务异常、配额告警推送到 Webhook/Telegram。

## 4. 数据模型

所有实体使用自增整数主键，引用关系一律用外键 ID，不用名字。因此用户、stack、inbound、upstream 改名时引用不会断，生成物里的 tag 在下一次 apply 时自动更新。

### 4.1 配置表

| 表 | 主要字段 | 说明 |
| --- | --- | --- |
| `settings` | key, value(JSON) | 全局设置，按分组存放：基本、端口池、默认值、安全、依赖源、下载代理、订阅、流量、面板 |
| `users` | id, name, enabled, uuid, ss_password, remark, display_template, tag, sub_token, quota_bytes, reset_mode, reset_day, expire_at, note | 订阅用户。`name` 唯一，满足标识符规则 |
| `user_profiles` | id, user_id, name, uuid, ss_password, remark, display_template, tag | 同一用户的多套凭据档案；`default` 档案即用户本身 |
| `stacks` | id, name, enabled, desired_running, role, labels(JSON), xray_enabled, xray_loglevel, xray_api_port, xray_stats_override, xray_policy_override, mihomo_enabled, mihomo_mode, mihomo_loglevel, controller_port, controller_secret, rule_profile, rule_final_ref | stack 主体 |
| `xray_outbounds` | stack_id, type, listener_id, server, port, username, password, private_direct | 每个 stack 一条出站 |
| `inbounds` | id, stack_id, position, name, protocol, listen, port, udp, sub, server, remark, display_template, region, tag, network, ws_path, ws_headers(JSON), grpc_service_name, method, password, auth_type, auth_username, auth_password, sub_user_id | xray 入站 |
| `inbound_user_refs` | inbound_id, user_id, profile_id, overrides(JSON), position | vmess/shadowsocks 入站引用的用户 |
| `mihomo_listeners` | id, stack_id, kind(socks/http), name, listen, port, auth_mode, users(JSON) | socks 必须一条，http 至多一条 |
| `mihomo_upstreams` | id, stack_id, position, name, type(raw/xray-socks5), raw_config(JSON), ref_inbound_id | 上游代理 |
| `mihomo_groups` | id, stack_id, position, name, type, url, interval, strategy | 代理组 |
| `mihomo_group_members` | group_id, position, member_kind(upstream/group/builtin), member_id, builtin | 组成员，有序 |
| `mihomo_rules` | id, stack_id, position, rule_type, value, target_kind, target_id, target_builtin, options | 附加规则 |
| `manual_nodes` | id, user_id, position, protocol, direct, server, port, tag, remark, region, udp, network, method, auth(JSON), extra(JSON) | 手工订阅节点 |
| `remote_sources` | id, name, url, token, enabled, last_sync_at, last_error | 远端节点源 |
| `sub_templates` | id, format, content, updated_at | 自定义订阅模板，空表示使用内置模板 |

### 4.2 运行时与历史表

| 表 | 说明 |
| --- | --- |
| `config_revisions` | id, created_at, actor, summary, snapshot(JSON)。每次配置事务提交后写入一条完整快照 |
| `applied_state` | 每个 stack 最近一次 apply 对应的 revision、生成物 sha256、时间；用于计算「待应用变更」和漂移检测 |
| `user_stack_disabled` | user_id, stack_id, reason(manual/quota/expired), since。对应参考项目的 `disabled.json` |
| `traffic_minute` / `traffic_hour` / `traffic_day` / `traffic_month` | 流量明细，见第 8 节 |
| `user_usage` | user_id, period_start, up, down。当前计费周期累计，用于配额判定 |
| `jobs` / `job_runs` | 定时任务定义与执行记录，见第 9 节 |
| `tasks` | 后台作业（下载、apply 等）的状态、进度、日志 |
| `dep_installs` | 组件、版本、架构与变体、来源、sha256、操作者、开始/结束时间、结果、失败原因；「依赖」页的安装历史 |
| `audit_logs` | 时间、操作者、来源 IP、动作、对象、变更摘要 |
| `admins` / `sessions` | 管理员账号（argon2id 密码哈希、可选 TOTP 密钥）与会话 |

### 4.3 领域模型的组装

领域核心从参考项目移植，输入是参考项目中的 `StackSet`（全局配置 + 全部 stack）。新项目在仓储层实现「数据库行 → 领域模型」的组装：

1. 一次只读事务读出全部配置表。
2. 把外键引用翻译成参考项目中的名字引用（例如 `ref_inbound_id` → `<stack>.<inbound>`，`listener_id` → `<stack>.clash.socks`）。
3. 交给移植后的校验、引用图和生成器，不改动其算法。
4. 校验错误从领域路径映射回「实体类型 + 实体 ID + 字段名」，返回给前端定位到具体控件。

需要从参考模型中删除的部分：YAML 文件加载、未知字段保留（`fields` 与 `Extra`）、legacy 写法（inbound 级 `user`/`users` 内联多用户、`cipher` 作为 `method` 的别名），因为数据由表单产生，只保留一种规范写法。

## 5. 配置变更与生效流程

### 5.1 保存与应用

1. **保存**：表单提交 → 应用服务层在事务内写入 → 组装完整领域模型并全量校验 → 校验通过则提交事务并写入 `config_revisions`，失败则回滚并返回字段级错误。
2. **待应用变更**：数据库是期望状态。顶栏常驻「待应用变更」提示，点开后按 stack 列出生成物 diff（create/update/delete）和将要重启的服务，对应参考项目的 `check`。
3. **应用**：用户确认后执行 apply：生成全部受影响 stack 的配置 → 原子写盘 → 更新 `applied_state` → 重启 `desired_running=true` 且生成物有变化的 unit。apply 以后台作业执行，前端显示每个 unit 的结果。
4. 保存弹窗提供「保存并应用」快捷按钮，单 stack 编辑时只 apply 该 stack。

### 5.2 用户启停热生效

沿用参考项目 `user enable/disable` 的语义和全部验收规则：

- 只作用于 vmess 和 shadowsocks 入站。
- 先计算目标状态、预演生成，确认生成物差异**恰好**等于本次 clients 增删，再写 `user_stack_disabled`、写生成物、通过 HandlerService 热增删用户。
- 差异不止 clients（例如该 stack 还有未应用的其他修改）时拒绝热生效，提示先 apply。
- 拒绝停用某个入站的最后一个启用用户，并区分「唯一用户」和「最后一个启用用户」两种提示。
- 热应用失败（服务未运行、未开启 HandlerService）只告警，并在界面上提供「重启该 stack」按钮。
- 停用状态跨重启生效：生成配置时按 `user_stack_disabled` 过滤。

配额超限和到期停用复用同一机制，`reason` 分别为 `quota` 和 `expired`。手工停用与自动停用互不覆盖：只有对应 reason 的条目会被自动恢复。

### 5.3 对账

管理服务启动时以及每 5 分钟执行一次对账：

- 生成物与 `applied_state` 记录的 sha256 不一致 → 报告漂移（健康检查页）。
- unit 实际状态与 `desired_running` 不一致 → 启动时自动纠正；运行期间只报告，不反复拉起崩溃的 unit（由 systemd 的 `Restart=on-failure` 负责）。

### 5.4 版本与回滚

- 「配置历史」页列出所有修订版本，可选两个版本查看结构化差异（按实体列出新增、删除、修改的字段）。
- 回滚 = 把选中快照写回配置表并生成一个新修订版本，之后走正常的「待应用变更」流程。
- 任意修订版本都可以按 5.5 节的格式导出为文件。

### 5.5 导入导出

除逐项手工录入外，配置可以导出为文件，也可以从文件导入。导入与手工保存走同一条路径：事务内写入、全量校验、生成修订版本、进入「待应用变更」，不会自动应用。

**文件格式**

- 本项目自有格式，默认 YAML（便于阅读和手工修改），也可选择 JSON。文件头包含 `schema: proxystack-web.config`、`version: 1`、`exported_at`、`scope`、`secrets`。
- 实体之间按名字引用，不包含数据库 ID：入站引用用户写「用户名 + 档案名」，`xray-socks5` 上游写 `<stack>.<inbound>`，出站写目标监听名，代理组成员和规则目标写组名、上游名或内置策略名。导入时再把名字翻译成本机 ID。
- 结构按「全局设置 → 用户 → stack（xray、入站、出站、mihomo、上游、代理组、规则）→ 手工节点 → 远端节点源 → 订阅模板 → 定时任务」组织，与参考项目的 YAML 结构接近，但不承诺与参考项目的文件兼容。

**导出范围**

| 范围 | 入口 | 内容 |
| --- | --- | --- |
| 全部配置 | 「备份」页 | 系统设置（不含面板监听、TLS 和管理员）、用户及档案、全部 stack、手工节点、远端节点源、订阅模板、定时任务定义；可选包含手工停用状态 |
| 选定 stack | Stack 列表多选后「导出」 | 所选 stack 及其全部子项；默认附带被引用的用户（可取消）。引用的其他 stack 不附带，导入时须已存在或同时导入 |
| 用户 | 用户列表多选后「导出」 | 用户、档案、额度、重置规则、到期时间；格式可选 YAML 或 CSV |
| 手工节点 | 手工节点列表多选后「导出」 | 所选节点 |
| 订阅模板 | 订阅模板页 | 三种格式的自定义模板 |
| 历史版本 | 配置历史页 | 指定修订版本的全部配置 |

流量数据、审计日志、管理员账号、会话不随配置导出；流量数据通过第 8 节的 CSV 导出获取。

**凭据处理**

导出时用单选按钮组选择凭据（UUID、密码、controller secret、订阅 token、下载代理密码）的处理方式：

| 选项 | 行为 |
| --- | --- |
| 口令加密（默认） | 文件结构保持可读，凭据字段逐个加密（argon2id 派生密钥 + AES-256-GCM），值形如 `enc:...`；导入时要求输入同一口令 |
| 不包含 | 凭据字段省略；导入时自动重新生成，预览中标注「凭据将重新生成，客户端需重新导入订阅」 |
| 明文 | 凭据原样写出；选择时需二次确认 |

每次导出都写入审计日志，记录范围和凭据处理方式。

**导入流程**

1. **选择来源**：「备份」页导入全部配置，各列表页的「导入」按钮导入对应范围。支持拖拽上传文件，或在代码编辑器中粘贴文本。
2. **解析**：校验 `schema`、`version` 和文件结构；加密文件弹出口令输入框。
3. **选项**：
   - 导入模式（单选按钮组）：合并 / 替换全部。「替换全部」只在全部配置范围可用，需二次确认，执行前自动备份数据库。
   - 同名冲突（单选按钮组）：跳过 / 覆盖 / 重命名导入（自动加后缀）。
   - 端口（单选按钮组）：保留文件中的端口 / 冲突时自动重新分配 / 全部重新分配。
4. **预览**：按实体类型列出新增、覆盖、跳过、重命名的条目，端口变更明细，将重新生成的凭据，以及无法解析的引用。每条实体可单独取消勾选；存在无法解析的引用时不能继续。
5. **执行**：在单个事务内写入并全量校验，任何错误整体回滚并在预览中定位到具体条目；成功后生成一个修订版本，摘要注明「导入：文件名、范围」，随后进入「待应用变更」。

**与全量迁移的区别**

本节的导入导出面向配置，可以按范围挑选、跨实例合并，不包含流量历史等运行数据。整机迁移使用 5.6 节的迁移包。

**CSV 用户导入**

CSV 列为 `name, remark, quota_gb, reset_mode, reset_day, expire_at, uuid, ss_password, note`，首行为表头。`uuid`、`ss_password` 留空时自动生成。导入前同样经过预览和冲突选项，适用于批量开通用户。

### 5.6 全量迁移

用于把整个实例搬到另一台机器，或在重装系统后原样恢复。迁移包包含数据库中的全部数据，恢复后的实例与源实例一致：用户的订阅链接、token 和凭据不变，流量历史和配额用量连续。

**迁移包内容**

迁移包是一个 `.pswbak` 文件（tar.gz 归档，默认整体口令加密），包含：

| 内容 | 说明 |
| --- | --- |
| `manifest.json` | 迁移包格式版本、程序版本、数据库迁移版本、导出时间、源主机名、源 external_host、各文件 sha256、各类数据的条数 |
| `proxystack.db` | 用 `VACUUM INTO` 生成的一致性数据库快照：全部配置、凭据、订阅 token、配置修订历史、停用状态、流量明细与用量、定时任务及执行记录、审计日志、管理员账号、依赖安装历史 |
| `tls/` | 面板证书与私钥（可选） |

不包含：xray/mihomo 二进制和 geo 数据（目标机器的架构可能不同，按源实例记录的版本在目标机器重新安装）、生成的配置文件（恢复后从数据库重新生成）、会话（恢复后需要重新登录）。

**导出**

- 入口：「备份」页「导出迁移包」；命令行 `proxystack-web backup --output FILE`。
- 导出前先把采集器内存中尚未落库的流量写入数据库，再生成快照，保证快照时刻之前的流量完整。
- 加密方式（单选按钮组）：口令加密（默认，age 格式，scrypt 派生密钥）/ 不加密（需二次确认，迁移包包含全部明文凭据）。
- 导出作为后台作业执行，完成后提供下载；导出操作写入审计日志。
- 「数据库备份」定时任务在本机 `backups/` 生成的快照，也可以在「备份」页一键打包成迁移包下载。

**恢复**

恢复会整体替换目标实例的数据库，提供两个入口：

1. **安装时恢复（推荐用于迁移）**：`install.sh --restore FILE`。安装脚本跳过生成初始管理员密码，直接用迁移包初始化，恢复后使用源实例的管理员账号登录。
2. **在已安装实例的 Web 端恢复**：「备份」页「恢复迁移包」，或命令行 `proxystack-web restore FILE`。执行前自动把当前数据库备份到 `backups/`。

Web 端恢复流程：

1. 上传迁移包，输入口令解密。
2. 校验 manifest 与各文件 sha256；迁移包的数据库迁移版本高于当前程序时拒绝，并提示先升级程序；低于当前程序时恢复后自动执行迁移。
3. **预览**：源主机、导出时间、源 external_host，用户、stack、手工节点、修订版本、流量明细的条数及时间范围。
4. **选项**：
   | 选项 | 控件 | 默认 |
   | --- | --- | --- |
   | external_host | 文本框，预填源实例的值 | 源实例的值；更换了 IP 且订阅使用 IP 时需要修改 |
   | 面板设置（监听、访问路径、TLS） | 单选按钮组：保留本机 / 使用迁移包 | 保留本机 |
   | 管理员账号 | 单选按钮组：使用迁移包 / 保留本机 | 使用迁移包 |
   | 恢复后启动代理 | 开关 | 开启 |
5. **端口检查**：用迁移包中的配置检查本机端口占用，冲突时列出冲突的 stack 和端口，可以先恢复再在 Web 端调整，或返回取消。
6. **执行**：暂停采集器和调度器 → 备份当前数据库 → 原子替换数据库文件 → 管理服务退出并由 systemd 重启。
7. **恢复后对账**：重启后检查所需的 xray/mihomo/geo 是否已安装。缺失或版本与源实例不同时，在「依赖」页提示「安装与源实例相同的版本」，一键安装。依赖就绪后从数据库重新生成配置，按「恢复后启动代理」选项拉起期望运行的 stack。

**迁移步骤建议**

1. 在新机器执行 `install.sh --restore FILE`，登录面板确认配置和依赖。
2. 订阅地址使用域名时，把域名解析切换到新机器，客户端无需任何改动；使用 IP 时，修改 external_host 并应用，客户端需更新订阅。
3. 确认新机器流量正常后，停止源机器上的服务。

导出时刻之后、停止源机器之前，源机器上产生的流量不在迁移包中。需要完整流量记录时，在停止源机器前从其流量页导出该时段的 CSV 存档。

## 6. 表单设计

### 6.1 通用规则

- **控件选择**：布尔用开关；枚举不超过 4 项用单选按钮组，超过 4 项用下拉框，选项多时可搜索；「继承全局 / 开 / 关」三态用分段单选；数字用数字输入框并带上下限；列表实体用「表格 + 侧边抽屉表单」；引用其他实体一律用下拉框，只列出合法目标。
- **条件显示**：依赖其他字段的控件按条件显示，例如选择协议后只显示该协议需要的字段。隐藏字段不提交。
- **必填与说明**：必填字段带星号；每个字段旁有说明图标，说明文字取自参考项目 schema 文档和 stack 模板注释中的字段说明。
- **校验**：前端做必填、格式、范围等基础校验；后端校验为准，错误返回字段路径，前端定位到对应控件并滚动到可见位置。跨实体错误（例如端口冲突、引用失效）显示在表单顶部并链接到相关实体。
- **敏感字段**：密码、UUID、secret、token 默认遮蔽，提供「显示」「复制」「生成」按钮。生成规则按字段类型：UUID v4；shadowsocks 2022 密钥按所选 method 生成对应长度的 base64；其他密码 24 位随机字符。
- **端口字段**：数字输入框旁有「自动分配」按钮，从对应端口池分配未占用端口；失焦时检查与其他 stack 的冲突和本机端口占用，冲突时就地提示。
- **展示名模板**：文本框下方列出可插入的变量（`.stack`、`.inbound`、`.protocol`、`.port`、`.user`、`.profile`、`.remark`）和函数（`toUpper`、`toLower`、`trim`、`replace`），并实时显示渲染结果。
- **未保存保护**：表单有改动时离开页面需确认。

### 6.1.1 控件与组件对应

本节各表中的控件均直接使用 Naive UI 组件，或由 Naive UI 组件组合成少量项目内通用组件。表单页面按实体手写，统一使用这些组件，不引入 JSON Schema 自动生成表单。

| 方案中的控件 | 实现 |
| --- | --- |
| 文本框 | `NInput` |
| 多行文本框 | `NInput type="textarea"` |
| 数字框 | `NInputNumber`（`min`/`max`/`step`） |
| 开关 | `NSwitch` |
| 单选按钮组 | `NRadioGroup` + `NRadioButton` |
| 单选卡片（新建向导选模板） | `NRadioGroup` + `NRadio` 包在 `NCard` 网格中 |
| 复选框组 | `NCheckboxGroup` + `NCheckbox` |
| 下拉框 / 可搜索下拉框 | `NSelect`（`filterable`） |
| 可输入下拉框（例如监听地址） | `NSelect`（`filterable` + `tag`，允许输入列表外的值） |
| 多选下拉框 | `NSelect`（`multiple`） |
| 级联下拉框（stack → 入站） | `NCascader` |
| 标签输入框 | `NDynamicTags` |
| 键值对编辑器 | `NDynamicInput`（`preset="pair"`） |
| 行编辑器 | `NDynamicInput` + 自定义行插槽 |
| 日期时间选择器 / 日期范围选择器 | `NDatePicker`（`type="datetime"` / `type="daterange"`） |
| 时间选择器 | `NTimePicker` |
| 表格 + 侧边抽屉表单 | `NDataTable` + `NDrawer` + `NForm` |
| 折叠的高级项 | `NCollapse` |
| 分步向导 | `NSteps` |
| 标签页 | `NTabs` |
| 二维码 | `NQrCode` |
| 用量进度条 | `NProgress` |
| 文件上传（导入） | `NUpload`（拖拽模式） |
| 二次确认 | `NPopconfirm` 或 `useDialog` |
| 表单校验与错误提示 | `NForm` 的 `rules` 做前端校验；后端返回的字段错误通过 `NFormItem` 的 `validation-status` 和 `feedback` 回填 |

项目内通用组件（由上述组件组合）：

| 组件 | 组成 | 用途 |
| --- | --- | --- |
| SecretInput | `NInput type="password"` + 显示 / 复制 / 生成按钮 | 密码、UUID、secret、token |
| PortInput | `NInputNumber` + 「自动分配」按钮 + 失焦异步冲突检查 | 各类端口 |
| PortRangeInput | `NInputGroup` 内两个 `NInputNumber` | 端口池区间 |
| TriStateSelect | `NRadioGroup`：继承全局 / 开 / 关 | stack 级覆盖全局默认值 |
| ByteSizeInput | `NInputNumber` + 单位 `NSelect`（GB / TB） | 流量额度 |
| TemplateInput | `NInput` + 变量和函数快捷插入 + 实时渲染预览 | 展示名模板 |
| OrderedPicker | `NTransfer` 选择 + 已选列表拖动排序（SortableJS） | 代理组成员；`NTransfer` 本身不支持对已选项排序 |
| RuleListEditor | 可拖动排序的行编辑器 + 文本模式切换 | mihomo 附加规则 |
| CronInput | 预设 `NSelect` + cron 文本框 + 未来执行时间预览 | 定时任务计划 |
| CodeEditor | CodeMirror 6（YAML / JSON / 模板语法高亮、只读模式） | 原始上游配置、订阅模板、生成预览、导入粘贴 |
| TrafficChart | ECharts（vue-echarts） | 流量趋势与实时速率 |

### 6.2 系统设置

| 分组 | 字段 | 控件 |
| --- | --- | --- |
| 基本 | external_host | 文本框（域名或 IP 校验） |
| 基本 | 时区 | 可搜索下拉框，默认 Asia/Shanghai；流量统计周期边界按此时区计算 |
| 端口池 | xray_inbound、clash_socks、clash_http、xray_api_range、clash_controller | 区间输入（起始、结束两个数字框，1–65535，起始 ≤ 结束） |
| 默认值 | mihomo mode | 单选按钮组：Rule / Global / Direct |
| 默认值 | mihomo loglevel | 下拉框：debug / info / warning / error / silent |
| 默认值 | xray loglevel | 下拉框：debug / info / warning / error / none |
| 默认值 | xray api 启用 | 开关 |
| 默认值 | xray api services | 复选框组：HandlerService、StatsService；流量监控开启时 StatsService 锁定勾选，并说明 HandlerService 是用户启停热生效的前提 |
| 默认值 | xray stats 启用、policy 启用 | 开关 |
| 默认值 | policy 高级项（statsUserUplink/Downlink、statsInbound/Outbound Uplink/Downlink） | 折叠面板内的开关组 |
| 安全 | require_auth_for_public_socks_http | 开关 |
| 安全 | allow_noauth_public | 开关，打开时弹出二次确认说明风险 |
| 依赖源（xray/mihomo/geo 各一组） | version | 下拉框：latest + 从上游拉取的版本列表；允许手工输入 |
| 依赖源 | source | 单选按钮组：自动 / GitHub / 自定义 URL |
| 依赖源 | URL、sha256 | 文本框，仅「自定义 URL」时显示；http(s) URL 时 sha256 必填，64 位十六进制 |
| 依赖源 | archive_member | 文本框，折叠在高级项中 |
| 依赖源 | mihomo 构建变体 | 单选按钮组：自动 / 标准 / compatible |
| 依赖源 | 自动升级（xray、mihomo 各一个） | 开关，默认关闭 |
| 依赖源 | 维护时间窗 | 两个时间选择器（`NTimePicker`）：开始、结束 |
| 下载代理 | 模式 | 单选按钮组：直连（默认）/ 经指定 stack 的 mihomo 监听 / 自定义代理 |
| 下载代理 | stack、监听 | 仅「经指定 stack」时显示：stack 下拉框 + 该 stack 的 HTTP/SOCKS5/mixed 监听下拉框；监听要求认证时自动使用其凭据 |
| 下载代理 | 协议 | 仅「自定义代理」时显示：单选按钮组 HTTP / SOCKS5 |
| 下载代理 | 主机、端口 | 仅「自定义代理」时显示：文本框 + 数字框；可粘贴 `http://user:pass@host:port` 或 `socks5://user:pass@host:port` 形式的 URL，自动拆分填入各字段 |
| 下载代理 | 用户名、密码 | 仅「自定义代理」时显示：文本框 + 密码框，可同时留空；只填其中一个时报字段错误。密码按敏感字段遮蔽，保存时留空表示保持原值 |
| 下载代理 | 由代理解析域名 | 开关，仅 SOCKS5 时显示，默认开启：目标域名交给代理端解析，本机不做 DNS 查询 |
| 下载代理 | 测试连接 | 按钮：用表单当前值（无需先保存）经代理请求 GitHub API，显示耗时，失败时区分「连接代理失败 / 代理认证失败 / 超时 / TLS 错误 / 上游返回错误」 |
| 订阅 | 监听地址、端口 | 下拉框（0.0.0.0 / 127.0.0.1 / ::，可手工输入）+ 数字框 |
| 订阅 | public_base_url | URL 文本框，只允许 http/https，禁止 query 和 fragment |
| 订阅 | Surge managed config 启用 | 开关 |
| 订阅 | interval | 数字框 + 单位下拉框（小时 / 天） |
| 订阅 | strict | 开关 |
| 流量 | 采集间隔 | 单选按钮组：5 秒 / 10 秒 / 30 秒 |
| 流量 | 分钟、小时、日、月明细保留期 | 数字框 + 单位 |
| 面板 | 监听地址、端口、访问路径 | 文本框；修改后提示需重启管理服务，并提供重启按钮 |
| 面板 | TLS 模式 | 单选按钮组：自签证书 / ACME 自动申请 / 反向代理（面板只监听本机 HTTP） |
| 面板 | ACME 域名、邮箱 | 文本框，仅 ACME 模式显示 |

xray API 的监听地址固定为 `127.0.0.1`，端口从 `xray_api_range` 为每个 stack 自动分配，不再暴露为可编辑字段；api tag 固定为 `api`。

### 6.3 用户

| 字段 | 控件 |
| --- | --- |
| name | 文本框，标识符规则（字母数字开头，可含 `_.-`），唯一 |
| enabled | 开关；关闭等价于在所有 stack 上手工停用 |
| remark | 文本框 |
| display_template | 展示名模板编辑框（见 6.1） |
| tag | 文本框，折叠在高级项中 |
| uuid | 遮蔽文本框 + 生成按钮；新建时自动生成 |
| shadowsocks 密码 | 遮蔽文本框 + 生成按钮；生成长度按该用户被引用的 shadowsocks 入站的 method 确定，多个 method 冲突时提示 |
| 凭据档案 | 子表格：档案名、uuid、密码、remark、display_template、tag，每行可编辑 |
| 流量额度 | 数字框 + 单位下拉框（GB / TB），0 表示不限 |
| 重置周期 | 单选按钮组：不重置 / 每月 |
| 重置日 | 数字框 1–28，仅「每月」时显示 |
| 到期时间 | 日期时间选择器，可清空 |
| 订阅 token | 只读文本 + 复制按钮 + 「重置」按钮（重置后旧链接立即失效，需二次确认） |
| 订阅链接 | 三种格式（Clash / Premium Clash / Surge）各一行：复制按钮 + 二维码弹窗 |
| 各 stack 启停 | 详情页表格：stack、入站列表、停用原因、开关（热生效） |
| 备注 | 多行文本框 |

用户详情页同时显示：被哪些入站引用（只读列表，可跳转）、本周期用量进度条、最近 30 天用量趋势图。

### 6.4 Stack 新建向导

1. **模板**：单选卡片，三选一：`pair`（单节点链路）、`auto-url-test`（多成员自动测速）、`load-balance`（多成员负载均衡）。每张卡片附简短拓扑示意。
2. **基本信息**：名称（文本框）、标签（标签输入框）。
3. **成员**：仅 auto 类模板显示，多选下拉框，只列出带 `relay` socks5 入站的 edge stack。
4. **端口**：开关「自动分配全部端口」，默认开启。
5. 完成后进入 Stack 编辑页，所有字段已按模板预填，UUID、secret、密码已随机生成。

模板默认内容以参考项目的 stack 模板和片段为准（见第 13 节）。

### 6.5 Stack 编辑页

页面分为「基本」「Xray」「入站」「出站」「Mihomo」「上游」「代理组」「规则」「成员」（仅 auto 角色）几个标签。

**基本**

| 字段 | 控件 |
| --- | --- |
| name | 文本框，标识符规则，唯一；改名不影响引用 |
| enabled | 开关；关闭时不生成配置、不参与对账 |
| 期望运行 | 开关；对应参考项目的 enable/disable |
| role | 单选按钮组：edge / auto |
| labels | 标签输入框 |

**Xray**

| 字段 | 控件 |
| --- | --- |
| enabled | 开关 |
| loglevel | 下拉框：继承全局 / debug / info / warning / error / none |
| api、stats、policy | 三态分段单选：继承全局 / 开 / 关 |
| api 端口 | 只读显示 + 「重新分配」按钮 |

**入站**（表格 + 抽屉表单）

| 字段 | 显示条件 | 控件 |
| --- | --- | --- |
| name | 总是 | 文本框，stack 内唯一 |
| protocol | 总是 | 单选按钮组：VMess / Shadowsocks / SOCKS5 / HTTP |
| listen | 总是 | 可输入下拉框：0.0.0.0 / 127.0.0.1 / :: / 自定义 |
| port | 总是 | 端口输入（见 6.1） |
| sub | 总是 | 单选按钮组「进入订阅：是 / 否」，**无默认选中**，必须显式选择（沿用参考项目「sub 必须显式声明」的规则） |
| udp | VMess / Shadowsocks / SOCKS5 | 开关 |
| network | VMess | 单选按钮组：raw(tcp) / ws / grpc |
| ws path | VMess 且 ws | 文本框，以 `/` 开头 |
| ws headers | VMess 且 ws | 键值对编辑器（增删行） |
| grpc service_name | VMess 且 grpc | 文本框 |
| method | Shadowsocks | 下拉框：aes-128-gcm、aes-256-gcm、chacha20-ietf-poly1305、2022-blake3-aes-128-gcm、2022-blake3-aes-256-gcm、2022-blake3-chacha20-poly1305。参考项目对非 2022 系列 method 不做枚举限制，本项目收窄为该集合，并在后端校验中同步限制 |
| password | Shadowsocks | 遮蔽文本框 + 生成按钮；2022 系列按 method 校验密钥长度 |
| 用户 | VMess / Shadowsocks | 多选下拉框（来自用户表），每个已选用户可再选档案（下拉框），并可展开覆盖 remark/display_template/tag |
| auth type | SOCKS5 / HTTP | 单选按钮组：无认证 / 用户名密码；listen 非 loopback 且选择无认证时，按安全设置给出阻止或风险确认 |
| username、password | SOCKS5 / HTTP 且用户名密码 | 文本框 + 遮蔽文本框 + 生成按钮 |
| 订阅用户 | SOCKS5 / HTTP 且 sub=是 | 单选下拉框（来自用户表） |
| server | sub=是 | 文本框，占位符显示 external_host |
| remark | sub=是 | 文本框 |
| display_template | sub=是 | 展示名模板编辑框 |
| region | sub=是 | 可搜索下拉框：两位国家代码 + 国家名 |
| tag | 总是 | 文本框，折叠在高级项中，占位符显示默认值 `<protocol>:<port>:<name>` |

**出站**

| 字段 | 显示条件 | 控件 |
| --- | --- | --- |
| type | 总是 | 单选按钮组：本 stack Mihomo / 外部 SOCKS5 / 外部 HTTP / 直连 |
| 目标监听 | Mihomo | 下拉框：本 stack 的 mihomo socks 监听 |
| server、port | SOCKS5 / HTTP | 文本框 + 数字框 |
| username、password | SOCKS5 / HTTP | 文本框 + 遮蔽文本框（可选） |
| private_direct | SOCKS5 / HTTP | 开关「私网目标直连」 |

选择「直连」或外部代理时提示可以关闭本 stack 的 Mihomo。

**Mihomo**

| 字段 | 控件 |
| --- | --- |
| enabled | 开关 |
| mode | 分段单选：继承全局 / Rule / Global / Direct |
| loglevel | 下拉框：继承全局 / debug / info / warning / error / silent |
| controller 端口 | 只读显示 + 「重新分配」按钮；监听固定为 127.0.0.1 |
| controller secret | 遮蔽文本框 + 生成按钮 |
| socks 监听：name、listen、port | 文本框 / 可输入下拉框 / 端口输入 |
| socks 监听：认证方式 | 单选按钮组：跟随全局 / 不认证 / 指定账号 |
| socks 监听：账号 | 「指定账号」时显示行编辑器（用户名、密码） |
| http 监听 | 开关「启用 HTTP 监听」，打开后显示与 socks 相同的字段组 |

**上游**（表格 + 抽屉表单）

| 字段 | 显示条件 | 控件 |
| --- | --- | --- |
| name | 总是 | 文本框，stack 内唯一 |
| type | 总是 | 单选按钮组：外部节点 / 引用其他 stack 的 SOCKS5 入站 |
| 引用 | 引用 | 级联下拉框：stack → 该 stack 的 socks5 入站 |
| 节点协议 | 外部节点 | 下拉框：vmess / shadowsocks / socks5 / http / 其他 |
| 结构化字段 | 外部节点且协议非「其他」 | 按协议显示：server、port、uuid、network、ws 选项、cipher、password、username、tls、skip-cert-verify、servername、udp 等 |
| 原始配置 | 总是可切换 | 「高级」标签：CodeMirror YAML 编辑器，与结构化表单双向同步；协议为「其他」时只能用此方式，按 mihomo proxy 字段原样写入 |

**代理组**（表格 + 抽屉表单）

| 字段 | 显示条件 | 控件 |
| --- | --- | --- |
| name | 总是 | 文本框 |
| type | 总是 | 单选按钮组：select / url-test / load-balance / fallback |
| proxies | 总是 | 有序穿梭框：左侧候选（本 stack 上游、其他代理组、DIRECT、REJECT），右侧已选可拖动排序；不能选择自身，不能形成循环 |
| url | 非 select | 文本框，默认 `http://www.gstatic.com/generate_204` |
| interval | 非 select | 数字框（秒） |
| strategy | load-balance | 下拉框：consistent-hashing / round-robin / sticky-sessions |

**规则**

| 字段 | 控件 |
| --- | --- |
| profile | 下拉框：default |
| final | 下拉框：本 stack 的代理组、上游、DIRECT、REJECT，默认 AllProxy |
| 附加规则 | 可拖动排序的行编辑器：规则类型（下拉框：DOMAIN、DOMAIN-SUFFIX、DOMAIN-KEYWORD、IP-CIDR、IP-CIDR6、GEOIP、GEOSITE、DST-PORT、PROCESS-NAME 等）、匹配值（文本框）、目标（下拉框，同 final 的候选）、no-resolve（复选框，仅 IP 类规则显示） |
| 批量编辑 | 切换为多行文本模式，每行一条 mihomo 规则文本，切回时解析为行并校验目标 |

**成员**（仅 auto 角色）

多选下拉框，只列出带 `relay` socks5 入站的 edge stack。保存时按参考项目 `member add/remove` 的逻辑同步上游（`xray-socks5` 引用）和相关代理组成员。

### 6.6 手工节点

| 字段 | 控件 |
| --- | --- |
| 所属用户 | 下拉框 |
| 模式 | 单选按钮组：标准节点 / Mihomo 原生节点（对应参考项目 `direct: true`） |
| 标准节点字段 | protocol（单选按钮组）、server、port、tag、remark、region、udp，以及协议相关凭据字段，规则同入站表单 |
| 原生节点字段 | CodeMirror YAML 编辑器，按 mihomo proxy 字段原样输出到 Clash/Premium Clash，Surge 输出按参考项目兼容规则映射或跳过 |
| 批量操作 | 表格多选后「批量设置 server」「批量删除」「克隆」 |

保存时与该用户的全部订阅节点一起做合并校验：节点 tag 全局唯一，同一用户下代理名不重复。

### 6.7 定时任务

| 字段 | 控件 |
| --- | --- |
| 名称 | 文本框 |
| 任务类型 | 下拉框（来自任务注册表，内置类型不可删除） |
| 计划 | 预设下拉框（每小时 / 每天 / 每周 / 每月 / 自定义）；选「自定义」时显示 cron 表达式文本框；下方显示未来 5 次执行时间 |
| 启用 | 开关 |
| 超时 | 数字框（秒） |
| 参数 | 按任务类型渲染的子表单，例如备份任务的「保留份数」数字框 |

列表页每行提供「立即执行」按钮和最近一次执行结果；详情页显示执行历史和每次执行的日志。

## 7. 订阅

- 订阅端口与管理端口分开，订阅端口可以对公网开放，管理端口可以只开放给特定来源。
- **每个用户一个独立的订阅 token**，路径为 `/s/<token>/clash`、`/s/<token>/premium`、`/s/<token>/surge`。与参考项目「全局 token + 路径中带用户名」相比，单个用户的链接泄露不会影响其他用户，重置也只影响该用户。
- 订阅内容由同一进程直接从数据库生成：本机所有 `sub=是` 的入站 + 该用户的手工节点 + 远端节点源同步来的节点。生成结果按配置修订版本缓存。
- 渲染规则、内置模板、Surge 兼容映射和 managed config 首行完全沿用参考项目。
- 「订阅模板」页允许覆盖三种格式的模板（CodeMirror 编辑器），右侧选择一个用户实时预览渲染结果；保存前做语法检查，清空即恢复内置模板。
- 错误码沿用参考项目：token 无效返回 403，用户无节点返回 404，模板错误返回 503。用户被禁用或到期时返回 404 或空订阅，可在设置中选择。
- **远端节点源**：其他 proxystack-web 实例在「系统设置 → 节点导出」中开启只读的节点导出接口（独立 token）。本实例添加远端源后，由定时任务定期拉取，节点归属按用户名匹配，同步结果和错误显示在远端源列表中。它替代参考项目「导出 bundle → 拷贝 → 导入」的流程。

## 8. 流量监控

### 8.1 与参考实现的对比

| 方面 | 参考实现 | 本方案 |
| --- | --- | --- |
| 采集方式 | 每小时执行一次 `xray api statsquery -reset=true` 命令 | 进程内 gRPC 调用 StatsService，`reset=true`，默认每 10 秒一次 |
| 实例配置 | 手工配置实例名与 API 地址列表 | 自动跟随 stack：所有启用 xray 且开启 stats 的 stack 自动纳入 |
| 计划内重启 | 两次采集之间的计数随 xray 重启丢失（最多一小时） | apply、依赖升级、热增删用户之前先补采一次，计划内操作不丢数据；意外崩溃最多丢一个采集间隔 |
| 实时查看 | `show current` 显示上次 reset 以来的累计 | 内存保留最近 1 小时的 10 秒粒度数据，前端通过 SSE 显示实时速率曲线 |
| 用户归属 | 按 xray 中的 email 字符串 | 生成配置时 email 写为 `u<用户ID>`（档案为 `u<用户ID>.p<档案ID>`），用户改名后历史数据仍然连续 |
| 聚合层级 | 小时 → 日 → 月，年由月聚合 | 分钟 → 小时 → 日 → 月，年由月聚合 |
| 保留策略 | hourly 按配置天数，daily 至少 62 天，monthly 36 个月 | 分钟 7 天，小时 180 天，日 2 年，月永久；均可在设置中修改 |
| 时区 | 配置时区，周期边界按时区计算 | 相同；数据库存 UTC 时间戳，周期边界按设置的时区计算 |
| 配额 | 无 | 用户额度、按月重置、到期时间，超限自动停用 |

### 8.2 数据结构

四张明细表结构相同：

```text
traffic_<level>(bucket_start, stack_id, scope, subject, up, down)
主键：(bucket_start, stack_id, scope, subject)
scope：user / inbound / outbound
subject：user 为用户 ID（含档案时为 "用户ID.档案ID"），inbound/outbound 为 tag
```

- 采集器把每次采集的增量累加在内存中，每分钟在一个事务里写入 `traffic_minute`，同时累加 `user_usage`。
- 小时、日、月汇总由调度器执行，从下一级聚合，可重复执行（按主键覆盖写入）。
- 查询当前未结束的周期时，把已入库部分与内存中尚未落库的部分合并显示，与参考实现「show 时补当前周期」的行为一致。

### 8.3 计数解析

StatsService 返回的计数名格式为 `user>>>EMAIL>>>traffic>>>uplink`、`inbound>>>TAG>>>traffic>>>downlink`、`outbound>>>TAG>>>traffic>>>uplink`。解析规则沿用参考实现：

- 忽略 api 入站自身的统计。
- 无法映射到当前用户的 email（例如已删除用户）计入 `subject="unknown"`，不丢弃。
- 采集某个 stack 失败（服务未运行、端口不通）只记录该 stack 的错误状态，不影响其他 stack。

用户级统计依赖 policy 中 `statsUserUplink/statsUserDownlink` 为 true。流量监控开启时，生成器强制为所有入站用户写入 email 并开启这两项。

### 8.4 页面

- **总览**：全部 stack 实时上下行速率曲线；今日、本月合计；按用户排序的本月用量 Top 10。
- **明细**：筛选条件为时间粒度（单选：小时 / 日 / 月 / 年）、时间范围（日期范围选择器）、stack（多选）、维度（单选：用户 / 入站 / 出站）、对象（多选）。显示堆叠柱状图和表格，表格支持排序和 CSV 导出。
- **用户详情**：本周期用量进度条（相对额度）、最近 30 天日用量趋势。

### 8.5 配额与到期

- 采集器每分钟落库后检查 `user_usage`：超出额度的用户以 `reason=quota` 在其所有 stack 上热停用。
- 调度器每分钟检查到期时间：到期用户以 `reason=expired` 停用。
- 每月重置日 00:00（按设置时区）清零当期用量，并自动恢复 `reason=quota` 的停用；`manual` 和 `expired` 的停用不受影响。
- 预留告警阈值（例如额度 80%），接入通知功能后推送。

## 9. 定时任务

### 9.1 机制

- 任务注册表在代码中定义任务类型：类型名、参数结构、默认计划、执行函数。
- `jobs` 表保存实例化的任务（计划、启用、参数）；首次启动时为每个内置类型写入默认任务。
- 同一任务不并发执行，上一次未结束时跳过本次并记录。
- 每次执行写 `job_runs`：开始/结束时间、状态（成功 / 失败 / 跳过 / 超时）、日志摘要。执行记录保留最近 200 条/任务。
- 管理服务停机期间错过的执行不补跑，汇总类任务本身可重入，下一次执行会补齐。

### 9.2 内置任务

| 任务 | 默认计划 | 说明 |
| --- | --- | --- |
| 流量小时汇总 | 每小时第 1 分钟 | 分钟 → 小时 |
| 流量日汇总 | 每天 00:10 | 小时 → 日 |
| 流量月汇总 | 每月 1 日 00:30 | 日 → 月 |
| 流量数据清理 | 每天 03:00 | 按保留策略删除过期明细 |
| 配额重置 | 每天 00:00 | 对到达重置日的用户清零用量并恢复 quota 停用 |
| 到期检查 | 每分钟 | 到期用户停用 |
| 数据库备份 | 每天 04:00 | `VACUUM INTO` 到 `backups/`，保留 N 份 |
| geo 数据更新 | 每周一 05:00 | 下载并校验，成功后重启使用 geo 的 mihomo |
| 依赖版本检查 | 每天 06:00 | 只检查不安装，有新版本时在界面上提示 |
| 远端节点源同步 | 每 30 分钟 | 拉取远端实例节点 |
| 对账 | 每 5 分钟 | 见 5.3 |
| 证书续期 | 每天 | ACME 模式下检查并续期 |

## 10. 安全

- **初始密码**：`proxystack-web init` 生成管理员随机密码，只在执行它的终端打印一次，数据库只保存哈希，不写入任何文件或日志。安装脚本调用 `init`，因此密码出现在运行安装脚本的终端，与面板地址（含随机访问路径）、证书指纹一起打印。首次登录强制修改密码。数据库未初始化时 `serve` 拒绝启动并提示先执行 `init`，避免服务自行生成密码并写入 journal。
- **传输**：默认使用自签证书启用 HTTPS，安装输出包含证书指纹；可切换为 ACME 自动申请，或「反向代理」模式（面板只监听 127.0.0.1 的 HTTP，由外部反向代理负责 TLS）。
- **认证**：argon2id 密码哈希；会话 cookie 设置 HttpOnly、Secure、SameSite=Strict；所有修改类请求校验 CSRF token；登录失败按 IP 和账号限速，连续失败后锁定一段时间；可选 TOTP 两步验证。
- **访问路径**：管理端挂在随机路径下，其他路径返回 404，减少被扫描识别的概率。
- **敏感信息**：日志中不记录密码、token、uuid 和完整订阅内容（沿用参考项目的约束）；API 默认返回遮蔽值，点击「显示」时单独请求明文并记入审计日志。
- **权限边界**：管理服务以普通用户运行，只能操作 `psw-` 前缀的两个模板 unit；Web 端被攻破时，影响范围是代理配置和服务账号可写的目录，不能直接获得 root。
- **救援命令**：`proxystack-web admin reset-password`、`proxystack-web admin reset-link`、`proxystack-web info`（显示面板地址和证书指纹）、`proxystack-web backup` / `restore`。救援命令需要 root 或服务账号身份执行。

### 10.1 管理员密码重置

重置的前提是证明操作者拥有服务器的 shell 权限，因此重置入口只在服务器本机的命令行发起，提供两种方式：

| 方式 | 命令 | 行为 |
| --- | --- | --- |
| 命令行直接重置 | `sudo proxystack-web admin reset-password` | 生成新的随机密码并在终端打印；也可用 `--password-stdin` 从标准输入读取指定密码。下次登录强制修改密码 |
| Web 端重置 | `sudo proxystack-web admin reset-link` | 生成一次性重置链接并在终端打印。链接 15 分钟内有效、只能使用一次；打开后在 Web 端设置新密码 |

共同规则：

- 两条命令直接写数据库（SQLite WAL 模式允许与运行中的服务并发），**不需要重启服务，也不需要修改 unit 或环境变量**。
- 重置成功后，该管理员的全部会话立即失效，登录失败计数和锁定状态清零。
- 附加参数 `--disable-totp` 同时关闭两步验证，用于 TOTP 设备丢失的场景。
- 重置链接中的 token 只在数据库保存哈希；新生成链接会使之前未使用的链接作废。
- 重置操作写入审计日志，记录方式和来源（命令行或链接）。

不采用「以环境变量 `RESET_PASSWORD=true` 启动进入重置模式」的原因：

- 重置模式期间，任何能访问面板的人都可以不经认证重设密码，形成未授权接管窗口；面板通常对公网开放，该窗口不可接受。
- 服务由 systemd 启动，注入环境变量需要编辑 unit 或 drop-in 文件，完成后还要撤销并再次重启，步骤多且容易遗漏；遗漏撤销会让重置入口长期开放。
- 两次重启管理服务没有必要，上述命令在服务运行期间即可完成重置。

## 11. 安装、升级与卸载

### 11.1 安装脚本

1. 检查 systemd 版本和 polkit 是否存在；缺少 polkit 时通过发行版包管理器安装。
2. 创建系统用户 `proxystack-web`（无登录 shell），加入 `systemd-journal` 组。
3. 创建 `/opt/proxystack-web` 目录树并设置属主和权限。
4. 下载指定版本的 release 二进制并校验 sha256，安装到 `bin/`，在 `/usr/local/bin` 建软链接。
5. 写入三个 unit 文件和 polkit 规则，执行 `daemon-reload`。
6. 以服务账号执行 `proxystack-web init`：建库、执行迁移、生成管理员随机密码、生成自签证书和面板随机路径。
7. `systemctl enable --now proxystack-web.service`。
8. 打印面板地址、初始密码和证书指纹。

参数：`--version`、`--listen`、`--port`、`--tls self-signed|acme|proxy`、`--domain`、`--email`、`--restore FILE`。

传入 `--restore FILE` 时，第 6 步改为用迁移包初始化数据库（口令从终端交互读取），不生成初始管理员密码；第 8 步只打印面板地址和证书指纹。恢复细节见 5.6。

### 11.2 升级

- Web 端「系统」页检查新版本 → 下载并校验 → 原子替换 `bin/proxystack-web` → 管理服务以特定退出码退出，由 systemd 重启。代理进程不受影响。检查新版本和下载都走 11.3「下载代理」中的设置。
- 数据库迁移在启动时自动执行，执行前先 `VACUUM INTO` 备份一份。
- xray/mihomo/geo 的安装、升级和回滚见 11.3。

### 11.3 xray / mihomo / geo 管理

「依赖」页集中管理三个组件：xray、mihomo、geo 数据（geoip.dat / geosite.dat）。安装源、版本解析、归档解压与路径安全检查沿用参考项目的依赖安装逻辑。

**组件卡片**

每个组件一张卡片，显示：

- 当前版本、安装时间、安装来源、文件 sha256、CPU 架构与构建变体。
- 最新可用版本（由「依赖版本检查」定时任务获取），有新版本时显示提示。
- 正在使用该组件的 stack 数量（期望运行的 stack）。
- 上一个版本（可回滚时显示）。

卡片操作：

| 操作 | 控件 | 说明 |
| --- | --- | --- |
| 升级到最新 | 按钮 | 安装最新稳定版 |
| 安装指定版本 | `NSelect` 版本列表 + 「包含预发布版本」开关 | 版本列表从上游 release 拉取；可降级 |
| 上传安装包 | `NUpload` | 无法访问外网的机器上传 release 归档，sha256 必填 |
| 回滚 | 按钮 + 二次确认 | 切换回上一个版本 |
| 查看更新说明 | 链接 / 抽屉 | 显示所选版本的 release notes |
| 安装历史 | 表格 | 每次安装的版本、来源、sha256、操作者、结果 |

**架构与构建变体**

- 根据运行平台自动选择 release 资产：amd64、arm64（以及 armv7，如上游提供）。
- mihomo 在 amd64 上区分标准构建和 compatible 构建（适配不支持较新指令集的 CPU）。默认按 CPU 特性自动选择，可在设置中固定为某一变体。

**安装流程**

安装作为后台作业执行，前端通过 SSE 显示每一步的进度和日志：

1. **解析版本**：从上游 release 解析出与本机架构匹配的资产下载地址。
2. **下载**：下载到 `downloads/`，显示进度。下载按「下载代理」设置连接（见本节末）。
3. **校验**：上游提供摘要文件时自动比对 sha256；自定义 URL 和上传安装包必须提供 sha256（沿用参考项目规则）。
4. **解压**：按参考项目的规则解压，拒绝链接文件、路径穿越和成员不明确的归档。
5. **冒烟验证**：先执行版本命令确认能运行，再用新二进制对当前全部生成配置做配置检查（xray 的 `-test`、mihomo 的 `-t`）。任何一个 stack 的配置不被新版本接受时，中止安装并列出出错的 stack，现有二进制保持不变。
6. **替换**：原子替换 `bin/` 中的文件，旧文件保留为上一个版本，用于回滚。只保留一个上一版本。
7. **重启**：列出受影响的期望运行 unit，按用户选择执行（单选按钮组）：
   - 逐个重启（默认）：一个 unit 重启并通过检查后再重启下一个。
   - 同时重启全部。
   - 暂不重启：新版本在下次 apply 或手动重启时生效，卡片上标注「有 unit 仍在运行旧版本」。
   重启 xray 前先补采一次流量。
8. **重启后检查**：unit 进入 active 并持续 10 秒，且配置中的监听端口已打开。任一 unit 检查失败时自动回滚到上一个版本，重启已切换的 unit，并在作业结果中报告失败原因。

**geo 数据**

- 流程与上面相同，没有版本选择，以下载时间和 sha256 标识版本。
- 「geo 数据更新」定时任务默认每周执行一次；更新后只重启引用了 geo 数据的 unit。

**更新策略**

- xray 和 mihomo 默认不自动升级。「依赖版本检查」任务只获取最新版本并在界面提示，升级由管理员手动触发。
- 可以为 xray、mihomo 分别开启「自动升级」开关（默认关闭），开启后在维护时间窗内按上述流程自动执行，失败自动回滚并记录。
- 所有安装、升级、回滚操作写入审计日志和安装历史。

**下载代理**

- 一组全局设置（见 6.2「下载代理」），作用于依赖管理和自更新的全部对外请求：版本列表与更新说明的获取、「依赖版本检查」与「geo 数据更新」定时任务、xray/mihomo/geo 下载、面板自更新的版本检查与下载。代理进程本身、订阅和远端节点源的请求不受它影响。
- 支持 HTTP 代理和 SOCKS5 代理，两者都可以带用户名和密码：HTTP 代理以 CONNECT 建立隧道并发送 Basic 认证，SOCKS5 使用用户名/密码认证方式。也可以选择经本机某个 stack 的 mihomo 监听出站。
- 选择「经指定 stack」而该 stack 未运行时，作业在下载前报错，提示改用其他模式或先启动该 stack。
- 作业日志、错误信息和审计日志中的代理地址一律去掉用户名和密码；修改下载代理设置写入审计日志，只记录模式、协议和地址。
- 代理设置在页面上修改后立即对之后发起的作业生效，不需要 apply；已在运行的下载作业继续使用开始时的设置。

### 11.4 卸载

卸载脚本停止并删除全部 `psw-*` 实例和管理服务、unit 文件、polkit 规则、软链接。默认保留 `/opt/proxystack-web/data` 和 `backups`，`--purge` 时删除整个目录和服务账号。

## 12. 健康检查与诊断

「健康检查」页汇总以下检查项，每项显示通过 / 警告 / 失败和处理建议：

- 管理服务版本、xray/mihomo/geo 版本，二进制是否存在且可执行。
- 每个 stack 的 unit 状态与期望运行状态是否一致。
- 生成物是否与最近一次 apply 一致（漂移检测）、是否有待应用变更。
- 端口：配置内部是否冲突、本机是否被其他进程占用。
- 每个 stack 的 HandlerService 状态：开启时说明它是本机无鉴权的用户管理接口，未开启时说明用户启停需要重启 stack 才生效。两者都是合法配置，只提示不判失败（沿用参考项目 `doctor` 的规则）。
- `user_stack_disabled` 中引用已不存在用户或 stack 的陈旧条目。
- 流量采集器各 stack 的最近采集时间和错误。
- 数据库大小、最近备份时间、磁盘剩余空间。
- polkit 授权是否生效（尝试查询 unit 属性）。

Stack 详情页的诊断工具：

- **出口 IP 检测**：经该 stack 的 mihomo socks 监听查询出口 IP，可选 IPv4 / IPv6 / 全部，超时默认 8 秒；数据源和回退顺序沿用参考项目。
- **上游延迟测试**：调用 mihomo controller 的延迟测试接口，对选中的上游或代理组测速，结果显示在上游表格中。

## 13. 参考项目查阅指引

参考项目位置：`/Users/eagle/Sync/proxy/proxystack-go`。

| 需要了解的内容 | 参考位置 |
| --- | --- |
| 全部功能点与每个命令的职责、副作用、验收规则 | `docs/cli-spec.md` |
| 配置字段、类型、默认值、校验 | `docs/schema-spec.md`，以 `internal/domain/models.go` 和 `internal/domain/validation/` 为准 |
| 用户档案与 `user_refs` 展开 | `internal/domain/user_refs.go` |
| xray / mihomo 配置生成规则 | `docs/generator-spec.md`，`internal/generator/xray/`、`internal/generator/mihomo/` |
| ref 引用图与目标范围 | `internal/graph/` |
| 生成物 diff、manifest、用户启停的「恰好等于 clients 增删」判定 | `internal/runtime/plan.go`、`internal/runtime/usersdiff.go` |
| 用户启停命令流程与提示文案 | `internal/cli/agent/user_commands.go` |
| stack 新建、克隆、成员维护、端口分配 | `internal/cli/agent/config_commands.go` |
| stack 模板与字段说明注释（表单说明文字的来源） | `internal/agentconfig/templates/`（含 `snippets/`） |
| xray HandlerService 热增删用户 | `internal/xrayapi/client.go` |
| 订阅节点生成、渲染、Surge 兼容映射 | `docs/template-compat-spec.md`，`internal/generator/sub/`，内置模板 `templates/sub/` |
| 订阅 HTTP 行为、错误码、managed config | `docs/http-subserver-spec.md`，`internal/subserver/` |
| 依赖下载、校验、安装源 | `internal/install/` |
| doctor 检查项与 ipinfo 数据源 | `internal/cli/agent/diagnostics_commands.go`，`internal/diagnostics/` |
| systemd 模板 unit 写法 | `internal/systemd/runner.go` |
| 生成器 golden 测试数据 | `tests/golden/`、`tests/fixtures/` |

移植原则：

- 领域模型、校验、引用图、生成器、订阅渲染按原有算法移植，只删除 YAML 加载、未知字段保留和 legacy 写法。
- 移植后先跑通参考项目的 golden 测试（输入改为构造领域模型），确认生成结果逐字节一致，再接入数据库。
- 参考项目的 CLI 层、launchd 支持、组可写目录权限模型、草稿文件机制、bundle/备份 zip 格式不移植。

## 14. 新项目结构

```text
proxystack-web/
├── cmd/proxystack-web/       入口：serve（默认）、init、admin、info、backup、restore
├── internal/
│   ├── app/                  应用服务层：每个业务操作一个方法
│   ├── api/                  HTTP 处理、OpenAPI、SSE、鉴权中间件
│   ├── store/                SQLite 仓储、迁移、sqlc 生成代码
│   ├── assemble/             数据库行 ↔ 领域模型
│   ├── domain/               移植：模型与校验
│   ├── graph/                移植：引用图
│   ├── generator/            移植：xray、mihomo、sub
│   ├── runtime/              生成物 diff、写盘、apply、用户热生效
│   ├── systemd/              D-Bus 服务管理、日志读取；开发模式假实现
│   ├── xrayapi/              gRPC：StatsService、HandlerService
│   ├── mihomoapi/            controller：延迟测试、连接信息
│   ├── deps/                 依赖下载与替换、自更新
│   ├── traffic/              采集器、汇总、配额
│   ├── scheduler/            任务注册表与执行
│   ├── subscription/         订阅端路由与缓存
│   ├── diagnostics/          健康检查、出口 IP
│   └── audit/                审计日志
├── web/                      Vue 前端工程，构建产物由 go:embed 内嵌
├── deploy/                   安装/卸载脚本、unit 模板、polkit 规则
└── tests/                    golden 数据、API 集成测试、Linux 端到端测试
```

## 15. 开发里程碑

| 里程碑 | 内容 | 完成标准 |
| --- | --- | --- |
| M0 骨架 | 仓库、构建、前端内嵌、数据库迁移、管理员登录、会话与 CSRF、救援命令、安装脚本草稿 | 在 Linux 虚拟机上安装后能登录空面板 |
| M1 核心移植 | 移植领域模型、校验、引用图、生成器；golden 测试通过 | 参考项目全部 golden 用例逐字节一致 |
| M2 配置管理 | 系统设置、用户、stack 全部表单与 API；新建向导、克隆、成员；字段级错误定位；配置历史与回滚；导入导出（含口令加密、预览、CSV 用户导入） | 用表单录入参考项目示例工程的等价配置，生成物与参考项目一致；导出后在空实例导入，生成物逐字节一致 |
| M3 运行时 | 待应用变更、apply、对账、unit 启停与状态推送、日志 SSE、用户启停热生效、依赖管理、自更新、下载代理 | 在虚拟机上完成「录入 → 应用 → 代理可用 → 停用用户即时生效 → 升级 xray 不丢配置」全流程；人为制造新版本启动失败时自动回滚到上一版本；经带用户名密码的 HTTP 代理和 SOCKS5 代理各完成一次 xray 下载和一次自更新，代理认证失败时作业报出明确原因 |
| M4 订阅 | 订阅监听器、每用户 token、三种格式、模板覆盖与预览、手工节点、远端节点源 | 三种客户端导入订阅成功；两台实例间远端节点同步成功 |
| M5 流量 | 采集器、补采、汇总、实时曲线、明细查询、CSV 导出、配额与到期 | 与 `xray api statsquery` 手工读数对账一致；配额超限自动停用并按月恢复 |
| M6 运维 | 调度器 UI 与内置任务、数据库备份、迁移包导出与恢复（含 `install.sh --restore`）、健康检查、出口 IP、延迟测试、审计日志 | 所有内置任务有执行记录；健康检查覆盖第 12 节全部项目；在另一台虚拟机用迁移包恢复后，订阅内容逐字节一致，流量历史与配额用量连续 |
| M7 加固 | ACME、TOTP、登录限速与锁定、安全审查、端到端测试、用户文档 | 端到端测试在 Debian 12 和 Ubuntu 24.04 上通过 |

测试策略：

- 生成器：golden 测试。
- 应用服务层：使用临时 SQLite 和假 systemd、假 xray API 的单元测试，覆盖校验失败回滚、热生效判定、配额状态机。
- API：httptest 集成测试，覆盖鉴权、CSRF、字段级错误格式。
- 前端：组件测试覆盖条件显示和校验；Playwright 覆盖关键流程。
- 端到端：在 Linux 虚拟机中安装 release 构建，跑完 M3/M4/M5 的完成标准。

## 16. 发版方式

本项目是新产品，不兼容现有部署，上线即全新安装：运行安装脚本 → 登录面板 → 手工录入或导入配置 → 应用。整个上线在一个变更窗口内完成，每一步都在面板上当场核对，**不需要分阶段发版**。

如果同一台机器上已有参考项目的部署，因为 unit 名、目录和服务账号都不同，两者可以共存：先在新面板录入配置并使用不同端口验证，确认后停掉旧服务，再把端口改回原值并应用。

## 17. 已采用的默认决定

以下决定已在方案中采用，如需调整，在开发开始前修改本节和对应章节：

1. 项目名 proxystack-web，安装目录 `/opt/proxystack-web`，unit 前缀 `psw-`。
2. 代理进程由 systemd 模板 unit 承载，管理服务以普通用户运行，靠 polkit 授权。
3. 订阅 token 每用户一个，不提供全局 token 模式。
4. 首期只有一个管理员角色，多管理员与权限分级留作扩展。
5. 前端组件库使用 Naive UI。
6. 跨机器节点汇总使用「远端节点源」拉取，不提供参考项目的订阅 bundle zip 包；配置层面的跨机器迁移使用 5.5 节的导入导出。
7. 首期支持的入站协议与参考项目一致（VMess、Shadowsocks、SOCKS5、HTTP）。新增协议（例如 VLESS/Reality、Trojan）时需要同时扩展生成器、订阅渲染和对应的入站表单，架构上按协议注册即可扩展。
8. 管理员初始密码只在执行 `init` 的终端打印一次；密码重置通过本机命令行发起（直接重置或一次性 Web 重置链接），不提供环境变量重置模式（见 10.1）。
9. 导入导出使用本项目自有格式（默认 YAML），实体按名字引用；导出凭据默认口令加密；导入只写入配置并生成修订版本，不自动应用（见 5.5）。
10. 整机迁移使用加密迁移包（数据库快照 + manifest + 可选证书），不含二进制和生成配置；推荐在新机器安装时用 `--restore` 恢复（见 5.6）。
