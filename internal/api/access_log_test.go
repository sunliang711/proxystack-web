package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAccessLogSettingsAPI 验证表单 API 的默认值、保存读取、三态校验和旧客户端省略字段的兼容。
func TestAccessLogSettingsAPI(t *testing.T) {
	h := newAdminHarness(t)
	settings := h.mustDo(http.MethodGet, "/api/config/settings", nil, http.StatusOK)
	xray := settings["defaults"].(map[string]any)["xray"].(map[string]any)
	require.Equal(t, true, xray["access_log"])
	xray["access_log"] = false
	h.mustDo(http.MethodPut, "/api/config/settings", settings, http.StatusOK)
	settings = h.mustDo(http.MethodGet, "/api/config/settings", nil, http.StatusOK)
	require.Equal(t, false, settings["defaults"].(map[string]any)["xray"].(map[string]any)["access_log"])

	h.mustDo(http.MethodPost, "/api/users", map[string]any{
		"name": "alice", "enabled": true, "uuid": "", "ss_password": "", "remark": "", "display_template": "", "tag": "",
		"sub_token": "", "quota_bytes": 0, "reset_mode": "none", "reset_day": 0, "expire_at": "", "note": "", "profiles": []any{},
	}, http.StatusCreated)
	stack := h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": "edge1", "labels": []string{}, "members": []string{}}, http.StatusCreated)
	path := fmt.Sprintf("/api/stacks/%.0f", stack["id"])
	require.Equal(t, "inherit", stack["xray"].(map[string]any)["access_log"])
	for _, mode := range []string{"on", "off", "inherit"} {
		stack["xray"].(map[string]any)["access_log"] = mode
		h.mustDo(http.MethodPut, path, stack, http.StatusOK)
		stack = h.mustDo(http.MethodGet, path, nil, http.StatusOK)
		require.Equal(t, mode, stack["xray"].(map[string]any)["access_log"])
	}
	stack["xray"].(map[string]any)["access_log"] = "invalid"
	h.mustDo(http.MethodPut, path, stack, http.StatusUnprocessableEntity)
	delete(stack["xray"].(map[string]any), "access_log")
	stack = h.mustDo(http.MethodPut, path, stack, http.StatusOK)
	require.Equal(t, "inherit", stack["xray"].(map[string]any)["access_log"])
}
