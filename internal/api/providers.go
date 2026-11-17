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

// registerProviders keeps subscription operations behind the existing session and CSRF middleware.
func registerProviders(api huma.API, s *Server) {
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
