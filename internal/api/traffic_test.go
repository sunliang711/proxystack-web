package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
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
}
