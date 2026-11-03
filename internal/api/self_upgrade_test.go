package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelfUpgradeScheduleAPI(t *testing.T) {
	h, password := newHarness(t)
	h.login("admin", password)
	response, _ := h.do(http.MethodPost, "/api/auth/password", map[string]string{
		"old_password": password, "new_password": "a-new-password",
	})
	require.Equal(t, http.StatusNoContent, response.StatusCode)

	response, body := h.do(http.MethodPost, "/api/system/upgrade-schedule", map[string]any{
		"target": "v0.3.7", "timeout_seconds": 60, "interval_seconds": 5,
	})
	require.Equal(t, http.StatusAccepted, response.StatusCode, body)
	require.Equal(t, "waiting", body["status"])
	require.Equal(t, "v0.3.7", body["target"])

	response, body = h.do(http.MethodGet, "/api/system/upgrade-schedule", nil)
	require.Equal(t, http.StatusOK, response.StatusCode, body)
	require.Equal(t, "waiting", body["status"])

	response, body = h.do(http.MethodDelete, "/api/system/upgrade-schedule", nil)
	require.Equal(t, http.StatusOK, response.StatusCode, body)
	require.Equal(t, "cancelled", body["status"])
}
