package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
)

// TestTrafficStackQuery 验证 stack 分组参数、空结果结构与用户筛选校验。
func TestTrafficStatsAPI(t *testing.T) {
	h := newAdminHarness(t)
	for _, view := range []string{"user", "stack"} {
		body := h.mustDo(http.MethodGet, "/api/traffic/stats?view="+view+"&level=month&from=1767196800&to=1798732800&compare=true", nil, http.StatusOK)
		require.Len(t, body["buckets"], 12)
		total := body["total"].(map[string]any)
		require.Equal(t, float64(0), total["up"])
		require.Len(t, total["series_up"], 12)
		require.NotNil(t, body["rows"])
		require.NotNil(t, body["gaps"])
	}
	h.mustDo(http.MethodGet, "/api/traffic/stats?view=invalid", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, "/api/traffic/stats?users=oops", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, "/api/traffic/stats?level=day&from=0&to=2000000000", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, "/api/traffic/stats/export?view=stack&level=day&from=1767225600&to=1767312000", nil, http.StatusOK)
	body := h.mustDo(http.MethodGet, "/api/traffic/ranking?from=1767225600&to=1767312000&compare=true", nil, http.StatusOK)
	require.NotNil(t, body["rows"])
	h.mustDo(http.MethodGet, "/api/traffic/ranking?from=1767225600&to=1767225600", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, "/api/traffic/query", nil, http.StatusNotFound)
	body = h.mustDo(http.MethodGet, "/api/traffic/overview", nil, http.StatusOK)
	require.Empty(t, body["by_stack"])
	for _, key := range []string{"today", "month"} {
		period := body[key].(map[string]any)
		require.Equal(t, map[string]any{"up": float64(0), "down": float64(0)}, period["total"])
		require.Equal(t, map[string]any{"up": float64(0), "down": float64(0)}, period["users"])
	}
}

// TestStackUsageAPI 验证鉴权、参数校验及未产生流量时的响应结构。
func TestStackUsageAPI(t *testing.T) {
	guest, _ := newHarness(t)
	guest.mustDo(http.MethodGet, "/api/stacks/1/usage", nil, http.StatusUnauthorized)
	h := newAdminHarness(t)
	user := h.mustDo(http.MethodPost, "/api/users", configdoc.User{Name: "alice", Enabled: true, ResetMode: "none", DailyLimitAction: "pause", Profiles: []configdoc.Profile{}}, http.StatusCreated)
	stack := h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": "edge1", "labels": []string{}, "members": []string{}}, http.StatusCreated)
	path := fmt.Sprintf("/api/stacks/%.0f/usage", stack["id"])
	for _, suffix := range []string{"", fmt.Sprintf("?user_id=%.0f", user["id"])} {
		body := h.mustDo(http.MethodGet, path+suffix, nil, http.StatusOK)
		require.Equal(t, "edge1", body["stack"])
		require.Equal(t, stack["id"], body["stack_id"])
		require.Len(t, body["daily"], 30)
		for _, key := range []string{"today", "month", "recent"} {
			require.Equal(t, map[string]any{"up": float64(0), "down": float64(0)}, body[key])
		}
		require.Empty(t, body["unattributed"])
	}
	h.mustDo(http.MethodGet, path+"?user_id=-1", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, path+"?user_id=oops", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, path+"?user_id=9999", nil, http.StatusNotFound)
	h.mustDo(http.MethodGet, "/api/stacks/0/usage", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, "/api/stacks/9999/usage", nil, http.StatusNotFound)
}
