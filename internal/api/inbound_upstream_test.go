package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInboundUpstreamAPI covers authenticated edits, safe summaries and member preservation without a default relay.
func TestInboundUpstreamAPI(t *testing.T) {
	h := newAdminHarness(t)
	h.mustDo(http.MethodPost, "/api/users", map[string]any{
		"name": "alice", "enabled": true, "uuid": "11111111-1111-4111-8111-111111111111", "ss_password": "", "remark": "", "display_template": "", "tag": "",
		"sub_token": "", "quota_bytes": 0, "reset_mode": "none", "reset_day": 0, "expire_at": "", "note": "", "profiles": []any{},
	}, http.StatusCreated)
	edge := h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": "edge1", "labels": []string{}, "members": []string{}}, http.StatusCreated)
	auto := h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "auto-url-test", "name": "auto", "labels": []string{}, "members": []string{"edge1"}}, http.StatusCreated)
	autoPath := fmt.Sprintf("/api/stacks/%.0f", auto["id"])
	edgePath := fmt.Sprintf("/api/stacks/%.0f", edge["id"])
	upstream := auto["mihomo"].(map[string]any)["upstreams"].([]any)[0].(map[string]any)
	upstream["type"], upstream["inbound"], upstream["user"], upstream["profile"] = "xray-inbound", "vmess", "alice", ""
	h.mustDo(http.MethodPut, autoPath, auto, http.StatusOK)
	inbounds := edge["xray"].(map[string]any)["inbounds"].([]any)
	inbounds[0].(map[string]any)["name"] = "custom-relay"
	h.mustDo(http.MethodPut, edgePath, edge, http.StatusOK)
	saved := h.mustDo(http.MethodPut, autoPath+"/members", map[string]any{"members": []string{"edge1"}}, http.StatusOK)
	got := saved["mihomo"].(map[string]any)["upstreams"].([]any)[0].(map[string]any)
	require.Equal(t, "xray-inbound", got["type"])
	require.Equal(t, "vmess", got["inbound"])
	require.Equal(t, "alice", got["user"])
	preview := h.mustDo(http.MethodGet, autoPath+"/preview", nil, http.StatusOK)
	require.Contains(t, preview["mihomo"], "11111111-1111-4111-8111-111111111111")
	list := h.mustDo(http.MethodGet, "/api/stacks", nil, http.StatusOK)
	for _, item := range list["stacks"].([]any) {
		stack := item.(map[string]any)
		if stack["name"] == "edge1" {
			users := stack["inbounds"].([]any)[1].(map[string]any)["users"].([]any)
			require.Equal(t, "alice", users[0].(map[string]any)["user"])
			require.NotContains(t, fmt.Sprint(users), "11111111-1111-4111-8111-111111111111")
		}
	}
	upstream["profile"] = "missing"
	resp, body := h.do(http.MethodPut, autoPath, auto)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Contains(t, fmt.Sprint(errorLocations(body)), "body.mihomo.upstreams[0].user|")
}
