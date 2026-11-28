package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
)

type activeSessionsOutput struct {
	Body struct {
		Sessions []app.ActiveSessionView `json:"sessions"`
	}
}

type revokeSessionInput struct {
	ID             string `path:"id" pattern:"^[a-f0-9]{64}$"`
	ForwardedProto string `header:"X-Forwarded-Proto"`
}

func registerSessions(api huma.API, s *Server) {
	huma.Register(api, secured(huma.Operation{OperationID: "listActiveSessions", Method: http.MethodGet, Path: "/api/auth/sessions", Summary: "当前账号的活跃会话", Tags: []string{"auth"}}),
		func(ctx context.Context, _ *struct{}) (*activeSessionsOutput, error) {
			sessions, err := s.svc.ListActiveSessions(ctx, sessionFrom(ctx))
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &activeSessionsOutput{}
			out.Body.Sessions = sessions
			return out, nil
		})
	huma.Register(api, secured(huma.Operation{OperationID: "revokeSession", Method: http.MethodDelete, Path: "/api/auth/sessions/{id}", Summary: "注销当前账号的指定会话", Tags: []string{"auth"}, DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *revokeSessionInput) (*logoutOutput, error) {
			current, err := s.svc.RevokeSession(ctx, sessionFrom(ctx), in.ID, metaFrom(ctx))
			if err != nil {
				return nil, toHTTPError(err)
			}
			if current {
				return &logoutOutput{SetCookie: s.sessionCookie("", -1, in.ForwardedProto)}, nil
			}
			return nil, nil
		})
}
