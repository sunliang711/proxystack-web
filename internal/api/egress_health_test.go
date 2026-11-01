package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
)

// TestEgressHealthAPI 验证后台出口状态接口需要鉴权、空列表稳定，以及未应用 Stack 不误报。
func TestEgressHealthAPI(t *testing.T) {
	guest, _ := newHarness(t)
	guest.mustDo(http.MethodGet, "/api/runtime/egress-health", nil, http.StatusUnauthorized)
	h := newAdminHarness(t)
	body := h.mustDo(http.MethodGet, "/api/runtime/egress-health", nil, http.StatusOK)
	require.NotNil(t, body["stacks"])
	require.Empty(t, body["stacks"])
	require.Equal(t, true, body["enabled"])
	h.mustDo(http.MethodPost, "/api/users", configdoc.User{Name: "alice", Enabled: true, ResetMode: "none", DailyLimitAction: "pause", Profiles: []configdoc.Profile{}}, http.StatusCreated)
	stack := h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": "edge1", "labels": []string{}, "members": []string{}}, http.StatusCreated)
	body = h.mustDo(http.MethodGet, "/api/runtime/egress-health", nil, http.StatusOK)
	rows := body["stacks"].([]any)
	require.Len(t, rows, 1)
	row := rows[0].(map[string]any)
	require.Equal(t, stack["id"], row["stack_id"])
	require.Equal(t, "unsupported", row["status"])
	require.EqualValues(t, 0, row["failures"])
}
