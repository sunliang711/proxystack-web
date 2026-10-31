package api

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
)

// SessionBody 是登录和查询会话的响应。
type SessionBody struct {
	Admin     app.AdminView `json:"admin"`
	CSRFToken string        `json:"csrf_token" doc:"修改类请求需在 X-CSRF-Token 头中携带"`
}

type sessionOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      SessionBody
}

type loginInput struct {
	Body struct {
		Username string `json:"username" minLength:"1" maxLength:"64"`
		Password string `json:"password" minLength:"1" maxLength:"256"`
	}
}

type changePasswordInput struct {
	Body struct {
		OldPassword string `json:"old_password" minLength:"1" maxLength:"256"`
		NewPassword string `json:"new_password" minLength:"1" maxLength:"256"`
	}
}

type resetInput struct {
	Body struct {
		Token       string `json:"token" minLength:"1" maxLength:"128"`
		NewPassword string `json:"new_password" minLength:"1" maxLength:"256"`
	}
}

type currentSessionOutput struct {
	Body SessionBody
}

type logoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

func (s *Server) sessionCookie(value string, maxAge int) http.Cookie {
	return http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/" + s.opts.PanelPath + "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   !s.opts.Dev,
		SameSite: http.SameSiteStrictMode,
	}
}

func registerAuth(api huma.API, s *Server) {
	huma.Register(api, huma.Operation{
		OperationID: "login",
		Method:      http.MethodPost,
		Path:        "/api/auth/login",
		Summary:     "管理员登录",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, in *loginInput) (*sessionOutput, error) {
		sess, err := s.svc.Login(ctx, in.Body.Username, in.Body.Password, metaFrom(ctx))
		if err != nil {
			return nil, toHTTPError(err)
		}
		return &sessionOutput{
			SetCookie: s.sessionCookie(sess.Token, 0),
			Body:      SessionBody{Admin: sess.Admin, CSRFToken: sess.CSRFToken},
		}, nil
	})

	huma.Register(api, secured(huma.Operation{
		OperationID: "getSession",
		Method:      http.MethodGet,
		Path:        "/api/auth/session",
		Summary:     "当前会话",
		Tags:        []string{"auth"},
		Metadata:    map[string]any{metaAllowPending: true},
	}), func(ctx context.Context, _ *struct{}) (*currentSessionOutput, error) {
		sess := sessionFrom(ctx)
		return &currentSessionOutput{Body: SessionBody{Admin: sess.Admin, CSRFToken: sess.CSRFToken}}, nil
	})

	huma.Register(api, secured(huma.Operation{
		OperationID:   "logout",
		Method:        http.MethodPost,
		Path:          "/api/auth/logout",
		Summary:       "退出登录",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusNoContent,
		Metadata:      map[string]any{metaAllowPending: true},
	}), func(ctx context.Context, _ *struct{}) (*logoutOutput, error) {
		if err := s.svc.Logout(ctx, sessionFrom(ctx), metaFrom(ctx)); err != nil {
			return nil, toHTTPError(err)
		}
		return &logoutOutput{SetCookie: s.sessionCookie("", -1)}, nil
	})

	huma.Register(api, secured(huma.Operation{
		OperationID:   "changePassword",
		Method:        http.MethodPost,
		Path:          "/api/auth/password",
		Summary:       "修改密码",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusNoContent,
		Metadata:      map[string]any{metaAllowPending: true},
	}), func(ctx context.Context, in *changePasswordInput) (*struct{}, error) {
		err := s.svc.ChangePassword(ctx, sessionFrom(ctx), in.Body.OldPassword, in.Body.NewPassword, metaFrom(ctx))
		return nil, toHTTPError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID:   "resetPassword",
		Method:        http.MethodPost,
		Path:          "/api/auth/reset",
		Summary:       "通过一次性链接重置密码",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *resetInput) (*struct{}, error) {
		err := s.svc.ResetWithToken(ctx, in.Body.Token, in.Body.NewPassword, metaFrom(ctx))
		return nil, toHTTPError(err)
	})
}

func constantTimeEqual(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
