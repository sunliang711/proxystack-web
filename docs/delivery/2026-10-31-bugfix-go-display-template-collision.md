# 第二个 Stack 创建时订阅节点重名排查

## 问题与根因

2026-10-31，实例 `10.2.1.115`（v0.0.7）添加第二个 Stack 时出现 `duplicate proxy name for user`。
用户 `eagle` 的 `display_template` 为 `.stack | toUpper`，缺少 Go 模板的 `{{ }}` 分隔符。
解析器将它作为固定文字，导致不同 Stack 为同一用户生成相同的订阅节点名。

## 处理

通过正式用户 API，将该字段修正为 `{{ .stack | toUpper }}`，由服务生成配置修订和审计记录。
初次排查仅修正模板，没有改动源码、凭据、端口、Stack 配置，也没有应用或重启服务。
保留现有节点名唯一性校验；固定文字模板仍按原有规则处理。

## 验证

使用现有 `de1` 配置构造名为 `usa1` 的第二个 Stack，通过导入预览接口校验，端口选择全部重新分配。

- 修正前：预览 `valid=false`，复现相同的重复节点名错误。
- 修正后：同一份预览 `valid=true`，`issues=[]`。
- 再次读取确认：用户模板已持久化；实例仍只包含原有 `de1`，预览没有写入新 Stack。

## 使用说明

展示名模板表达式必须使用 `{{ }}` 包裹，例如 `{{ .stack | toUpper }}`。
`de1`、`usa1` 对应渲染结果为 `DE1`、`USA1`。后续同一 Stack 存在多个订阅入站时，
可使用 `{{ .stack | toUpper }} {{ .inbound }}` 进一步区分节点名。

## 后续产品修复

补全共享展示名输入组件的帮助：Go template 分隔符与管道语法、全部七个变量、四个函数、覆盖与留空规则、节点名唯一性要求，以及可点击插入的完整示例。
修正 `replace` 快捷标签缺少替换参数的问题。用户档案和入站用户覆盖也使用同一组件，并显示准确的字段错误。

配置保存和导入预览统一检查所有层级的模板，包括尚未被 Stack 引用的用户和档案。
`.stack`、`.stack|toUpper`、`.stack | toUpper`、`toUpper .stack` 等明显遗漏分隔符的表达式会提示使用 `{{ ... }}`；完整模板同时检查解析语法。
普通固定文字仍可使用；不执行虚构上下文来检查条件模板，实际渲染时继续校验缺失变量、空结果和节点重名。

涉及文件：

- `internal/domain/validation/display_template.go`：模板输入检查。
- `internal/configdoc/validate.go`：用户、档案、入站、入站用户覆盖的字段校验。
- `web/src/components/TemplateInput.vue`、`web/src/util/options.ts`：完整帮助和快捷表达式。
- `web/src/views/UserDetailView.vue`、`web/src/views/stack/InboundsTab.vue`：复用组件与字段错误显示。
- 三层回归测试分别位于领域校验、配置文档和 API 包的 `display_template_test.go`。

## 修复验证与部署

- `go test ./...`：通过，覆盖首次保存即拒绝错误模板、修正后连续创建两个 Stack、无效更新不污染已存模板，以及现有重名保护。
- `vue-tsc --noEmit -p web/tsconfig.json`：通过。
- `vite build`：通过。
- `git diff --check`：通过。
- 已部署到 `10.2.1.115`，版本 `v0.0.7-template-help (dd0648a-dirty)`，本地与远端 SHA-256 一致，服务重启后 HTTP 检查通过。
- 旧面板二进制保留于 `/opt/proxystack-web/bin/proxystack-web.before-template-help-20261031T151541Z`。
- 浏览器验证完整帮助可展开，`replace` 插入 `{{ .remark | replace " " "-" }}`；保存错误表达式返回 422 并显示字段提示。
- 只读核对原用户模板仍为 `{{ .stack | toUpper }}`；未通过测试创建或保存额外 Stack，未应用 Stack 配置。

## 兼容性与限制

已有的疑似漏写括号模板在下一次保存配置时需要修正。普通文字不作自动改写，合法条件模板也不会因示例上下文渲染为空而被提前拒绝。
语法检查只检查模板能否解析及明显遗漏的分隔符；运行时变量、函数参数与最终节点名仍由实际渲染和唯一性校验保证。

## ChangeLog

### Fixed · 2026-10-31

- 展示名模板：补全语法帮助与完整示例，修正 `replace` 快捷插入，在保存阶段拦截明显遗漏分隔符的表达式，避免到创建第二个 Stack 时才发现节点重名。
