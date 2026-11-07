package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/api"
	"proxystack-web/internal/app"
	"proxystack-web/internal/auth"
)

// doFrom 以指定的 X-Forwarded-For 发起请求；测试服务器在回环地址上，请求头会被采信。
func (h *harness) doFrom(forwardedFor, method, path string, body any) (*http.Response, map[string]any) {
	h.t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(h.t, err)
	req, err := http.NewRequest(method, h.base+path, bytes.NewReader(raw))
	require.NoError(h.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", forwardedFor)
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

func currentCode(t *testing.T, secret string, offset int64) string {
	t.Helper()
	code, err := auth.TOTPCode(secret, auth.TOTPStep(time.Now())+offset)
	require.NoError(t, err)
	return code
}

func firstErrorLocation(body map[string]any) string {
	errs, _ := body["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	location, _ := errs[0].(map[string]any)["location"].(string)
	return location
}

func TestTOTPEnrollLoginAndDisable(t *testing.T) {
	h, password := newHarness(t)
	h.login("admin", password)
	resp, _ := h.do(http.MethodPost, "/api/auth/password", map[string]string{"old_password": password, "new_password": "a-new-password"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	password = "a-new-password"

	resp, setup := h.do(http.MethodPost, "/api/auth/totp/setup", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, setup)
	secret := setup["secret"].(string)
	require.Contains(t, setup["uri"], "otpauth://totp/")

	other := &harness{t: t, base: h.base, client: newClient(t)}
	other.login("admin", password)

	resp, body := h.do(http.MethodPost, "/api/auth/totp/enable", map[string]string{"totp_code": "000000"})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, body)
	resp, _ = h.do(http.MethodPost, "/api/auth/totp/enable", map[string]string{"totp_code": currentCode(t, secret, -1)})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	resp, body = h.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "the enabling session stays valid")
	require.Equal(t, true, body["admin"].(map[string]any)["totp_enabled"])
	resp, _ = other.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "other sessions are revoked")

	anon := &harness{t: t, base: h.base, client: newClient(t)}
	resp, body = anon.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "body.totp_code", firstErrorLocation(body), "a correct password without a code asks for the code")

	resp, body = anon.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password, "totp_code": "123456"})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "body.totp_code", firstErrorLocation(body))

	code := currentCode(t, secret, 0)
	resp, body = anon.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password, "totp_code": code})
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	anon.csrf = body["csrf_token"].(string)

	replay := &harness{t: t, base: h.base, client: newClient(t)}
	resp, body = replay.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password, "totp_code": code})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "a used code cannot be replayed")
	require.Equal(t, "body.totp_code", firstErrorLocation(body))

	resp, body = anon.do(http.MethodPost, "/api/auth/totp/disable", map[string]string{"password": "wrong-password", "totp_code": currentCode(t, secret, 1)})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Equal(t, "body.password", firstErrorLocation(body))
	resp, _ = anon.do(http.MethodPost, "/api/auth/totp/disable", map[string]string{"password": password, "totp_code": currentCode(t, secret, 1)})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	fresh := &harness{t: t, base: h.base, client: newClient(t)}
	fresh.login("admin", password)
}

func TestTOTPSetupWaitsForPasswordChange(t *testing.T) {
	h, password := newHarness(t)
	h.login("admin", password)
	resp, _ := h.do(http.MethodPost, "/api/auth/totp/setup", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestCLIResetCanDisableTOTP(t *testing.T) {
	h, password := newHarness(t)
	h.login("admin", password)
	resp, _ := h.do(http.MethodPost, "/api/auth/password", map[string]string{"old_password": password, "new_password": "a-new-password"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	_, setup := h.do(http.MethodPost, "/api/auth/totp/setup", nil)
	resp, _ = h.do(http.MethodPost, "/api/auth/totp/enable", map[string]string{"totp_code": currentCode(t, setup["secret"].(string), 0)})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	newPassword, err := h.svc.ResetPassword(context.Background(), app.ResetPasswordOptions{DisableTOTP: true})
	require.NoError(t, err)
	fresh := &harness{t: t, base: h.base, client: newClient(t)}
	fresh.login("admin", newPassword)
	_, body := fresh.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, false, body["admin"].(map[string]any)["totp_enabled"])
}

func TestLoginRateLimitPerAddress(t *testing.T) {
	h, password := newHarness(t)
	for i := range 10 {
		resp, _ := h.doFrom("203.0.113.7", http.MethodPost, "/api/auth/login", map[string]string{"username": "nobody" + strconv.Itoa(i), "password": "wrong"})
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}
	resp, body := h.doFrom("203.0.113.7", http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password})
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, body)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))

	// 客户端自带的 X-Forwarded-For 只出现在前面，限速按 nginx 追加的最后一个地址计算。
	resp, body = h.doFrom("203.0.113.7, 198.51.100.2", http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password})
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	resp, _ = h.doFrom("198.51.100.9, 203.0.113.7", http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password})
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, "a spoofed leading address does not escape the limit")
}

func TestSessionMaxLifetime(t *testing.T) {
	h, password := newHarness(t)
	h.login("admin", password)
	h.activity = true
	start := time.Now()
	// 每次请求都在闲置超时内，会话仍在绝对有效期到达后失效。
	for offset := 10 * time.Hour; offset < app.SessionMaxLifetime; offset += 10 * time.Hour {
		h.svc.Now = func() time.Time { return start.Add(offset) }
		resp, _ := h.do(http.MethodGet, "/api/auth/session", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, offset)
	}
	h.svc.Now = func() time.Time { return start.Add(app.SessionMaxLifetime + time.Minute) }
	resp, _ := h.do(http.MethodGet, "/api/auth/session", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestSessionIdleRenewal 确认只有管理员操作发出的请求续期闲置超时，后台定时读取不续期。
func TestSessionIdleRenewal(t *testing.T) {
	h, password := newHarness(t)
	start := time.Now()
	h.svc.Now = func() time.Time { return start }
	h.login("admin", password)
	at := func(offset time.Duration) { h.svc.Now = func() time.Time { return start.Add(offset) } }
	expiry := func(body map[string]any, key string) time.Time {
		value, err := time.Parse(time.RFC3339, body[key].(string))
		require.NoError(t, err)
		return value
	}

	// 不带操作标记的读取不续期，到期时间保持登录时的值。
	at(6 * time.Hour)
	body := h.mustDo(http.MethodGet, "/api/auth/session", nil, http.StatusOK)
	require.WithinDuration(t, start.Add(app.SessionIdleTimeout), expiry(body, "expires_at"), time.Second)
	require.WithinDuration(t, start.Add(app.SessionMaxLifetime), expiry(body, "max_expires_at"), time.Second)

	// 带操作标记的读取把闲置到期时间往后推，绝对到期时间不变。
	at(10 * time.Hour)
	h.activity = true
	body = h.mustDo(http.MethodGet, "/api/auth/session", nil, http.StatusOK)
	require.WithinDuration(t, start.Add(10*time.Hour+app.SessionIdleTimeout), expiry(body, "expires_at"), time.Second)
	require.WithinDuration(t, start.Add(app.SessionMaxLifetime), expiry(body, "max_expires_at"), time.Second)

	// 之后只有后台读取，距最后一次操作超过闲置超时即失效。
	h.activity = false
	at(10*time.Hour + app.SessionIdleTimeout - time.Hour)
	h.mustDo(http.MethodGet, "/api/auth/session", nil, http.StatusOK)
	at(10*time.Hour + app.SessionIdleTimeout + time.Minute)
	h.mustDo(http.MethodGet, "/api/auth/session", nil, http.StatusUnauthorized)
}

// TestSessionRenewedByWrites 确认修改类请求即使不带操作标记也续期。
func TestSessionRenewedByWrites(t *testing.T) {
	h, password := newHarness(t)
	start := time.Now()
	h.svc.Now = func() time.Time { return start }
	h.login("admin", password)
	h.svc.Now = func() time.Time { return start.Add(app.SessionIdleTimeout - time.Hour) }
	h.mustDo(http.MethodPost, "/api/auth/password", map[string]string{"old_password": password, "new_password": "a-new-password"}, http.StatusNoContent)
	h.svc.Now = func() time.Time { return start.Add(app.SessionIdleTimeout + time.Hour) }
	h.mustDo(http.MethodGet, "/api/auth/session", nil, http.StatusOK)
}
