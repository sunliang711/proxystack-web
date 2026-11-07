package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/api"
	"proxystack-web/internal/app"
	"proxystack-web/internal/paths"
	"proxystack-web/internal/store"
)

type harness struct {
	t      *testing.T
	svc    *app.Service
	server *httptest.Server
	base   string
	client *http.Client
	csrf   string
	// activity 为 true 时请求带 X-Session-Activity: 1，模拟管理员实际操作。
	activity bool
}

func newHarness(t *testing.T) (*harness, string) {
	t.Helper()
	layout := paths.New(t.TempDir())
	st, err := store.Open(filepath.Join(layout.Base, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	svc := app.New(st, layout)
	res, err := svc.Initialize(context.Background(), app.InitOptions{Port: 9443})
	require.NoError(t, err)
	static := fstest.MapFS{
		"index.html":    {Data: []byte("<html><head><!--psw:base--></head></html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	srv := httptest.NewServer(api.New(svc, api.Options{Dev: true, Static: static}))
	t.Cleanup(srv.Close)
	h := &harness{t: t, svc: svc, server: srv, base: srv.URL}
	h.client = newClient(t)
	return h, res.Password
}

func newClient(t *testing.T) *http.Client {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar}
}

func (h *harness) do(method, path string, body any) (*http.Response, map[string]any) {
	h.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(h.t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, h.base+path, reader)
	require.NoError(h.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.csrf != "" {
		req.Header.Set(api.CSRFHeader, h.csrf)
	}
	if h.activity {
		req.Header.Set(api.ActivityHeader, "1")
	}
	resp, err := h.client.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func (h *harness) login(username, password string) {
	h.t.Helper()
	resp, body := h.do(http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": password})
	require.Equal(h.t, http.StatusOK, resp.StatusCode, body)
	h.csrf = body["csrf_token"].(string)
}

func TestLoginForcesPasswordChange(t *testing.T) {
	h, password := newHarness(t)

	resp, _ := h.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp, _ = h.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": "wrong"})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	h.login("admin", password)
	resp, body := h.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, true, body["admin"].(map[string]any)["must_change_password"])

	resp, _ = h.do(http.MethodGet, "/api/system/info", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "other APIs are blocked until the password is changed")

	csrf := h.csrf
	h.csrf = ""
	resp, _ = h.do(http.MethodPost, "/api/auth/password", map[string]string{"old_password": password, "new_password": "a-new-password"})
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "CSRF token is required")
	h.csrf = csrf

	resp, body = h.do(http.MethodPost, "/api/auth/password", map[string]string{"old_password": password, "new_password": "short"})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Equal(t, "body.new_password", body["errors"].([]any)[0].(map[string]any)["location"])

	resp, _ = h.do(http.MethodPost, "/api/auth/password", map[string]string{"old_password": password, "new_password": "a-new-password"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	resp, body = h.do(http.MethodGet, "/api/system/info", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)

	resp, _ = h.do(http.MethodPost, "/api/auth/logout", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp, _ = h.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestResetLinkIsSingleUseAndRevokesSessions(t *testing.T) {
	h, password := newHarness(t)
	h.login("admin", password)

	token, err := h.svc.CreateResetLink(context.Background(), "", false)
	require.NoError(t, err)
	stale, err := h.svc.CreateResetLink(context.Background(), "", false)
	require.NoError(t, err)

	anon := &harness{t: t, base: h.base, client: newClient(t)}
	resp, _ := anon.do(http.MethodPost, "/api/auth/reset", map[string]string{"token": token, "new_password": "reset-password-1"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a newer link invalidates older ones")

	resp, _ = anon.do(http.MethodPost, "/api/auth/reset", map[string]string{"token": stale, "new_password": "reset-password-1"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp, _ = anon.do(http.MethodPost, "/api/auth/reset", map[string]string{"token": stale, "new_password": "reset-password-2"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "links are single use")

	resp, _ = h.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "existing sessions are revoked")

	anon.login("admin", "reset-password-1")
	_, body := anon.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, false, body["admin"].(map[string]any)["must_change_password"])
}

func TestCLIResetPasswordRevokesSessions(t *testing.T) {
	h, password := newHarness(t)
	h.login("admin", password)
	newPassword, err := h.svc.ResetPassword(context.Background(), app.ResetPasswordOptions{})
	require.NoError(t, err)
	resp, _ := h.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	h.login("admin", newPassword)
}

func TestAccountLockout(t *testing.T) {
	h, password := newHarness(t)
	for range 5 {
		resp, _ := h.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": "wrong"})
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}
	resp, _ := h.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password})
	require.Equal(t, http.StatusLocked, resp.StatusCode)
}

func TestStatic(t *testing.T) {
	h, _ := newHarness(t)
	for _, page := range []string{"/", "/users/1"} {
		resp, err := http.Get(h.base + page)
		require.NoError(t, err)
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		require.Equal(t, http.StatusOK, resp.StatusCode, page)
		require.Contains(t, buf.String(), `<base href="/">`, page)
	}

	resp, err := http.Get(h.base + "/assets/app.js")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, err = http.Get(h.base + "/assets/missing.js")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp, err = http.Get(h.base + "/api/unknown")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestSessionCookieFollowsForwardedProto 验证会话 cookie 只在 nginx 报告 HTTPS 时带 Secure：
// 局域网里 nginx 走 HTTP 时，带 Secure 的 cookie 会被浏览器丢弃，登录静默失败。
func TestSessionCookieFollowsForwardedProto(t *testing.T) {
	h, password := newHarness(t)
	login := func(proto string) *http.Cookie {
		body := strings.NewReader(fmt.Sprintf(`{"username":"admin","password":%q}`, password))
		request, err := http.NewRequest(http.MethodPost, h.base+"/api/auth/login", body)
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		if proto != "" {
			request.Header.Set("X-Forwarded-Proto", proto)
		}
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		for _, cookie := range response.Cookies() {
			if cookie.Name == api.SessionCookie {
				return cookie
			}
		}
		t.Fatal("no session cookie")
		return nil
	}
	require.True(t, login("https").Secure)
	require.False(t, login("http").Secure)
	require.False(t, login("").Secure)
}

// TestPanelURLPrefersConfiguredAddress 验证设置了面板地址后，通知邮件等处用它而不是本机监听地址。
func TestPanelURLPrefersConfiguredAddress(t *testing.T) {
	h, _ := newHarness(t)
	ctx := context.Background()
	info, err := h.svc.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, "0.0.0.0", info.Panel.Listen)
	require.True(t, strings.HasSuffix(info.PanelURL, ":9443/"), info.PanelURL)

	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Basic.PanelURL = "https://panel.example.com/"
	_, err = h.svc.UpdateSettings(ctx, app.Actor{Name: "admin"}, settings)
	require.NoError(t, err)
	info, err = h.svc.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, "https://panel.example.com/", info.PanelURL)

	settings.Basic.PanelURL = "ftp://panel.example.com"
	_, err = h.svc.UpdateSettings(ctx, app.Actor{Name: "admin"}, settings)
	require.Error(t, err)
}

// TestPanelListenCanBeChanged 验证面板监听可以修改，非法地址和与 stack 冲突的端口被拒绝。
func TestPanelListenCanBeChanged(t *testing.T) {
	h, _ := newHarness(t)
	ctx := context.Background()
	actor := app.Actor{Name: "admin"}
	require.NoError(t, h.svc.UpdatePanelListen(ctx, actor, app.PanelListen{Listen: "127.0.0.1", Port: 19000}))
	info, err := h.svc.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", info.Panel.Listen)
	require.Equal(t, 19000, info.Panel.Port)

	require.Error(t, h.svc.UpdatePanelListen(ctx, actor, app.PanelListen{Listen: "localhost", Port: 19000}))
	require.Error(t, h.svc.UpdatePanelListen(ctx, actor, app.PanelListen{Listen: "0.0.0.0", Port: 0}))
}

// TestCreateStackWithoutUsersExplainsWhy 验证没有用户时创建 stack 返回明确的原因，不是入站校验的内部错误。
func TestCreateStackWithoutUsersExplainsWhy(t *testing.T) {
	h, password := newHarness(t)
	h.login("admin", password)
	resp, _ := h.do(http.MethodPost, "/api/auth/password", map[string]string{"old_password": password, "new_password": "NewPassword-12345"})
	require.Less(t, resp.StatusCode, 300)
	h.login("admin", "NewPassword-12345")
	resp, body := h.do(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": "edge1", "labels": []string{}, "members": []string{}})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Contains(t, fmt.Sprint(body["errors"]), "create a user first")
}
