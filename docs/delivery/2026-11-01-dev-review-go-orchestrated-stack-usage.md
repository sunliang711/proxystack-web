# 入站用户批量添加与详情用量统计

交付日期：2026-11-01。主 Agent 完成实现、测试及页面验证，独立 `review_usage` Agent 评审与复审，文档 Agent 整理本交付记录。

## 功能与操作

- Stack 详情的入站编辑区增加「添加全部」按钮和已选用户数量。按钮只补齐尚未选择的用户，保留已有引用的档案、备注、展示名模板和 tag；新增引用使用 default 档案。暂停用户同样可以加入，但不会被恢复。全部已选或没有用户时按钮禁用。
- 批量添加仅改变入站编辑草稿；确认入站编辑后再保存 Stack，应用后代理配置才会生效。
- 用户详情的用量卡片每 10 秒自动刷新，保留正在编辑但尚未保存的用户信息，并提供手动刷新和最近更新时间。
- Stack 详情新增「用量统计」标签，显示本 Stack 的今日、本月、近 30 天上下行用量及日趋势，可选择指定用户或清空选择查看全部用户，每 10 秒自动刷新。
- 轮询跳过尚未完成的请求，切换统计对象时取消旧请求并防止旧响应覆盖新结果；离开页面或切离 Stack 用量标签时停止轮询。刷新失败保留上次成功结果并显示提示。

## 统计接口与口径

新增管理接口：`GET /api/stacks/{id}/usage?user_id=0`，沿用现有管理员鉴权和参数校验。

| 参数或返回项 | 说明 |
| --- | --- |
| `id` | 大于 0 的 Stack ID；不存在时返回 404 |
| `user_id` | 省略或为 0 时统计全部入站流量；正数时按该用户及其全部凭据档案统计，不存在时返回 404 |
| `today`、`month`、`recent` | 今日、本自然月、近 30 天的上行与下行字节数 |
| `daily` | 按系统设置时区生成的最近 30 个自然日，包含今日，缺失日期补零 |
| `enabled`、`collection` | 流量监控开关及当前 Stack 的最近采集状态 |

全部流量使用入站计数，指定用户时使用用户计数，分别查询以避免重复累计。共享认证入站的流量可能无法归属到用户，指定用户之和不一定等于全部入站总量。本月汇总独立读取月表，避免依赖近 30 天日明细推算完整月份；统计计入现有采集机制中的未落库增量。历史日趋势受数据保留策略影响。

## 文件与配置变更

- 后端新增：`internal/app/stack_usage.go`、`internal/app/stack_usage_test.go`。
- API：`internal/api/traffic.go`、`internal/api/traffic_test.go`；重新生成 `web/openapi.json` 和 `web/src/api/schema.d.ts`。
- 前端新增：`web/src/composables/usePolling.ts`、`web/src/views/stack/UsageTab.vue`。
- 前端修改：`web/src/components/UsageCard.vue`、`web/src/views/stack/InboundsTab.vue`、`web/src/views/stack/StackEditView.vue`。
- 测试及运行入口：`web/tests/polling.test.mjs`、`web/tests/inbounds.test.mjs`、`web/package.json`。
- 使用说明：`docs/user-guide.md`。

未引入新依赖、数据库迁移或部署配置项。本次仅本地交付，未部署远端。

## 验证结果

| 检查 | 结果 |
| --- | --- |
| `go test ./...` | 通过 |
| `npm run build`（包含 `vue-tsc --noEmit`） | 通过 |
| `npm run test:polling` | 4 项通过 |
| `npm run test:inbounds` | 3 项通过 |
| `go test -race ./internal/app ./internal/api -run 'TestStackUsage\|TestTrafficStackQuery\|TestUserStackUsageIncludesPending' -count=1` | 通过 |

后端专项测试覆盖 Stack 隔离、入站与用户两种口径、用户档案合并、自然日和自然月范围、30 天补零、空数据、未知实体、未落库增量及顺序落库前后的统计一致性。API 测试覆盖鉴权、非法参数及空统计结构。轮询测试覆盖 10 秒间隔、慢请求不堆积、切换取消、旧响应隔离、卸载清理与失败恢复。

本地 `127.0.0.1:19443` 隔离开发实例的 Tabbit 浏览器验证：

- 入站选择从 1/3 补齐至 3/3，选满后按钮禁用；确认保存后保留 `alice/phone` 及其覆盖设置，`bob`、`carol` 使用 default 档案。
- 用户详情填写未保存备注，观察约 9974 毫秒间隔的自动刷新后，输入内容仍保留。
- Stack 全部流量显示 3 MB，选择 `alice` 后显示 9 KB（合并默认及额外档案流量），清空筛选后恢复 3 MB；切离用量标签后不再请求该统计接口。
- 桌面及 768 像素宽度截图已目视检查。

额外全包 race 检查未全部通过：既有 `TestMigrationPackageRestoresAnotherInstance` 在 `internal/app/ops_test.go:140` 的 3 秒任务等待中超时，附带该测试的 race 复跑仍超时。普通全量测试通过，本次相关专项 race 检查通过，检查输出没有数据竞态报告。本次未修改无关迁移测试，也不将专项通过表述为全包 race 通过。

## 独立评审与处理

独立 Agent 首次评审提出：如果入站草稿包含同一用户的多个档案，通过用户名选择器重建全部引用可能合并档案。主 Agent 将「添加全部」改为直接追加缺少用户，并按唯一用户名计算已选数量，增加真实 SFC setup 测试验证正常添加、异常草稿的多档案保留和空列表。

进一步使用真实 API 核实发现，`internal/configdoc/validate.go:455` 已按用户名拒绝同一入站重复引用，因此上述多档案状态无法经正常管理 API 保存。独立 Agent 已更正初次可达性判断并撤销 P2 缺陷等级；直接追加保留为符合需求的防御性改进，多档案测试仅验证异常草稿，不视为正常 API 场景回归。

最终评审及复审结论：未发现已证实可达的本次功能缺陷，复审通过。

## 残余限制

采集落库、分级汇总与统计查询沿用已有机制，没有提供跨数据库和内存增量的一致快照；分钟边界可能短暂遗漏增量，或出现今日与月汇总不同步。顺序落库前后的一致性测试不能证明并发快照一致性。本次未扩大范围重构采集器。
