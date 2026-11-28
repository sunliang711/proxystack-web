package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/api"
	"proxystack-web/internal/app"
	"proxystack-web/internal/auth"
	"proxystack-web/internal/store/dbq"
)

const accountTestPassword = "account-test-password"

func accountHarness(t *testing.T) *harness {
	t.Helper()
	h, initial := newHarness(t)
	h.login("admin", initial)
	resp, body := h.do(http.MethodPost, "/api/auth/password", map[string]string{"old_password": initial, "new_password": accountTestPassword})
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
	return h
}

func createAccount(t *testing.T, h *harness, username, role string) int64 {
	t.Helper()
	resp, body := h.do(http.MethodPost, "/api/admins", map[string]string{"current_password": accountTestPassword, "username": username, "password": accountTestPassword, "role": role})
	require.Equal(t, http.StatusCreated, resp.StatusCode, body)
	require.Equal(t, role, body["role"])
	require.Equal(t, true, body["must_change_password"])
	require.NotContains(t, body, "password_hash")
	require.NotContains(t, body, "totp_secret")
	return int64(body["id"].(float64))
}

func accountClient(t *testing.T, h *harness, username string) *harness {
	t.Helper()
	other := &harness{t: t, svc: h.svc, base: h.base, client: newClient(t)}
	other.login(username, accountTestPassword)
	return other
}

func activateAccount(t *testing.T, h *harness) {
	t.Helper()
	resp, body := h.do(http.MethodPost, "/api/auth/password", map[string]string{"old_password": accountTestPassword, "new_password": "activated-account-password"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
}

func TestAccountManagement(t *testing.T) {
	h := accountHarness(t)
	id := createAccount(t, h, "operator", app.RoleViewer)
	operator := accountClient(t, h, "operator")
	resp, _ := operator.do(http.MethodGet, "/api/traffic/overview", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "新账号必须先改密")
	activateAccount(t, operator)
	resp, body := operator.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Equal(t, app.RoleViewer, body["admin"].(map[string]any)["role"])

	resp, body = h.do(http.MethodGet, "/api/admins", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Len(t, body["admins"], 2)
	require.NotContains(t, fmt.Sprint(body), "password_hash")

	path := fmt.Sprintf("/api/admins/%d", id)
	resp, body = h.do(http.MethodPut, path, map[string]string{"current_password": "incorrect-password", "username": "renamed"})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, body)
	require.Equal(t, "body.current_password", firstErrorLocation(body))
	resp, body = h.do(http.MethodPut, path, map[string]string{"current_password": accountTestPassword, "username": "renamed"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
	resp, body = operator.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, "renamed", body["admin"].(map[string]any)["username"])

	resp, body = h.do(http.MethodPost, path+"/disable", map[string]string{"current_password": accountTestPassword})
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
	resp, _ = operator.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp, _ = operator.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "renamed", "password": "activated-account-password"})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "停用账号不能重新登录")
	resp, body = h.do(http.MethodPost, path+"/enable", map[string]string{"current_password": accountTestPassword})
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
	operator.login("renamed", "activated-account-password")

	resp, body = h.do(http.MethodPut, path+"/role", map[string]string{"current_password": accountTestPassword, "role": app.RoleAdmin})
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
	resp, _ = operator.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "角色变更撤销现有会话")
	operator.login("renamed", "activated-account-password")
	resp, body = operator.do(http.MethodGet, "/api/admins", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)

	resp, body = h.do(http.MethodPost, path+"/reset-password", map[string]string{"current_password": accountTestPassword, "new_password": "reset-account-password"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
	resp, _ = operator.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	operator.login("renamed", "reset-account-password")
	resp, body = operator.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, true, body["admin"].(map[string]any)["must_change_password"])
	resp, _ = h.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "操作他人账号保留自己的会话")

	rows, err := h.svc.Store.Q.ListAuditLogs(context.Background(), dbq.ListAuditLogsParams{Limit: 100})
	require.NoError(t, err)
	actions := make([]string, 0, len(rows))
	for _, row := range rows {
		actions = append(actions, row.Action)
		require.NotContains(t, row.Summary, accountTestPassword)
	}
	require.Subset(t, actions, []string{"admin.create", "admin.rename", "admin.disable", "admin.enable", "admin.role_change", "admin.password_reset"})
}

func TestAccountChangesRevokeResetLinks(t *testing.T) {
	h := accountHarness(t)
	for _, test := range []struct {
		name, method, suffix string
		body                 map[string]string
	}{
		{"role", http.MethodPut, "/role", map[string]string{"role": app.RoleAdmin}},
		{"disable", http.MethodPost, "/disable", map[string]string{}},
		{"password", http.MethodPost, "/reset-password", map[string]string{"new_password": "reset-account-password"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			id := createAccount(t, h, "target-"+test.name, app.RoleViewer)
			ctx := context.Background()
			tokenHash := auth.HashToken("reset-link-" + test.name)
			err := h.svc.Store.Q.CreateResetToken(ctx, dbq.CreateResetTokenParams{TokenHash: tokenHash, AdminID: id, CreatedAt: h.svc.Now().Unix(), ExpiresAt: h.svc.Now().Add(time.Hour).Unix()})
			require.NoError(t, err)
			test.body["current_password"] = accountTestPassword
			resp, body := h.do(test.method, fmt.Sprintf("/api/admins/%d%s", id, test.suffix), test.body)
			require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
			// 安全状态变化后，旧重置链接不能重新取得账号访问权。
			_, err = h.svc.Store.Q.GetResetToken(ctx, tokenHash)
			require.ErrorIs(t, err, sql.ErrNoRows)
		})
	}
}

func TestViewerPermissions(t *testing.T) {
	h := accountHarness(t)
	createAccount(t, h, "viewer", app.RoleViewer)
	viewer := accountClient(t, h, "viewer")
	activateAccount(t, viewer)
	for _, path := range []string{"/api/users", "/api/stacks", "/api/config/settings", "/api/traffic/overview", "/api/auth/sessions", "/api/runtime/pending", "/api/system/info", "/api/audit"} {
		t.Run(path, func(t *testing.T) {
			resp, body := viewer.do(http.MethodGet, path, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode, body)
		})
	}
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/api/admins"},
		{http.MethodPost, "/api/admins"},
		{http.MethodPut, "/api/admins/1/role"},
		{http.MethodPost, "/api/admins/1/disable"},
		{http.MethodPost, "/api/admins/1/reset-password"},
		{http.MethodPut, "/api/config/settings"},
		{http.MethodPost, "/api/runtime/apply"},
		{http.MethodPost, "/api/config/export"},
		{http.MethodGet, "/api/config/settings/secrets"},
		{http.MethodGet, "/api/users/1/secrets"},
		{http.MethodGet, "/api/stacks/1/secrets"},
		{http.MethodGet, "/api/stacks/1/preview"},
		{http.MethodGet, "/api/manual-nodes/secrets"},
		{http.MethodGet, "/api/users/1/subscription"},
		{http.MethodGet, "/api/users/1/subscription/preview"},
		{http.MethodGet, "/api/sub-publish/targets/1/setup"},
		{http.MethodGet, "/api/backup/files/example.db/download"},
		{http.MethodGet, "/api/stacks/1/speedtest"},
		{http.MethodPost, "/api/sub-bundle/publish-online"},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			resp, body := viewer.do(test.method, test.path, nil)
			require.Equal(t, http.StatusForbidden, resp.StatusCode, body)
		})
	}
	// 自己的两步验证仍可设置，不受运维只读权限影响。
	resp, body := viewer.do(http.MethodPost, "/api/auth/totp/setup", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
}

func TestAccountValidationAndLastAdmin(t *testing.T) {
	h := accountHarness(t)
	for _, test := range []struct{ username, role, password string }{
		{"admin", app.RoleViewer, accountTestPassword},
		{"bad name", app.RoleAdmin, accountTestPassword},
		{"valid-name", "owner", accountTestPassword},
		{"valid-name", app.RoleAdmin, "short"},
	} {
		resp, body := h.do(http.MethodPost, "/api/admins", map[string]string{"current_password": accountTestPassword, "username": test.username, "role": test.role, "password": test.password})
		require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, body)
	}
	resp, body := h.do(http.MethodPut, "/api/admins/1/role", map[string]string{"current_password": accountTestPassword, "role": app.RoleViewer})
	require.Equal(t, http.StatusConflict, resp.StatusCode, body)
	resp, body = h.do(http.MethodPost, "/api/admins/1/disable", map[string]string{"current_password": accountTestPassword})
	require.Equal(t, http.StatusConflict, resp.StatusCode, body)
	resp, body = h.do(http.MethodPost, "/api/admins/999/disable", map[string]string{"current_password": accountTestPassword})
	require.Equal(t, http.StatusNotFound, resp.StatusCode, body)
	createAccount(t, h, "second-admin", app.RoleAdmin)
	resp, body = h.do(http.MethodPost, "/api/admins/1/disable", map[string]string{"current_password": accountTestPassword})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, body)

	other := accountClient(t, h, "admin")
	resp, body = h.do(http.MethodPost, "/api/admins/1/reset-password", map[string]string{"current_password": accountTestPassword, "new_password": "self-reset-password"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
	resp, body = h.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Equal(t, false, body["admin"].(map[string]any)["must_change_password"])
	resp, _ = other.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestConcurrentLastAdminProtection(t *testing.T) {
	h := accountHarness(t)
	id := createAccount(t, h, "second-admin", app.RoleAdmin)
	second := accountClient(t, h, "second-admin")
	activateAccount(t, second)
	firstSession, err := h.svc.Login(context.Background(), "admin", accountTestPassword, "", app.RequestMeta{})
	require.NoError(t, err)
	secondSession, err := h.svc.Login(context.Background(), "second-admin", "activated-account-password", "", app.RequestMeta{})
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, item := range []struct {
		session  *app.SessionInfo
		password string
		id       int64
	}{{firstSession, accountTestPassword, 1}, {secondSession, "activated-account-password", id}} {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- h.svc.ChangeAccountRole(context.Background(), item.session, item.password, item.id, app.RoleViewer, app.RequestMeta{})
		}()
	}
	close(start)
	group.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, app.ErrLastAdmin)
		}
	}
	require.Equal(t, 1, succeeded)
	count, err := h.svc.Store.Q.CountEnabledAdmins(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestActiveSessionsAndRevocation(t *testing.T) {
	h := accountHarness(t)
	other := accountClient(t, h, "admin")
	resp, body := h.do(http.MethodGet, "/api/auth/sessions", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	sessions := body["sessions"].([]any)
	require.Len(t, sessions, 2)
	var currentID, otherID string
	for _, raw := range sessions {
		item := raw.(map[string]any)
		require.NotContains(t, item, "csrf_token")
		require.NotContains(t, item, "id_hash")
		if item["current"] == true {
			currentID = item["id"].(string)
		} else {
			otherID = item["id"].(string)
		}
	}
	require.NotEmpty(t, currentID)
	require.NotEmpty(t, otherID)
	rows, err := h.svc.Store.Q.ListActiveAdminSessions(context.Background(), dbq.ListActiveAdminSessionsParams{AdminID: 1, Now: 0, CreatedAfter: 0})
	require.NoError(t, err)
	for _, row := range rows {
		require.NotContains(t, fmt.Sprint(body), row.IDHash)
		require.NotContains(t, fmt.Sprint(body), h.csrf)
	}
	csrf := h.csrf
	h.csrf = ""
	resp, _ = h.do(http.MethodDelete, "/api/auth/sessions/"+otherID, nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	h.csrf = csrf
	resp, body = h.do(http.MethodDelete, "/api/auth/sessions/"+otherID, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
	require.Empty(t, resp.Header.Get("Set-Cookie"), "注销其他会话不修改当前 cookie")
	resp, _ = other.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp, _ = h.do(http.MethodDelete, "/api/auth/sessions/"+otherID, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, body = h.do(http.MethodDelete, "/api/auth/sessions/"+currentID, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
	require.Contains(t, resp.Header.Get("Set-Cookie"), "Max-Age=0")
	resp, _ = h.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestSessionOwnershipAndExpiry(t *testing.T) {
	h := accountHarness(t)
	createAccount(t, h, "viewer", app.RoleViewer)
	viewer := accountClient(t, h, "viewer")
	activateAccount(t, viewer)
	_, body := viewer.do(http.MethodGet, "/api/auth/sessions", nil)
	id := body["sessions"].([]any)[0].(map[string]any)["id"].(string)
	resp, _ := h.do(http.MethodDelete, "/api/auth/sessions/"+id, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "管理员也只能注销当前账号自己的会话")

	now := time.Now().Truncate(time.Second)
	for _, test := range []struct {
		name             string
		created, expires time.Time
	}{
		{"idle-expired", now.Add(-time.Hour), now},
		{"absolute-expired", now.Add(-app.SessionMaxLifetime), now.Add(time.Hour)},
		{"absolute-capped", now.Add(-app.SessionMaxLifetime + time.Hour), now.Add(2 * time.Hour)},
	} {
		require.NoError(t, h.svc.Store.Q.CreateSession(context.Background(), dbq.CreateSessionParams{IDHash: auth.HashToken(test.name), AdminID: 1, CsrfToken: "private-csrf", CreatedAt: test.created.Unix(), LastSeenAt: now.Unix(), ExpiresAt: test.expires.Unix(), Ip: test.name}))
	}
	resp, body = h.do(http.MethodGet, "/api/auth/sessions", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Len(t, body["sessions"], 2)
	for _, raw := range body["sessions"].([]any) {
		item := raw.(map[string]any)
		if item["client_ip"] == "absolute-capped" {
			expires, err := time.Parse(time.RFC3339, item["expires_at"].(string))
			require.NoError(t, err)
			require.WithinDuration(t, now.Add(time.Hour), expires, time.Second)
		}
	}
	// 只读账号可以注销自己的会话。
	resp, _ = viewer.do(http.MethodDelete, "/api/auth/sessions/"+id, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestRevocationClosesEventStream(t *testing.T) {
	h := accountHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+"/api/events", nil)
	require.NoError(t, err)
	resp, err := h.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, body := h.do(http.MethodGet, "/api/auth/sessions", nil)
	id := body["sessions"].([]any)[0].(map[string]any)["id"].(string)
	deleted, _ := h.do(http.MethodDelete, "/api/auth/sessions/"+id, nil)
	require.Equal(t, http.StatusNoContent, deleted.StatusCode)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err, "已建立的流应在会话失效后关闭")
}

func TestAccountManagementRequiresAuthenticationAndCSRF(t *testing.T) {
	h := accountHarness(t)
	anon := &harness{t: t, base: h.base, client: newClient(t)}
	for _, path := range []string{"/api/admins", "/api/auth/sessions"} {
		resp, _ := anon.do(http.MethodGet, path, nil)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}
	h.csrf = ""
	resp, _ := h.do(http.MethodPost, "/api/admins", map[string]string{"current_password": accountTestPassword, "username": "blocked", "password": accountTestPassword, "role": app.RoleAdmin})
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	// 响应不会包含认证 cookie 的明文值。
	resp, body := h.do(http.MethodGet, "/api/auth/sessions", nil)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	for _, cookie := range h.client.Jar.Cookies(resp.Request.URL) {
		if cookie.Name == api.SessionCookie {
			require.False(t, strings.Contains(string(raw), cookie.Value))
		}
	}
}
