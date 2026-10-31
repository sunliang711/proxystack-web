package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDisplayTemplateRejectedBeforeSecondStack 覆盖首次保存即报错及修正后多个 Stack 的创建。
func TestDisplayTemplateRejectedBeforeSecondStack(t *testing.T) {
	h := newAdminHarness(t)
	user := map[string]any{
		"name": "alice", "enabled": true, "uuid": "", "ss_password": "", "remark": "", "display_template": ".stack|toUpper", "tag": "",
		"sub_token": "", "quota_bytes": 0, "reset_mode": "none", "reset_day": 0, "expire_at": "", "note": "", "profiles": []any{},
	}
	body := h.mustDo(http.MethodPost, "/api/users", user, http.StatusUnprocessableEntity)
	require.Contains(t, strings.Join(errorLocations(body), "\n"), "body.display_template|")
	require.Contains(t, strings.Join(errorLocations(body), "\n"), "{{ ... }}")

	user["display_template"] = "{{ .stack | toUpper }} {{ .inbound }}"
	created := h.mustDo(http.MethodPost, "/api/users", user, http.StatusCreated)
	userPath := fmt.Sprintf("/api/users/%.0f", created["id"])
	for _, name := range []string{"de1", "usa1"} {
		h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": name, "labels": []string{}, "members": []string{}}, http.StatusCreated)
	}

	// 无效更新不能污染已保存的模板。
	created["display_template"] = ".stack | toUpper"
	h.mustDo(http.MethodPut, userPath, created, http.StatusUnprocessableEntity)
	saved := h.mustDo(http.MethodGet, userPath, nil, http.StatusOK)
	require.Equal(t, user["display_template"], saved["user"].(map[string]any)["display_template"])
}
