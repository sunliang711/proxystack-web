package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
)

type providersOutput struct {
	Body []app.ProviderView
}

type providerUpdateInput struct {
	ID         int64 `path:"id"`
	ProviderID int64 `path:"provider_id"`
}

type providerNodesOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         []app.ProviderNodeSummary
}

type providerNodeDetailsInput struct {
	ID         int64 `path:"id"`
	ProviderID int64 `path:"provider_id"`
	Body       struct {
		Name   string `json:"name" minLength:"1" maxLength:"4096"`
		Reveal bool   `json:"reveal"`
	}
}

type providerNodeDetailsOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         app.ProviderNodeDetails
}

// registerProviders keeps subscription operations behind the existing session and CSRF middleware.
func registerProviders(api huma.API, s *Server) {
	huma.Register(api, secured(huma.Operation{OperationID: "listProviderNodes", Method: http.MethodGet, Path: "/api/stacks/{id}/providers/{provider_id}/nodes", Tags: []string{"ops"}, Summary: "上游订阅节点的地址和端口"}),
		func(ctx context.Context, in *providerUpdateInput) (*providerNodesOutput, error) {
			nodes, err := s.svc.ListProviderNodes(ctx, in.ID, in.ProviderID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &providerNodesOutput{CacheControl: "no-store", Body: nodes}, nil
		})
	huma.Register(api, secured(huma.Operation{OperationID: "getProviderNodeDetails", Method: http.MethodPost, Path: "/api/stacks/{id}/providers/{provider_id}/nodes/detail", Tags: []string{"ops"}, Summary: "查看上游订阅节点配置，查看凭据记入审计"}),
		func(ctx context.Context, in *providerNodeDetailsInput) (*providerNodeDetailsOutput, error) {
			details, err := s.svc.GetProviderNodeDetails(ctx, actorFrom(ctx), in.ID, in.ProviderID, in.Body.Name, in.Body.Reveal)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &providerNodeDetailsOutput{CacheControl: "no-store", Body: *details}, nil
		})
	huma.Register(api, secured(huma.Operation{OperationID: "listStackProviders", Method: http.MethodGet, Path: "/api/stacks/{id}/providers", Tags: []string{"ops"}, Summary: "上游订阅的运行状态和节点"}),
		func(ctx context.Context, in *idPath) (*providersOutput, error) {
			views, err := s.svc.ListStackProviders(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &providersOutput{Body: views}, nil
		})
	huma.Register(api, secured(huma.Operation{OperationID: "updateStackProvider", Method: http.MethodPost, Path: "/api/stacks/{id}/providers/{provider_id}/update", Tags: []string{"ops"}, Summary: "立即更新上游订阅", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *providerUpdateInput) (*struct{}, error) {
			if err := s.svc.UpdateStackProvider(ctx, actorFrom(ctx), in.ID, in.ProviderID); err != nil {
				return nil, toHTTPError(err)
			}
			return nil, nil
		})
}
