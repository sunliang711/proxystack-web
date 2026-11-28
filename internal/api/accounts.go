package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
)

type accountsOutput struct {
	Body struct {
		Admins []app.AccountView `json:"admins"`
	}
}

type accountOutput struct {
	Body app.AccountView
}

type createAccountInput struct {
	Body struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"256"`
		Username        string `json:"username" minLength:"3" maxLength:"64"`
		Password        string `json:"password" minLength:"1" maxLength:"256"`
		Role            string `json:"role" enum:"admin,viewer"`
	}
}

type renameAccountInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"256"`
		Username        string `json:"username" minLength:"3" maxLength:"64"`
	}
}

type accountRoleInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"256"`
		Role            string `json:"role" enum:"admin,viewer"`
	}
}

type accountPasswordInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"256"`
		NewPassword     string `json:"new_password" minLength:"1" maxLength:"256"`
	}
}

type accountStateInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"256"`
	}
}

func registerAccounts(api huma.API, s *Server) {
	reg := func(op huma.Operation) huma.Operation {
		op.Tags = []string{"accounts"}
		return secured(op)
	}
	huma.Register(api, reg(huma.Operation{OperationID: "listAccounts", Method: http.MethodGet, Path: "/api/admins", Summary: "管理账号列表"}),
		func(ctx context.Context, _ *struct{}) (*accountsOutput, error) {
			admins, err := s.svc.ListAccounts(ctx, sessionFrom(ctx))
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &accountsOutput{}
			out.Body.Admins = admins
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "createAccount", Method: http.MethodPost, Path: "/api/admins", Summary: "创建管理账号（需验证操作者密码）", DefaultStatus: http.StatusCreated}),
		func(ctx context.Context, in *createAccountInput) (*accountOutput, error) {
			admin, err := s.svc.CreateAccount(ctx, sessionFrom(ctx), in.Body.CurrentPassword, in.Body.Username, in.Body.Password, in.Body.Role, metaFrom(ctx))
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &accountOutput{Body: *admin}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "renameAccount", Method: http.MethodPut, Path: "/api/admins/{id}", Summary: "修改管理账号用户名", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *renameAccountInput) (*struct{}, error) {
			return nil, toHTTPError(s.svc.RenameAccount(ctx, sessionFrom(ctx), in.Body.CurrentPassword, in.ID, in.Body.Username, metaFrom(ctx)))
		})
	huma.Register(api, reg(huma.Operation{OperationID: "changeAccountRole", Method: http.MethodPut, Path: "/api/admins/{id}/role", Summary: "修改角色并撤销该账号全部会话", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *accountRoleInput) (*struct{}, error) {
			return nil, toHTTPError(s.svc.ChangeAccountRole(ctx, sessionFrom(ctx), in.Body.CurrentPassword, in.ID, in.Body.Role, metaFrom(ctx)))
		})
	huma.Register(api, reg(huma.Operation{OperationID: "resetAccountPassword", Method: http.MethodPost, Path: "/api/admins/{id}/reset-password", Summary: "重置管理账号密码", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *accountPasswordInput) (*struct{}, error) {
			return nil, toHTTPError(s.svc.ResetAccountPassword(ctx, sessionFrom(ctx), in.Body.CurrentPassword, in.ID, in.Body.NewPassword, metaFrom(ctx)))
		})
	for _, state := range []struct {
		operation string
		path      string
		summary   string
		disabled  bool
	}{
		{"disableAccount", "/api/admins/{id}/disable", "停用管理账号并撤销全部会话", true},
		{"enableAccount", "/api/admins/{id}/enable", "启用管理账号", false},
	} {
		huma.Register(api, reg(huma.Operation{OperationID: state.operation, Method: http.MethodPost, Path: state.path, Summary: state.summary, DefaultStatus: http.StatusNoContent}),
			func(ctx context.Context, in *accountStateInput) (*struct{}, error) {
				return nil, toHTTPError(s.svc.SetAccountDisabled(ctx, sessionFrom(ctx), in.Body.CurrentPassword, in.ID, state.disabled, metaFrom(ctx)))
			})
	}
}
