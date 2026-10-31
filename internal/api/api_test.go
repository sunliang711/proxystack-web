package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
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
}

func newHarness(t *testing.T) (*harness, string) {
	t.Helper()
	layout := paths.New(t.TempDir())
	st, err := store.Open(filepath.Join(layout.Base, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	svc := app.New(st, layout)
	res, err := svc.Initialize(context.Background(), app.InitOptions{TLSMode: store.TLSProxy, Port: 9443})
	require.NoError(t, err)
	panel, err := store.GetPanelSettings(context.Background(), st.Q)
	require.NoError(t, err)
	static := fstest.MapFS{
		"index.html":    {Data: []byte("<html><head><!--psw:base--></head></html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	srv := httptest.NewServer(api.New(svc, api.Options{PanelPath: panel.Path, Dev: true, Static: static}))
	t.Cleanup(srv.Close)
	h := &harness{t: t, svc: svc, server: srv, base: srv.URL + "/" + panel.Path}
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
	require.Equal(t, "proxy", body["tls_mode"])

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

func TestPanelPathAndStatic(t *testing.T) {
	h, _ := newHarness(t)
	resp, err := http.Get(h.server.URL + "/")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "paths outside the panel path return 404")

	resp, err = http.Get(h.base + "/users/1")
	require.NoError(t, err)
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, buf.String(), `<base href="/`)

	resp, err = http.Get(h.base + "/assets/missing.js")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp, err = http.Get(h.base + "/api/unknown")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
