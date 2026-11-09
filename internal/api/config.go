package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
)

type idPath struct {
	ID int64 `path:"id"`
}

type emptyOutput struct{}

type settingsOutput struct {
	Body configdoc.Settings
}

type userOutput struct {
	Body configdoc.User
}

// UserReference 是引用某个用户的入站。
type UserReference struct {
	StackID int64  `json:"stack_id"`
	Stack   string `json:"stack"`
	Inbound string `json:"inbound"`
	Profile string `json:"profile"`
}

// UserDetail 是用户详情：用户本身、引用它的入站和当前订阅的节点情况。
type UserDetail struct {
	User       configdoc.User  `json:"user"`
	References []UserReference `json:"references"`
	// Subscription 在订阅数据无法构建时为空，不影响查看和编辑用户。
	Subscription *app.UserSubscriptionStatus `json:"subscription,omitempty"`
}

type userDetailOutput struct {
	Body UserDetail
}

type userListOutput struct {
	Body struct {
		Users []configdoc.User `json:"users"`
	}
}

type userBodyInput struct {
	Body configdoc.User
}

type userUpdateInput struct {
	ID   int64 `path:"id"`
	Body configdoc.User
}

// InboundSummary 是列表和下拉框使用的入站摘要。
type InboundSummary struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Listen   string `json:"listen"`
	Port     int    `json:"port"`
	Sub      bool   `json:"sub"`
}

// StackSummary 是 stack 列表的一行。
type StackSummary struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	DesiredRunning bool   `json:"desired_running"`
	Role           string `json:"role"`
	// RelaysOnly 表示按配置结构是聚合 stack：只经其他 stack 的 relay 出网，与角色标记无关。
	RelaysOnly    bool              `json:"relays_only"`
	Labels        []string          `json:"labels"`
	XrayEnabled   bool              `json:"xray_enabled"`
	MihomoEnabled bool              `json:"mihomo_enabled"`
	Inbounds      []InboundSummary  `json:"inbounds"`
	MihomoSocks   string            `json:"mihomo_socks"`
	MihomoHTTP    string            `json:"mihomo_http"`
	Members       []string          `json:"members"`
	Loopback      bool              `json:"loopback" doc:"全部入站只监听本机回环地址"`
	Outbound      OutboundSummary   `json:"outbound"`
	Upstreams     []UpstreamSummary `json:"upstreams"`
}

// OutboundSummary 是 xray 出站的去向，用于列表页的流量图示。
type OutboundSummary struct {
	Type   string `json:"type" enum:"mihomo,socks5,http,direct"`
	Target string `json:"target" doc:"外部代理的 server:port，其他类型为空"`
}

// UpstreamSummary 是 mihomo 上游的去向：外部节点给出协议和地址，引用其他 stack 时给出 stack 和入站名。
type UpstreamSummary struct {
	Name     string `json:"name"`
	Type     string `json:"type" enum:"raw,xray-socks5"`
	Stack    string `json:"stack"`
	Inbound  string `json:"inbound"`
	Protocol string `json:"protocol"`
	Server   string `json:"server"`
}

type stackListOutput struct {
	Body struct {
		Stacks []StackSummary `json:"stacks"`
	}
}

type stackOutput struct {
	Body configdoc.Stack
}

type stackCreateInput struct {
	Body struct {
		Template string   `json:"template" enum:"pair,auto-url-test,load-balance"`
		Name     string   `json:"name"`
		Labels   []string `json:"labels"`
		Members  []string `json:"members"`
	}
}

type stackUpdateInput struct {
	ID   int64 `path:"id"`
	Body configdoc.Stack
}

type stackCloneInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Name string `json:"name"`
	}
}

type stackMembersInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Members []string `json:"members"`
	}
}

type previewOutput struct {
	Body struct {
		Xray   string   `json:"xray"`
		Mihomo string   `json:"mihomo"`
		Errors []string `json:"errors"`
	}
}

type allocateInput struct {
	Body struct {
		Pool    string `json:"pool" enum:"xray_inbound,xray_api,mihomo_socks,mihomo_http,mihomo_controller"`
		Exclude []int  `json:"exclude" required:"false" doc:"表单中尚未保存但已使用的端口"`
	}
}

type allocateOutput struct {
	Body struct {
		Port int `json:"port"`
	}
}

type checkPortInput struct {
	Port    int   `query:"port" minimum:"1" maximum:"65535"`
	StackID int64 `query:"stack_id" required:"false"`
}

type checkPortOutput struct {
	Body struct {
		ConflictWith string `json:"conflict_with"`
		InUse        bool   `json:"in_use"`
	}
}

// RevisionItem 是配置历史列表的一行。
type RevisionItem struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Actor     string    `json:"actor"`
	Summary   string    `json:"summary"`
}

type revisionListInput struct {
	Limit  int `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset int `query:"offset" default:"0" minimum:"0"`
}

type revisionListOutput struct {
	Body struct {
		Items []RevisionItem `json:"items"`
		Total int64          `json:"total"`
	}
}

type revisionDiffInput struct {
	From int64 `query:"from"`
	To   int64 `query:"to" required:"false" doc:"0 或省略表示当前配置"`
}

type revisionDiffOutput struct {
	Body struct {
		Entries []configdoc.DiffEntry `json:"entries"`
	}
}

type exportInput struct {
	Body struct {
		Scope                  string   `json:"scope" enum:"all,stacks,users,manual_nodes,templates"`
		ManualNodes            []int64  `json:"manual_nodes" required:"false"`
		Stacks                 []string `json:"stacks" required:"false"`
		Users                  []string `json:"users" required:"false"`
		IncludeReferencedUsers bool     `json:"include_referenced_users" required:"false"`
		RevisionID             int64    `json:"revision_id" required:"false"`
		Format                 string   `json:"format" enum:"yaml,json,csv"`
		Secrets                string   `json:"secrets" enum:"plain,encrypted,omitted"`
		Passphrase             string   `json:"passphrase" required:"false"`
	}
}

type exportOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

// ImportBody 是导入预览与执行的请求体。
type ImportBody struct {
	Filename   string   `json:"filename" required:"false"`
	Content    string   `json:"content"`
	CSV        bool     `json:"csv" required:"false"`
	Passphrase string   `json:"passphrase" required:"false"`
	Mode       string   `json:"mode" enum:"merge,replace"`
	Conflict   string   `json:"conflict" enum:"skip,overwrite,rename"`
	Ports      string   `json:"ports" enum:"keep,realloc-conflicts,realloc-all"`
	Exclude    []string `json:"exclude" required:"false"`
}

type importInput struct {
	Body ImportBody
}

// ImportIssue 是导入预览中的一条问题。
type ImportIssue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type importOutput struct {
	Body struct {
		Scope           string                  `json:"scope"`
		Secrets         string                  `json:"secrets"`
		NeedsPassphrase bool                    `json:"needs_passphrase"`
		Entries         []configdoc.ImportEntry `json:"entries"`
		Issues          []ImportIssue           `json:"issues"`
		Valid           bool                    `json:"valid"`
	}
}

func actorFrom(ctx context.Context) app.Actor {
	actor := app.Actor{IP: metaFrom(ctx).IP}
	if sess := sessionFrom(ctx); sess != nil {
		actor.Name = sess.Admin.Username
	}
	return actor
}

func registerConfig(api huma.API, s *Server) {
	reg := func(op huma.Operation) huma.Operation {
		op.Tags = []string{"config"}
		return secured(op)
	}

	huma.Register(api, reg(huma.Operation{OperationID: "getSettings", Method: http.MethodGet, Path: "/api/config/settings", Summary: "读取系统设置"}),
		func(ctx context.Context, _ *struct{}) (*settingsOutput, error) {
			doc, err := s.svc.LoadConfig(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			configdoc.MaskSettings(doc.Settings)
			return &settingsOutput{Body: *doc.Settings}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "updateSettings", Method: http.MethodPut, Path: "/api/config/settings", Summary: "保存系统设置"}),
		func(ctx context.Context, in *struct{ Body configdoc.Settings }) (*settingsOutput, error) {
			doc, err := s.svc.UpdateSettings(ctx, actorFrom(ctx), in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			configdoc.MaskSettings(doc.Settings)
			return &settingsOutput{Body: *doc.Settings}, nil
		})

	huma.Register(api, reg(huma.Operation{OperationID: "listUsers", Method: http.MethodGet, Path: "/api/users", Summary: "用户列表"}),
		func(ctx context.Context, _ *struct{}) (*userListOutput, error) {
			doc, err := s.svc.LoadConfig(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &userListOutput{}
			out.Body.Users = doc.Users
			for index := range out.Body.Users {
				configdoc.MaskUser(&out.Body.Users[index])
			}
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "createUser", Method: http.MethodPost, Path: "/api/users", Summary: "新建用户", DefaultStatus: http.StatusCreated}),
		func(ctx context.Context, in *userBodyInput) (*userOutput, error) {
			user, err := s.svc.CreateUser(ctx, actorFrom(ctx), in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			configdoc.MaskUser(user)
			return &userOutput{Body: *user}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getUser", Method: http.MethodGet, Path: "/api/users/{id}", Summary: "用户详情"}),
		func(ctx context.Context, in *idPath) (*userDetailOutput, error) {
			doc, err := s.svc.LoadConfig(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			index := doc.UserByID(in.ID)
			if index < 0 {
				return nil, toHTTPError(app.ErrNotFound)
			}
			out := &userDetailOutput{Body: UserDetail{User: doc.Users[index], References: []UserReference{}}}
			configdoc.MaskUser(&out.Body.User)
			for _, stack := range doc.Stacks {
				for _, inbound := range stack.Xray.Inbounds {
					for _, ref := range inbound.Users {
						if ref.User == out.Body.User.Name {
							out.Body.References = append(out.Body.References, UserReference{
								StackID: stack.ID, Stack: stack.Name, Inbound: inbound.Name, Profile: ref.Profile,
							})
						}
					}
				}
			}
			if status, err := s.svc.UserSubscriptionStatus(ctx, in.ID); err == nil {
				out.Body.Subscription = status
			} else {
				slog.Warn("user subscription status", "user", out.Body.User.Name, "err", err)
			}
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getUserSecrets", Method: http.MethodGet, Path: "/api/users/{id}/secrets", Summary: "查看用户明文凭据（记入审计日志）"}),
		func(ctx context.Context, in *idPath) (*userOutput, error) {
			doc, err := s.svc.LoadConfig(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			index := doc.UserByID(in.ID)
			if index < 0 {
				return nil, toHTTPError(app.ErrNotFound)
			}
			if err := s.svc.AuditSecretView(ctx, actorFrom(ctx), "user:"+doc.Users[index].Name); err != nil {
				return nil, toHTTPError(err)
			}
			return &userOutput{Body: doc.Users[index]}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "updateUser", Method: http.MethodPut, Path: "/api/users/{id}", Summary: "保存用户"}),
		func(ctx context.Context, in *userUpdateInput) (*userOutput, error) {
			user, err := s.svc.UpdateUser(ctx, actorFrom(ctx), in.ID, in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			configdoc.MaskUser(user)
			return &userOutput{Body: *user}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "resetUserToken", Method: http.MethodPost, Path: "/api/users/{id}/reset-token", Summary: "重置订阅 token"}),
		func(ctx context.Context, in *idPath) (*userOutput, error) {
			user, err := s.svc.ResetUserToken(ctx, actorFrom(ctx), in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			configdoc.MaskUser(user)
			return &userOutput{Body: *user}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "deleteUser", Method: http.MethodDelete, Path: "/api/users/{id}", Summary: "删除用户", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *idPath) (*emptyOutput, error) {
			return nil, toHTTPError(s.svc.DeleteUser(ctx, actorFrom(ctx), in.ID))
		})

	huma.Register(api, reg(huma.Operation{OperationID: "listStacks", Method: http.MethodGet, Path: "/api/stacks", Summary: "Stack 列表"}),
		func(ctx context.Context, _ *struct{}) (*stackListOutput, error) {
			doc, err := s.svc.LoadConfig(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &stackListOutput{}
			out.Body.Stacks = []StackSummary{}
			for _, stack := range doc.Stacks {
				out.Body.Stacks = append(out.Body.Stacks, summarizeStack(stack))
			}
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "createStack", Method: http.MethodPost, Path: "/api/stacks", Summary: "按模板新建 stack", DefaultStatus: http.StatusCreated}),
		func(ctx context.Context, in *stackCreateInput) (*stackOutput, error) {
			stack, err := s.svc.CreateStack(ctx, actorFrom(ctx), configdoc.NewStackOptions{
				Template: in.Body.Template, Name: in.Body.Name, Labels: in.Body.Labels, Members: in.Body.Members,
			})
			if err != nil {
				return nil, toHTTPError(err)
			}
			configdoc.MaskStack(stack)
			return &stackOutput{Body: *stack}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getStack", Method: http.MethodGet, Path: "/api/stacks/{id}", Summary: "Stack 详情"}),
		func(ctx context.Context, in *idPath) (*stackOutput, error) {
			stack, err := s.loadStack(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			configdoc.MaskStack(stack)
			return &stackOutput{Body: *stack}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getStackSecrets", Method: http.MethodGet, Path: "/api/stacks/{id}/secrets", Summary: "查看 stack 明文凭据（记入审计日志）"}),
		func(ctx context.Context, in *idPath) (*stackOutput, error) {
			stack, err := s.loadStack(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if err := s.svc.AuditSecretView(ctx, actorFrom(ctx), "stack:"+stack.Name); err != nil {
				return nil, toHTTPError(err)
			}
			return &stackOutput{Body: *stack}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "updateStack", Method: http.MethodPut, Path: "/api/stacks/{id}", Summary: "保存 stack"}),
		func(ctx context.Context, in *stackUpdateInput) (*stackOutput, error) {
			stack, err := s.svc.UpdateStack(ctx, actorFrom(ctx), in.ID, in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			configdoc.MaskStack(stack)
			return &stackOutput{Body: *stack}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "deleteStack", Method: http.MethodDelete, Path: "/api/stacks/{id}", Summary: "删除 stack", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *idPath) (*emptyOutput, error) {
			return nil, toHTTPError(s.svc.DeleteStack(ctx, actorFrom(ctx), in.ID))
		})
	huma.Register(api, reg(huma.Operation{OperationID: "cloneStack", Method: http.MethodPost, Path: "/api/stacks/{id}/clone", Summary: "克隆 stack", DefaultStatus: http.StatusCreated}),
		func(ctx context.Context, in *stackCloneInput) (*stackOutput, error) {
			stack, err := s.svc.CloneStack(ctx, actorFrom(ctx), in.ID, in.Body.Name)
			if err != nil {
				return nil, toHTTPError(err)
			}
			configdoc.MaskStack(stack)
			return &stackOutput{Body: *stack}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "setStackMembers", Method: http.MethodPut, Path: "/api/stacks/{id}/members", Summary: "设置 auto stack 成员"}),
		func(ctx context.Context, in *stackMembersInput) (*stackOutput, error) {
			stack, err := s.svc.SetStackMembers(ctx, actorFrom(ctx), in.ID, in.Body.Members)
			if err != nil {
				return nil, toHTTPError(err)
			}
			configdoc.MaskStack(stack)
			return &stackOutput{Body: *stack}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "previewStack", Method: http.MethodGet, Path: "/api/stacks/{id}/preview", Summary: "生成预览"}),
		func(ctx context.Context, in *idPath) (*previewOutput, error) {
			preview, err := s.svc.PreviewStack(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &previewOutput{}
			out.Body.Xray, out.Body.Mihomo, out.Body.Errors = preview.Xray, preview.Mihomo, preview.Errors
			if out.Body.Errors == nil {
				out.Body.Errors = []string{}
			}
			return out, nil
		})

	huma.Register(api, reg(huma.Operation{OperationID: "allocatePort", Method: http.MethodPost, Path: "/api/ports/allocate", Summary: "从端口池分配端口"}),
		func(ctx context.Context, in *allocateInput) (*allocateOutput, error) {
			port, err := s.svc.AllocatePort(ctx, in.Body.Pool, in.Body.Exclude)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &allocateOutput{}
			out.Body.Port = port
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "checkPort", Method: http.MethodGet, Path: "/api/ports/check", Summary: "检查端口冲突与占用"}),
		func(ctx context.Context, in *checkPortInput) (*checkPortOutput, error) {
			result, err := s.svc.CheckPort(ctx, in.Port, in.StackID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &checkPortOutput{}
			out.Body.ConflictWith, out.Body.InUse = result.ConflictWith, result.InUse
			return out, nil
		})

	huma.Register(api, reg(huma.Operation{OperationID: "listRevisions", Method: http.MethodGet, Path: "/api/revisions", Summary: "配置历史"}),
		func(ctx context.Context, in *revisionListInput) (*revisionListOutput, error) {
			revisions, total, err := s.svc.ListRevisions(ctx, in.Limit, in.Offset)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &revisionListOutput{}
			out.Body.Items, out.Body.Total = []RevisionItem{}, total
			for _, revision := range revisions {
				out.Body.Items = append(out.Body.Items, RevisionItem(revision))
			}
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "diffRevisions", Method: http.MethodGet, Path: "/api/revisions/diff", Summary: "比较两个版本"}),
		func(ctx context.Context, in *revisionDiffInput) (*revisionDiffOutput, error) {
			entries, err := s.svc.DiffRevisions(ctx, in.From, in.To)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &revisionDiffOutput{}
			out.Body.Entries = entries
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "rollbackRevision", Method: http.MethodPost, Path: "/api/revisions/{id}/rollback", Summary: "回滚到指定版本", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *idPath) (*emptyOutput, error) {
			return nil, toHTTPError(s.svc.RollbackRevision(ctx, actorFrom(ctx), in.ID))
		})

	huma.Register(api, reg(huma.Operation{OperationID: "exportConfig", Method: http.MethodPost, Path: "/api/config/export", Summary: "导出配置文件"}),
		func(ctx context.Context, in *exportInput) (*exportOutput, error) {
			result, err := s.svc.ExportConfig(ctx, actorFrom(ctx), app.ExportOptions{
				Scope: configdoc.ExportScope{
					Scope: in.Body.Scope, Stacks: in.Body.Stacks, Users: in.Body.Users,
					IncludeReferencedUsers: in.Body.IncludeReferencedUsers, ManualNodes: in.Body.ManualNodes,
				},
				RevisionID: in.Body.RevisionID, Format: in.Body.Format, Secrets: in.Body.Secrets, Passphrase: in.Body.Passphrase,
			})
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &exportOutput{
				ContentType:        result.ContentType,
				ContentDisposition: fmt.Sprintf(`attachment; filename="%s"`, result.Filename),
				Body:               result.Data,
			}, nil
		})
	importHandler := func(apply bool) func(context.Context, *importInput) (*importOutput, error) {
		return func(ctx context.Context, in *importInput) (*importOutput, error) {
			req := app.ImportRequest{
				Filename: app.SanitizeFilename(in.Body.Filename), Content: []byte(in.Body.Content), CSV: in.Body.CSV,
				Passphrase: in.Body.Passphrase,
				Options: configdoc.ImportOptions{
					Mode: in.Body.Mode, Conflict: in.Body.Conflict, Ports: in.Body.Ports, Exclude: in.Body.Exclude,
				},
			}
			var preview *app.ImportPreview
			var err error
			if apply {
				preview, err = s.svc.ApplyImport(ctx, actorFrom(ctx), req)
			} else {
				preview, err = s.svc.PreviewImport(ctx, req)
			}
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &importOutput{}
			out.Body.Scope, out.Body.Secrets, out.Body.NeedsPassphrase = preview.Scope, preview.Secrets, preview.NeedsPassphrase
			out.Body.Entries, out.Body.Valid, out.Body.Issues = preview.Entries, preview.Valid, []ImportIssue{}
			for _, issue := range preview.Issues {
				out.Body.Issues = append(out.Body.Issues, ImportIssue(issue))
			}
			return out, nil
		}
	}
	huma.Register(api, reg(huma.Operation{OperationID: "previewImport", Method: http.MethodPost, Path: "/api/config/import/preview", Summary: "导入预览", MaxBodyBytes: 8 << 20}), importHandler(false))
	huma.Register(api, reg(huma.Operation{OperationID: "applyImport", Method: http.MethodPost, Path: "/api/config/import/apply", Summary: "执行导入", MaxBodyBytes: 8 << 20}), importHandler(true))
}

func (s *Server) loadStack(ctx context.Context, id int64) (*configdoc.Stack, error) {
	doc, err := s.svc.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	index := doc.StackByID(id)
	if index < 0 {
		return nil, app.ErrNotFound
	}
	return &doc.Stacks[index], nil
}

// rawEndpoint 从外部节点的 mihomo 字段中取出协议和 server:port。
func rawEndpoint(config configdoc.RawMapping) (protocol, server string) {
	var fields map[string]any
	if config.Node == nil || config.Node.Decode(&fields) != nil {
		return "", ""
	}
	protocol, _ = fields["type"].(string)
	host, _ := fields["server"].(string)
	if host != "" {
		server = net.JoinHostPort(host, fmt.Sprint(fields["port"]))
	}
	return protocol, server
}

func summarizeStack(stack configdoc.Stack) StackSummary {
	summary := StackSummary{
		ID: stack.ID, Name: stack.Name, Enabled: stack.Enabled, DesiredRunning: stack.DesiredRunning, Role: stack.Role,
		RelaysOnly: configdoc.RelaysOnly(&stack), Labels: stack.Labels, XrayEnabled: stack.Xray.Enabled, MihomoEnabled: stack.Mihomo.Enabled,
		Inbounds: []InboundSummary{}, Members: configdoc.Members(&stack), Loopback: true,
		Outbound: OutboundSummary{Type: stack.Xray.Outbound.Type}, Upstreams: []UpstreamSummary{},
	}
	if stack.Xray.Outbound.Type == "socks5" || stack.Xray.Outbound.Type == "http" {
		summary.Outbound.Target = net.JoinHostPort(stack.Xray.Outbound.Server, strconv.Itoa(stack.Xray.Outbound.Port))
	}
	for _, upstream := range stack.Mihomo.Upstreams {
		item := UpstreamSummary{Name: upstream.Name, Type: upstream.Type, Stack: upstream.Stack, Inbound: upstream.Inbound}
		if upstream.Type == "raw" {
			item.Protocol, item.Server = rawEndpoint(upstream.Config)
		}
		summary.Upstreams = append(summary.Upstreams, item)
	}
	for _, inbound := range stack.Xray.Inbounds {
		summary.Inbounds = append(summary.Inbounds, InboundSummary{
			ID: inbound.ID, Name: inbound.Name, Protocol: inbound.Protocol, Listen: inbound.Listen, Port: inbound.Port, Sub: inbound.Sub,
		})
		if inbound.Listen != "127.0.0.1" && inbound.Listen != "::1" {
			summary.Loopback = false
		}
	}
	if stack.Mihomo.Enabled {
		summary.MihomoSocks = fmt.Sprintf("%s:%d", stack.Mihomo.Socks.Listen, stack.Mihomo.Socks.Port)
		if stack.Mihomo.HTTP.Enabled {
			summary.MihomoHTTP = fmt.Sprintf("%s:%d", stack.Mihomo.HTTP.Listen, stack.Mihomo.HTTP.Port)
		}
	}
	return summary
}
