package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
)

// TestUserRemarkAllowsSavingMultipleStackMemberships 覆盖备注用户依次加入多个入站并保存的完整请求路径。
func TestUserRemarkAllowsSavingMultipleStackMemberships(t *testing.T) {
	h := newAdminHarness(t)
	h.mustDo(http.MethodPost, "/api/users", configdoc.User{
		Name: "eagle", Enabled: true, ResetMode: "none", DailyLimitAction: "pause", Profiles: []configdoc.Profile{},
	}, http.StatusCreated)
	paths := []string{}
	for _, name := range []string{"usa1", "usa3"} {
		created := h.mustDo(http.MethodPost, "/api/stacks", map[string]any{
			"template": "pair", "name": name, "labels": []string{}, "members": []string{},
		}, http.StatusCreated)
		paths = append(paths, fmt.Sprintf("/api/stacks/%d", int64(created["id"].(float64))))
	}
	user := h.mustDo(http.MethodPost, "/api/users", configdoc.User{
		Name: "xc", Enabled: true, Remark: "xiaochen", ResetMode: "none", DailyLimitAction: "pause", Profiles: []configdoc.Profile{},
	}, http.StatusCreated)
	userID := int64(user["id"].(float64))

	for _, path := range paths {
		stack := h.mustDo(http.MethodGet, path, nil, http.StatusOK)
		inbound := stack["xray"].(map[string]any)["inbounds"].([]any)[1].(map[string]any)
		inbound["users"] = append(inbound["users"].([]any), configdoc.UserRef{User: "xc", Profile: "default"})
		saved := h.mustDo(http.MethodPut, path, stack, http.StatusOK)
		savedInbound := saved["xray"].(map[string]any)["inbounds"].([]any)[1].(map[string]any)
		require.Equal(t, stack["name"].(string)+" vmess", savedInbound["remark"])
		users := savedInbound["users"].([]any)
		require.Len(t, users, 2)
		require.Equal(t, "xc", users[1].(map[string]any)["user"])
	}

	// 后续修改管理备注仍可保存，持久化后的备注保持完整。
	path := fmt.Sprintf("/api/users/%d", userID)
	detail := h.mustDo(http.MethodGet, path, nil, http.StatusOK)
	user = detail["user"].(map[string]any)
	require.Len(t, detail["references"], 2)
	require.Equal(t, "xiaochen", user["remark"])
	user["remark"] = "小陈"
	h.mustDo(http.MethodPut, path, user, http.StatusOK)
	user = h.mustDo(http.MethodGet, path, nil, http.StatusOK)["user"].(map[string]any)
	require.Equal(t, "小陈", user["remark"])
}
