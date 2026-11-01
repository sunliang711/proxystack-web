package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStackDirectUsersAPI 验证授权列表的表单读写、引用校验与旧客户端兼容。
func TestStackDirectUsersAPI(t *testing.T) {
	h := newAdminHarness(t)
	h.mustDo(http.MethodPost, "/api/users", map[string]any{
		"name": "alice", "enabled": true, "uuid": "", "ss_password": "", "remark": "", "display_template": "", "tag": "",
		"sub_token": "", "quota_bytes": 0, "reset_mode": "none", "reset_day": 0, "expire_at": "", "note": "", "profiles": []any{},
	}, http.StatusCreated)
	stack := h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": "edge1", "labels": []string{}, "members": []string{}}, http.StatusCreated)
	path := fmt.Sprintf("/api/stacks/%.0f", stack["id"])
	require.Empty(t, stack["direct_users"])
	stack["direct_users"] = []string{"alice"}
	stack = h.mustDo(http.MethodPut, path, stack, http.StatusOK)
	require.Equal(t, []any{"alice"}, stack["direct_users"])
	stack = h.mustDo(http.MethodGet, path, nil, http.StatusOK)
	require.Equal(t, []any{"alice"}, stack["direct_users"])
	for _, users := range [][]string{{"missing"}, {"alice", "alice"}} {
		stack["direct_users"] = users
		h.mustDo(http.MethodPut, path, stack, http.StatusUnprocessableEntity)
	}
	delete(stack, "direct_users")
	stack = h.mustDo(http.MethodPut, path, stack, http.StatusOK)
	require.Empty(t, stack["direct_users"])
}
