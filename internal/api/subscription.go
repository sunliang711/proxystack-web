package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/generator/sub"
)

type manualNodesOutput struct {
	Body []configdoc.ManualNode
}

type remoteSourceView struct {
	configdoc.RemoteSource
	Status app.RemoteSourceStatus `json:"status"`
}

type remoteSourcesOutput struct {
	Body []remoteSourceView
}

type syncInput struct {
	Body struct {
		ID int64 `json:"id" required:"false" doc:"为 0 时同步全部启用的源"`
	}
}

type syncOutput struct {
	Body struct {
		Error string `json:"error"`
	}
}

// TemplatesView 是订阅模板页的数据：当前自定义模板和内置模板。
type TemplatesView struct {
	Current configdoc.Templates `json:"current"`
	Builtin configdoc.Templates `json:"builtin"`
}

type templatesOutput struct {
	Body TemplatesView
}

type templatePreviewInput struct {
	Body struct {
		Format  string `json:"format" enum:"clash,premium,surge"`
		UserID  int64  `json:"user_id"`
		Content string `json:"content" doc:"为空时预览内置模板"`
	}
}

type textOutput struct {
	Body struct {
		Text string `json:"text"`
	}
}

type subscriptionLinksOutput struct {
	Body app.SubscriptionLinks
}

type userPreviewInput struct {
	ID     int64  `path:"id"`
	Format string `query:"format" enum:"clash,premium,surge" default:"clash"`
}

type bundleStatusOutput struct {
	Body app.BundleStatus
}

func registerSubscription(api huma.API, s *Server) {
	reg := func(op huma.Operation) huma.Operation {
		op.Tags = []string{"subscription"}
		return secured(op)
	}

	huma.Register(api, reg(huma.Operation{OperationID: "listManualNodes", Method: http.MethodGet, Path: "/api/manual-nodes", Summary: "手工节点"}),
		func(ctx context.Context, _ *struct{}) (*manualNodesOutput, error) {
			doc, err := s.svc.LoadConfig(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &manualNodesOutput{Body: append([]configdoc.ManualNode{}, doc.ManualNodes...)}
			for index := range out.Body {
				configdoc.MaskManualNode(&out.Body[index])
			}
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getManualNodeSecrets", Method: http.MethodGet, Path: "/api/manual-nodes/secrets", Summary: "查看手工节点明文凭据（记入审计日志）"}),
		func(ctx context.Context, _ *struct{}) (*manualNodesOutput, error) {
			doc, err := s.svc.LoadConfig(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if err := s.svc.AuditSecretView(ctx, actorFrom(ctx), "manual_nodes"); err != nil {
				return nil, toHTTPError(err)
			}
			return &manualNodesOutput{Body: append([]configdoc.ManualNode{}, doc.ManualNodes...)}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "saveManualNodes", Method: http.MethodPut, Path: "/api/manual-nodes", Summary: "保存手工节点列表"}),
		func(ctx context.Context, in *struct{ Body []configdoc.ManualNode }) (*manualNodesOutput, error) {
			nodes, err := s.svc.SaveManualNodes(ctx, actorFrom(ctx), in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &manualNodesOutput{Body: append([]configdoc.ManualNode{}, nodes...)}
			for index := range out.Body {
				configdoc.MaskManualNode(&out.Body[index])
			}
			return out, nil
		})

	listSources := func(ctx context.Context) (*remoteSourcesOutput, error) {
		doc, err := s.svc.LoadConfig(ctx)
		if err != nil {
			return nil, toHTTPError(err)
		}
		statuses, err := s.svc.RemoteSourceStatuses(ctx)
		if err != nil {
			return nil, toHTTPError(err)
		}
		out := &remoteSourcesOutput{Body: []remoteSourceView{}}
		for _, source := range doc.RemoteSources {
			configdoc.MaskRemoteSource(&source)
			out.Body = append(out.Body, remoteSourceView{RemoteSource: source, Status: statuses[source.ID]})
		}
		return out, nil
	}
	huma.Register(api, reg(huma.Operation{OperationID: "listRemoteSources", Method: http.MethodGet, Path: "/api/remote-sources", Summary: "远端节点源"}),
		func(ctx context.Context, _ *struct{}) (*remoteSourcesOutput, error) { return listSources(ctx) })
	huma.Register(api, reg(huma.Operation{OperationID: "saveRemoteSources", Method: http.MethodPut, Path: "/api/remote-sources", Summary: "保存远端节点源列表"}),
		func(ctx context.Context, in *struct{ Body []configdoc.RemoteSource }) (*remoteSourcesOutput, error) {
			if _, err := s.svc.SaveRemoteSources(ctx, actorFrom(ctx), in.Body); err != nil {
				return nil, toHTTPError(err)
			}
			return listSources(ctx)
		})
	huma.Register(api, reg(huma.Operation{OperationID: "syncRemoteSources", Method: http.MethodPost, Path: "/api/remote-sources/sync", Summary: "立即同步远端节点源"}),
		func(ctx context.Context, in *syncInput) (*syncOutput, error) {
			out := &syncOutput{}
			if err := s.svc.SyncRemoteSources(ctx, in.Body.ID); err != nil {
				out.Body.Error = err.Error()
			}
			return out, nil
		})

	huma.Register(api, reg(huma.Operation{OperationID: "getTemplates", Method: http.MethodGet, Path: "/api/sub-templates", Summary: "订阅模板"}),
		func(ctx context.Context, _ *struct{}) (*templatesOutput, error) {
			doc, err := s.svc.LoadConfig(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &templatesOutput{Body: TemplatesView{Current: *doc.Templates}}
			for format, target := range map[string]*string{sub.FormatClash: &out.Body.Builtin.Clash, sub.FormatPremium: &out.Body.Builtin.Premium, sub.FormatSurge: &out.Body.Builtin.Surge} {
				if *target, err = sub.BuiltinTemplate(sub.TemplateNameFor(format)); err != nil {
					return nil, toHTTPError(err)
				}
			}
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "saveTemplates", Method: http.MethodPut, Path: "/api/sub-templates", Summary: "保存订阅模板（空字符串恢复内置模板）"}),
		func(ctx context.Context, in *struct{ Body configdoc.Templates }) (*struct{ Body configdoc.Templates }, error) {
			templates, err := s.svc.SaveTemplates(ctx, actorFrom(ctx), in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body configdoc.Templates }{Body: *templates}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "previewTemplate", Method: http.MethodPost, Path: "/api/sub-templates/preview", Summary: "用指定模板预览某个用户的订阅"}),
		func(ctx context.Context, in *templatePreviewInput) (*textOutput, error) {
			text, err := s.svc.PreviewSubscription(ctx, in.Body.UserID, in.Body.Format, &in.Body.Content)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &textOutput{}
			out.Body.Text = text
			return out, nil
		})

	huma.Register(api, reg(huma.Operation{OperationID: "getUserSubscription", Method: http.MethodGet, Path: "/api/users/{id}/subscription", Summary: "用户的订阅链接（含 token，记入审计日志）"}),
		func(ctx context.Context, in *idPath) (*subscriptionLinksOutput, error) {
			links, err := s.svc.UserSubscriptionLinks(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if err := s.svc.AuditSecretView(ctx, actorFrom(ctx), "subscription_links"); err != nil {
				return nil, toHTTPError(err)
			}
			return &subscriptionLinksOutput{Body: *links}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "previewUserSubscription", Method: http.MethodGet, Path: "/api/users/{id}/subscription/preview", Summary: "预览用户的订阅内容"}),
		func(ctx context.Context, in *userPreviewInput) (*textOutput, error) {
			text, err := s.svc.PreviewSubscription(ctx, in.ID, in.Format, nil)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &textOutput{}
			out.Body.Text = text
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getSubscriptionBundleStatus", Method: http.MethodGet, Path: "/api/sub-bundle", Summary: "订阅包发布状态"}),
		func(ctx context.Context, _ *struct{}) (*bundleStatusOutput, error) {
			status, err := s.svc.SubscriptionBundleStatus(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &bundleStatusOutput{Body: *status}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "publishSubscriptionBundle", Method: http.MethodPost, Path: "/api/sub-bundle/publish", Summary: "签发并下载订阅包（含全部用户的 token，记入审计日志）"}),
		func(ctx context.Context, _ *struct{}) (*exportOutput, error) {
			bundle, err := s.svc.PublishSubscriptionBundle(ctx, actorFrom(ctx))
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &exportOutput{
				ContentType:        "application/octet-stream",
				ContentDisposition: fmt.Sprintf(`attachment; filename="%s"`, bundle.Filename),
				Body:               bundle.Data,
			}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getSettingsSecrets", Method: http.MethodGet, Path: "/api/config/settings/secrets", Summary: "查看设置中的明文凭据（记入审计日志）"}),
		func(ctx context.Context, _ *struct{}) (*settingsOutput, error) {
			doc, err := s.svc.LoadConfig(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if err := s.svc.AuditSecretView(ctx, actorFrom(ctx), "settings"); err != nil {
				return nil, toHTTPError(err)
			}
			return &settingsOutput{Body: *doc.Settings}, nil
		})
}

// exportNodesHandler 是只读节点导出接口，供其他实例的远端节点源凭 Bearer token 拉取本机节点。不需要登录。
func exportNodesHandler(svc *app.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeError := func(status int, code, message string) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(http.StatusUnauthorized, "unauthorized", "a bearer token is required")
			return
		}
		export, allowed, err := svc.ExportNodes(r.Context(), token)
		switch {
		case err != nil:
			slog.Error("export nodes", "err", err)
			writeError(http.StatusServiceUnavailable, "index_unavailable", "nodes are not available")
		case !allowed:
			writeError(http.StatusForbidden, "forbidden", "invalid token or node export is disabled")
		default:
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(export)
		}
	}
}
