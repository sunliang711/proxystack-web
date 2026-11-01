# 任务中心

## 背景与实现

后台操作原先自动打开进度弹窗，执行过程中容易阻挡页面操作。本次将后台作业与定时任务执行记录统一到任务中心。

- 顶部“后台任务”入口显示运行中数量和未读失败提醒，失败已读状态按面板地址与管理员账号保存到浏览器。
- 发起任务只显示轻提示。手动打开的任务侧栏没有遮罩，不锁定焦点或页面滚动，点击页面其他区域不会自动关闭。
- 任务中心支持来源、状态筛选和服务端分页，运行中的任务优先展示；详情展示执行者、时间、耗时、错误与完整日志。
- 后台作业与定时执行记录使用来源和 ID 联合标识，避免两张表的 ID 冲突。
- SSE 通知日志和状态变化，300ms 合并刷新；5 秒轮询补偿断线，重连时主动刷新。页面提交的任务离开最近列表后仍继续追踪。
- 原页面仍接收自己提交的任务完成回调，刷新依赖、备份文件或待应用信息；完成事件早于提交响应到达时也补发回调，避免快速任务遗漏刷新。切换账号时取消旧请求，防止迟到结果污染新会话。
- 常规定时任务成功不弹提示；手动任务完成和失败显示轻提示，失败另保留未读标记。
- 修复读取定时任务列表会将运行记录误判为中断的问题：中断恢复仅在调度器启动时执行。

## 主要文件

- `internal/app/task_center.go`：统一列表、详情及运行中日志快照。
- `internal/store/queries/task_center.sql` 及生成代码：合并两类记录、筛选、排序和分页。
- `internal/app/scheduler.go`、`internal/api/runtime.go`：定时任务实时日志和事件、已鉴权接口。
- `web/src/tasks.ts`、`web/src/composables/useBackgroundTask.ts`：全局任务追踪、提醒、会话隔离和页面回调。
- `TaskCenterDrawer.vue`、`TaskDetails.vue`、`TasksView.vue`、`TaskDetailView.vue`：任务侧栏、列表和详情。
- 主布局、路由、运行事件与原任务弹窗使用页面：接入统一入口，删除 `TaskDialog.vue`。
- 后端接口／服务测试与 `web/tests/task-center.test.mjs`：接口校验、日志、分页、通知去重、异常与会话切换。

## 接口与兼容性

- `GET /api/task-center?source=all&status=all&limit=50&offset=0`
  - 来源：`all/task/job`；状态：`all/running/succeeded/failed/skipped/timeout`。
  - `limit` 范围 1–200；`offset` 为非负数。
  - 返回分页记录、筛选总数以及全局运行中数量；列表只返回最新一行日志。
- `GET /api/task-center/{source}/{id}`：返回完整日志；不存在返回 404。
- `GET /api/events`：增加 `job` 事件，携带执行记录 ID 和当前状态。
- 原任务和定时任务接口保留；没有数据库迁移、配置项、环境变量或依赖版本变更。

## 验证

- `go test ./...` 全部通过。
- `go test -race ./internal/app -run 'Test(TaskCenter|ListJobs)' -count=1` 通过。
- `go test -race ./internal/app ./internal/api` 中 API 通过，App 的既有迁移测试因 3 秒任务等待超时失败，未报告数据竞争；没有为此调整无关测试。
- `node --test tests/*.test.mjs`：58 项通过。
- `npm run build`：类型检查和生产构建通过。
- `git diff --check` 通过。
- 浏览器使用隔离响应验证两类同号任务、来源／状态筛选、完整日志、快速完成提示及提交后没有自动弹窗。
- 验证侧栏展开时可以输入页面表单，关闭侧栏后未保存内容保留；仅修改浏览器表单并恢复原值，没有保存测试内容。
- 检查桌面和 390px 手机宽度布局。
- 测试机读取真实历史记录；手动执行只报告状态的“对账”任务，验证完成提示和完整日志“状态一致”。

## 部署与回滚

部署目标：`root@10.2.78.11`，入口 `http://10.2.78.11:9443`，服务 `proxystack-web`。

通过 `GOOS=linux GOARCH=amd64 make build` 构建，将嵌入前端的二进制上传后核对 SHA-256，以 `proxystack-web:proxystack-web`、权限 750 原子替换 `/opt/proxystack-web/bin/proxystack-web`，重启服务并检查页面可用性。

首次部署前备份：`/opt/proxystack-web/bin/proxystack-web.before-task-center-20261101-071331`。

最终部署版本为 `v0.1.6-dirty`，基线提交 `51aba99`，构建时间 `2026-11-01T07:19:35Z`。本地与远端 SHA-256 一致：`93e423ca7cfcf51e2e4d85608df449f655176f51bb896b1a7bacca54cbc1d7d4`，服务状态为 `active`。

回滚时将上述备份恢复为运行二进制并重启服务。数据库结构没有变化。

## 当前边界

任务中心聚合已有后台作业与定时任务执行记录，不改变执行器的并发规则。日志和历史记录沿用现有保留策略；运行中的日志来自内存，进程重启后不能恢复尚未落盘的日志。没有增加任务取消、重试或虚构的百分比进度。

## ChangeLog

### [Unreleased] - 2026-11-01

- Added：统一后台任务中心、全局进度侧栏、失败未读提醒和任务详情。
- Changed：后台操作改为轻提示；定时任务提供实时日志事件，中断恢复仅在调度器启动时执行。
