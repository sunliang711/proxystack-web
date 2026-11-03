// Package api 实现管理端 HTTP 层：OpenAPI 操作、鉴权中间件和前端静态资源。
package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"proxystack-web/internal/app"
	"proxystack-web/internal/version"
)

// SessionCookie 是会话 cookie 名。
const SessionCookie = "psw_session"

// CSRFHeader 是修改类请求必须携带的 CSRF 头。
const CSRFHeader = "X-CSRF-Token"

const (
	securityScheme = "session"
	// metaAllowPending 标记在「首次登录强制改密」期间仍允许调用的操作。
	metaAllowPending = "allowPendingPasswordChange"
)

type ctxKey int

const (
	ctxSession ctxKey = iota
	ctxMeta
)

// Options 是管理端 HTTP 层的配置。
type Options struct {
	// Dev 为开发模式。
	Dev bool
	// Static 是前端构建产物；为空时不提供静态资源。
	Static StaticFS
}

// Server 是管理端 HTTP 处理器。
type Server struct {
	svc     *app.Service
	opts    Options
	api     huma.API
	handler http.Handler
}

func init() {
	// 响应中的列表总是输出 []，请求体也按非空列表校验，前端类型因此不含 null。
	huma.DefaultArrayNullable = false
}

// NewConfig 返回 Huma 配置；OpenAPI 文档与交互文档不对外暴露。
func NewConfig() huma.Config {
	config := huma.DefaultConfig("proxystack-web", version.Current().Version)
	config.OpenAPIPath = ""
	config.DocsPath = ""
	config.SchemasPath = ""
	// 不在响应体中附加 $schema 链接字段。
	config.CreateHooks = nil
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		securityScheme: {Type: "apiKey", In: "cookie", Name: SessionCookie},
	}
	return config
}

// New 创建管理端处理器，挂载在根路径下。
func New(svc *app.Service, opts Options) *Server {
	s := &Server{svc: svc, opts: opts}
	root := chi.NewRouter()
	root.Use(middleware.Recoverer, securityHeaders)
	root.Use(forwardedFor)
	root.NotFound(http.NotFound)
	s.api = humachi.New(root, NewConfig())
	s.api.UseMiddleware(s.authMiddleware)
	s.register(s.api)
	if svc != nil {
		root.Get("/export/nodes", exportNodesHandler(svc))
	}
	root.HandleFunc("/api/*", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	if opts.Static != nil {
		root.Handle("/*", newSPAHandler(opts.Static, "/"))
	}
	s.handler = root
	return s
}

// OpenAPI 返回只注册操作、不依赖数据库的 OpenAPI 文档，供前端生成类型。
func OpenAPI() *huma.OpenAPI {
	s := &Server{}
	api := humachi.New(chi.NewRouter(), NewConfig())
	s.register(api)
	return api.OpenAPI()
}

func (s *Server) register(api huma.API) {
	registerAuth(api, s)
	registerSystem(api, s)
	registerConfig(api, s)
	registerRuntime(api, s)
	registerDeps(api, s)
	registerSubscription(api, s)
	registerTraffic(api, s)
	registerOps(api, s)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// authMiddleware 对声明了 session 安全要求的操作校验会话与 CSRF。
func (s *Server) authMiddleware(ctx huma.Context, next func(huma.Context)) {
	ctx = huma.WithValue(ctx, ctxMeta, app.RequestMeta{IP: clientIP(ctx.RemoteAddr()), UserAgent: ctx.Header("User-Agent")})
	op := ctx.Operation()
	if !requiresSession(op) {
		next(ctx)
		return
	}
	cookie, err := huma.ReadCookie(ctx, SessionCookie)
	if err != nil {
		_ = huma.WriteErr(s.api, ctx, http.StatusUnauthorized, app.ErrUnauthenticated.Error())
		return
	}
	sess, err := s.svc.Authenticate(ctx.Context(), cookie.Value)
	if err != nil {
		if !errors.Is(err, app.ErrUnauthenticated) {
			slog.Error("authenticate session", "err", err)
		}
		_ = huma.WriteErr(s.api, ctx, http.StatusUnauthorized, app.ErrUnauthenticated.Error())
		return
	}
	switch ctx.Method() {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		if !constantTimeEqual(ctx.Header(CSRFHeader), sess.CSRFToken) {
			_ = huma.WriteErr(s.api, ctx, http.StatusForbidden, "missing or invalid CSRF token")
			return
		}
	}
	if sess.Admin.MustChangePassword && op.Metadata[metaAllowPending] != true {
		_ = huma.WriteErr(s.api, ctx, http.StatusForbidden, "password change required")
		return
	}
	next(huma.WithValue(ctx, ctxSession, sess))
}

func requiresSession(op *huma.Operation) bool {
	for _, req := range op.Security {
		if _, ok := req[securityScheme]; ok {
			return true
		}
	}
	return false
}

func sessionFrom(ctx context.Context) *app.SessionInfo {
	sess, _ := ctx.Value(ctxSession).(*app.SessionInfo)
	return sess
}

func metaFrom(ctx context.Context) app.RequestMeta {
	meta, _ := ctx.Value(ctxMeta).(app.RequestMeta)
	return meta
}

// secured 返回带 session 安全要求的操作定义。
func secured(op huma.Operation) huma.Operation {
	op.Security = []map[string][]string{{securityScheme: {}}}
	return op
}

func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// forwardedFor 在反向代理模式下，仅当直连方为回环地址时采用 X-Forwarded-For 的最后一个地址：
// 它是本机 nginx 写入的对端地址。前面的地址来自客户端自带的请求头，可以伪造，
// 不能用于审计和登录限速。
func forwardedFor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := net.ParseIP(clientIP(r.RemoteAddr)); ip != nil && ip.IsLoopback() {
			if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
				hops := strings.Split(values[len(values)-1], ",")
				last := strings.TrimSpace(hops[len(hops)-1])
				if net.ParseIP(last) != nil {
					r.RemoteAddr = net.JoinHostPort(last, "0")
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'")
		next.ServeHTTP(w, r)
	})
}

// toHTTPError 把应用层错误映射为 HTTP 错误；未知错误只记录日志，不把细节返回给客户端。
func toHTTPError(err error) error {
	var fe *app.FieldError
	var ve *app.ValidationError
	var tooMany *app.TooManyAttemptsError
	var remote *app.RemoteError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, app.ErrTOTPRequired), errors.Is(err, app.ErrInvalidTOTP):
		// 两步验证失败按认证失败处理，字段位置让前端显示验证码输入框。
		errors.As(err, &fe)
		return huma.Error401Unauthorized(fe.Message, &huma.ErrorDetail{Location: "body." + fe.Field, Message: fe.Message})
	case errors.As(err, &tooMany):
		seconds := max(int(tooMany.RetryAfter.Seconds()), 1)
		return huma.ErrorWithHeaders(huma.Error429TooManyRequests(err.Error()), http.Header{"Retry-After": {strconv.Itoa(seconds)}})
	case errors.As(err, &fe):
		return huma.Error422UnprocessableEntity("validation failed", &huma.ErrorDetail{Location: "body." + fe.Field, Message: fe.Message})
	case errors.As(err, &ve):
		details := make([]error, 0, len(ve.Issues))
		for _, issue := range ve.Translate() {
			location := ""
			if issue.Field != "" {
				location = "body." + issue.Field
			}
			details = append(details, &huma.ErrorDetail{Location: location, Message: issue.Message})
		}
		return huma.Error422UnprocessableEntity("validation failed", details...)
	case errors.As(err, &remote):
		return huma.Error502BadGateway(remote.Message)
	case errors.Is(err, app.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, app.ErrInvalidCredentials), errors.Is(err, app.ErrUnauthenticated):
		return huma.Error401Unauthorized(err.Error())
	case errors.Is(err, app.ErrAccountLocked):
		return huma.NewError(http.StatusLocked, err.Error())
	case errors.Is(err, app.ErrInvalidResetToken):
		return huma.Error400BadRequest(err.Error())
	default:
		slog.Error("request failed", "err", err)
		return huma.Error500InternalServerError("internal error")
	}
}
