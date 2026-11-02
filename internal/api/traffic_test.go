package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
)

// TestTrafficStackQuery 验证 stack 分组参数、空结果结构与用户筛选校验。
func TestTrafficStackQuery(t *testing.T) {
	h := newAdminHarness(t)
	body := h.mustDo(http.MethodGet, "/api/traffic/query?group_by=stack&scope=user&from=0&to=2000000000", nil, http.StatusOK)
	require.Equal(t, map[string]any{"up": float64(0), "down": float64(0)}, body["total"])
	require.Empty(t, body["by_stack"])
	require.Empty(t, body["series"])
	h.mustDo(http.MethodGet, "/api/traffic/query?group_by=invalid", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, "/api/traffic/overview?user_id=-1", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, "/api/traffic/overview?user_id=9999", nil, http.StatusNotFound)
	body = h.mustDo(http.MethodGet, "/api/traffic/overview", nil, http.StatusOK)
	require.Empty(t, body["by_stack"])
	for _, key := range []string{"today", "month", "user_today", "user_month"} {
		require.Equal(t, map[string]any{"up": float64(0), "down": float64(0)}, body[key])
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
	}
	h.mustDo(http.MethodGet, path+"?user_id=-1", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, path+"?user_id=oops", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, path+"?user_id=9999", nil, http.StatusNotFound)
	h.mustDo(http.MethodGet, "/api/stacks/0/usage", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, "/api/stacks/9999/usage", nil, http.StatusNotFound)
}
