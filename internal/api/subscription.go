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
	"proxystack-web/internal/subpublish"
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
type uninstallTargetInput struct {
	ID   int64 `path:"id"`
	Body app.SubUninstallRequest
}

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

type sshPublishIdentityOutput struct {
	Body app.SSHPublishIdentityView
}

type publishTargetsOutput struct {
	Body []app.PublishTargetView
}

type publishTargetOutput struct {
	Body app.PublishTargetView
}

type publishTargetSetupOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         app.PublishTargetSetup
}

type publishTargetCreateInput struct {
	Body app.PublishTargetInput
}

type publishTargetUpdateInput struct {
	ID   int64 `path:"id"`
	Body app.PublishTargetInput
}

type publishTargetProbeOutput struct {
	Body subpublish.ProbeResult
}

type publishTargetDeployStatusOutput struct {
	Body subpublish.DeployStatus
}

type publishTargetDeployInput struct {
	ID   int64 `path:"id"`
	Body app.SubDeployRequest
}

type onlinePublishInput struct {
	Body struct {
		TargetIDs []int64 `json:"target_ids" minItems:"1"`
	}
}

type onlinePublishOutput struct {
	Body app.OnlinePublishStarted
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
	huma.Register(api, reg(huma.Operation{OperationID: "getSSHPublishIdentity", Method: http.MethodGet, Path: "/api/sub-publish/identity", Summary: "SSH 在线发布公钥"}),
		func(ctx context.Context, _ *struct{}) (*sshPublishIdentityOutput, error) {
			identity, err := s.svc.SSHPublishIdentity(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &sshPublishIdentityOutput{Body: *identity}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "generateSSHPublishIdentity", Method: http.MethodPost, Path: "/api/sub-publish/identity", Summary: "生成 SSH 在线发布密钥"}),
		func(ctx context.Context, _ *struct{}) (*sshPublishIdentityOutput, error) {
			identity, err := s.svc.GenerateSSHPublishIdentity(ctx, actorFrom(ctx))
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &sshPublishIdentityOutput{Body: *identity}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listSubscriptionPublishTargets", Method: http.MethodGet, Path: "/api/sub-publish/targets", Summary: "SSH 在线发布目标"}),
		func(ctx context.Context, _ *struct{}) (*publishTargetsOutput, error) {
			targets, err := s.svc.ListPublishTargets(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &publishTargetsOutput{Body: targets}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "createSubscriptionPublishTarget", Method: http.MethodPost, Path: "/api/sub-publish/targets", Summary: "创建 SSH 在线发布目标"}),
		func(ctx context.Context, in *publishTargetCreateInput) (*publishTargetSetupOutput, error) {
			setup, err := s.svc.CreatePublishTarget(ctx, actorFrom(ctx), in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &publishTargetSetupOutput{CacheControl: "no-store", Body: *setup}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "updateSubscriptionPublishTarget", Method: http.MethodPut, Path: "/api/sub-publish/targets/{id}", Summary: "修改 SSH 在线发布目标"}),
		func(ctx context.Context, in *publishTargetUpdateInput) (*publishTargetOutput, error) {
			target, err := s.svc.UpdatePublishTarget(ctx, actorFrom(ctx), in.ID, in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &publishTargetOutput{Body: *target}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getSubscriptionPublishTargetSetup", Method: http.MethodGet, Path: "/api/sub-publish/targets/{id}/setup", Summary: "查看订阅端完整配置（包含发布 Secret，记入审计日志）"}),
		func(ctx context.Context, in *idPath) (*publishTargetSetupOutput, error) {
			setup, err := s.svc.GetPublishTargetSetup(ctx, actorFrom(ctx), in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &publishTargetSetupOutput{CacheControl: "no-store", Body: *setup}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "deleteSubscriptionPublishTarget", Method: http.MethodDelete, Path: "/api/sub-publish/targets/{id}", Summary: "删除 SSH 在线发布目标", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			if err := s.svc.DeletePublishTarget(ctx, actorFrom(ctx), in.ID); err != nil {
				return nil, toHTTPError(err)
			}
			return nil, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "rotateSubscriptionPublishTargetSecret", Method: http.MethodPost, Path: "/api/sub-publish/targets/{id}/rotate-secret", Summary: "轮换订阅端发布 Secret"}),
		func(ctx context.Context, in *idPath) (*publishTargetSetupOutput, error) {
			setup, err := s.svc.RotatePublishTargetSecret(ctx, actorFrom(ctx), in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &publishTargetSetupOutput{CacheControl: "no-store", Body: *setup}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "testSubscriptionPublishTarget", Method: http.MethodPost, Path: "/api/sub-publish/targets/{id}/test", Summary: "测试 SSH 在线发布目标"}),
		func(ctx context.Context, in *idPath) (*publishTargetProbeOutput, error) {
			result, err := s.svc.TestPublishTarget(ctx, actorFrom(ctx), in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &publishTargetProbeOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "inspectSubscriptionTargetDeployment", Method: http.MethodPost, Path: "/api/sub-publish/targets/{id}/deploy/inspect", Summary: "检测订阅端部署环境"}),
		func(ctx context.Context, in *idPath) (*publishTargetDeployStatusOutput, error) {
			result, err := s.svc.InspectPublishTargetDeploy(ctx, actorFrom(ctx), in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &publishTargetDeployStatusOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "deploySubscriptionTarget", Method: http.MethodPost, Path: "/api/sub-publish/targets/{id}/deploy", Summary: "通过 SSH 安装或更新订阅端（后台作业）", DefaultStatus: http.StatusAccepted}),
		func(ctx context.Context, in *publishTargetDeployInput) (*taskIDOutput, error) {
			id, err := s.svc.DeployPublishTarget(ctx, actorFrom(ctx), in.ID, in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &taskIDOutput{}
			out.Body.TaskID = id
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "rollbackSubscriptionTarget", Method: http.MethodPost, Path: "/api/sub-publish/targets/{id}/deploy/rollback", Summary: "通过 SSH 回滚订阅端（后台作业）", DefaultStatus: http.StatusAccepted}),
		func(ctx context.Context, in *idPath) (*taskIDOutput, error) {
			id, err := s.svc.RollbackPublishTarget(ctx, actorFrom(ctx), in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &taskIDOutput{}
			out.Body.TaskID = id
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "uninstallSubscriptionTarget", Method: http.MethodPost, Path: "/api/sub-publish/targets/{id}/deploy/uninstall", Summary: "通过 SSH 卸载订阅端并删除其数据（后台作业）", DefaultStatus: http.StatusAccepted}),
		func(ctx context.Context, in *uninstallTargetInput) (*taskIDOutput, error) {
			id, err := s.svc.UninstallPublishTarget(ctx, actorFrom(ctx), in.ID, in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &taskIDOutput{}
			out.Body.TaskID = id
			return out, nil
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
	huma.Register(api, reg(huma.Operation{OperationID: "publishSubscriptionBundleOnline", Method: http.MethodPost, Path: "/api/sub-bundle/publish-online", Summary: "签发订阅包并通过 SSH 向所选订阅端发布"}),
		func(ctx context.Context, in *onlinePublishInput) (*onlinePublishOutput, error) {
			started, err := s.svc.PublishSubscriptionBundleOnline(ctx, actorFrom(ctx), in.Body.TargetIDs)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &onlinePublishOutput{Body: *started}, nil
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
