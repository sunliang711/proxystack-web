package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/api"
	"proxystack-web/internal/configdoc"
)

func newAdminHarness(t *testing.T) *harness {
	t.Helper()
	h, password := newHarness(t)
	h.svc.PortChecker = nil
	h.login("admin", password)
	resp, _ := h.do(http.MethodPost, "/api/auth/password", map[string]string{"old_password": password, "new_password": "a-new-password"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	return h
}

func (h *harness) mustDo(method, path string, body any, status int) map[string]any {
	h.t.Helper()
	resp, out := h.do(method, path, body)
	require.Equal(h.t, status, resp.StatusCode, "%s %s: %v", method, path, out)
	return out
}

func errorLocations(body map[string]any) []string {
	locations := []string{}
	errs, _ := body["errors"].([]any)
	for _, item := range errs {
		detail := item.(map[string]any)
		location, _ := detail["location"].(string)
		locations = append(locations, location+"|"+fmt.Sprint(detail["message"]))
	}
	return locations
}

// TestConfigCRUDAndValidation 覆盖用户与 stack 的增改、凭据遮蔽、字段级错误定位、配置历史回滚。
func TestConfigCRUDAndValidation(t *testing.T) {
	h := newAdminHarness(t)

	user := h.mustDo(http.MethodPost, "/api/users", map[string]any{
		"name": "alice", "enabled": true, "uuid": "", "ss_password": "", "remark": "", "display_template": "", "tag": "",
		"sub_token": "", "quota_bytes": 0, "reset_mode": "none", "reset_day": 0, "expire_at": "", "note": "", "profiles": []any{},
	}, http.StatusCreated)
	require.Equal(t, configdoc.MaskedValue, user["uuid"], "secrets are masked")
	userID := int64(user["id"].(float64))

	edge := h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": "edge1", "labels": []string{}, "members": []string{}}, http.StatusCreated)
	h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": "edge2", "labels": []string{}, "members": []string{}}, http.StatusCreated)
	auto := h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "auto-url-test", "name": "auto", "labels": []string{}, "members": []string{"edge1", "edge2"}}, http.StatusCreated)
	autoID := int64(auto["id"].(float64))
	edgeID := int64(edge["id"].(float64))

	// 列表摘要带出站去向和上游引用，供流量图示使用。
	var autoSummary map[string]any
	for _, item := range h.mustDo(http.MethodGet, "/api/stacks", nil, http.StatusOK)["stacks"].([]any) {
		if item.(map[string]any)["name"] == "auto" {
			autoSummary = item.(map[string]any)
		}
	}
	require.Equal(t, "mihomo", autoSummary["outbound"].(map[string]any)["type"])
	upstream := autoSummary["upstreams"].([]any)[0].(map[string]any)
	require.Equal(t, "xray-socks5", upstream["type"])
	require.Equal(t, "edge1", upstream["stack"])
	require.Equal(t, "relay", upstream["inbound"])

	// 保存时与其他 stack 端口冲突：错误定位到请求体里的字段。
	stack := h.mustDo(http.MethodGet, fmt.Sprintf("/api/stacks/%d", edgeID), nil, http.StatusOK)
	other := h.mustDo(http.MethodGet, fmt.Sprintf("/api/stacks/%d", autoID), nil, http.StatusOK)
	inbounds := stack["xray"].(map[string]any)["inbounds"].([]any)
	inbounds[1].(map[string]any)["port"] = other["xray"].(map[string]any)["inbounds"].([]any)[0].(map[string]any)["port"]
	resp, body := h.do(http.MethodPut, fmt.Sprintf("/api/stacks/%d", edgeID), stack)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Contains(t, errorLocations(body)[0], "body.xray.inbounds[1].port|")

	// 改名后 auto 的上游引用跟随；遮蔽的凭据保持原值。
	stack = h.mustDo(http.MethodGet, fmt.Sprintf("/api/stacks/%d", edgeID), nil, http.StatusOK)
	stack["name"] = "edge-renamed"
	h.mustDo(http.MethodPut, fmt.Sprintf("/api/stacks/%d", edgeID), stack, http.StatusOK)
	other = h.mustDo(http.MethodGet, fmt.Sprintf("/api/stacks/%d", autoID), nil, http.StatusOK)
	require.Equal(t, "edge-renamed", other["mihomo"].(map[string]any)["upstreams"].([]any)[0].(map[string]any)["stack"])
	secrets := h.mustDo(http.MethodGet, fmt.Sprintf("/api/stacks/%d/secrets", edgeID), nil, http.StatusOK)
	require.NotEqual(t, configdoc.MaskedValue, secrets["mihomo"].(map[string]any)["controller_secret"])
	require.NotEmpty(t, secrets["mihomo"].(map[string]any)["controller_secret"])

	// 删除被引用的 stack：报跨实体错误。
	resp, body = h.do(http.MethodDelete, fmt.Sprintf("/api/stacks/%d", edgeID), nil)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Contains(t, strings.Join(errorLocations(body), "\n"), "stack auto")

	// 预览生成物。
	preview := h.mustDo(http.MethodGet, fmt.Sprintf("/api/stacks/%d/preview", autoID), nil, http.StatusOK)
	require.Contains(t, preview["mihomo"], "edge1-local")
	require.Empty(t, preview["errors"])

	// 配置历史：比较并回滚到改名前。
	revisions := h.mustDo(http.MethodGet, "/api/revisions", nil, http.StatusOK)
	items := revisions["items"].([]any)
	require.GreaterOrEqual(t, len(items), 5)
	beforeRename := int64(items[1].(map[string]any)["id"].(float64))
	diff := h.mustDo(http.MethodGet, fmt.Sprintf("/api/revisions/diff?from=%d", beforeRename), nil, http.StatusOK)
	require.NotEmpty(t, diff["entries"])
	h.mustDo(http.MethodPost, fmt.Sprintf("/api/revisions/%d/rollback", beforeRename), nil, http.StatusNoContent)
	stack = h.mustDo(http.MethodGet, fmt.Sprintf("/api/stacks/%d", edgeID), nil, http.StatusOK)
	require.Equal(t, "edge1", stack["name"])

	h.mustDo(http.MethodDelete, fmt.Sprintf("/api/users/%d", userID), nil, http.StatusUnprocessableEntity)
}

func (h *harness) download(path string, body any) string {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.base+path, jsonReader(h.t, body))
	require.NoError(h.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.CSRFHeader, h.csrf)
	resp, err := h.client.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	require.Equal(h.t, http.StatusOK, resp.StatusCode, string(data))
	require.Contains(h.t, resp.Header.Get("Content-Disposition"), "attachment")
	return string(data)
}

// TestExportImportRoundTrip 验证口令加密导出后导入到空实例，生成物逐字节一致。
func TestExportImportRoundTrip(t *testing.T) {
	source := newAdminHarness(t)
	source.mustDo(http.MethodPost, "/api/users", map[string]any{
		"name": "bob", "enabled": true, "uuid": "", "ss_password": "", "remark": "", "display_template": "", "tag": "",
		"sub_token": "", "quota_bytes": 0, "reset_mode": "none", "reset_day": 0, "expire_at": "", "note": "", "profiles": []any{},
	}, http.StatusCreated)
	source.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": "jp", "labels": []string{"asia"}, "members": []string{}}, http.StatusCreated)
	auto := source.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "load-balance", "name": "lb", "labels": []string{}, "members": []string{"jp"}}, http.StatusCreated)
	wantPreview := source.mustDo(http.MethodGet, fmt.Sprintf("/api/stacks/%d/preview", int64(auto["id"].(float64))), nil, http.StatusOK)

	file := source.download("/api/config/export", map[string]any{"scope": "all", "format": "yaml", "secrets": "encrypted", "passphrase": "correct horse"})
	require.Contains(t, file, "enc:")

	target := newAdminHarness(t)
	request := map[string]any{"filename": "backup.yaml", "content": file, "mode": "merge", "conflict": "skip", "ports": "keep"}
	preview := target.mustDo(http.MethodPost, "/api/config/import/preview", request, http.StatusOK)
	require.Equal(t, true, preview["needs_passphrase"])

	request["passphrase"] = "wrong passphrase"
	target.mustDo(http.MethodPost, "/api/config/import/preview", request, http.StatusUnprocessableEntity)

	request["passphrase"] = "correct horse"
	preview = target.mustDo(http.MethodPost, "/api/config/import/preview", request, http.StatusOK)
	require.Equal(t, true, preview["valid"], preview)
	result := target.mustDo(http.MethodPost, "/api/config/import/apply", request, http.StatusOK)
	require.Equal(t, true, result["valid"], result)

	stacks := target.mustDo(http.MethodGet, "/api/stacks", nil, http.StatusOK)["stacks"].([]any)
	var lbID int64
	for _, item := range stacks {
		if item.(map[string]any)["name"] == "lb" {
			lbID = int64(item.(map[string]any)["id"].(float64))
		}
	}
	gotPreview := target.mustDo(http.MethodGet, fmt.Sprintf("/api/stacks/%d/preview", lbID), nil, http.StatusOK)
	require.Equal(t, wantPreview["xray"], gotPreview["xray"])
	require.Equal(t, wantPreview["mihomo"], gotPreview["mihomo"])

	// 再次导入同一文件：冲突改名并重新分配冲突端口。
	request["conflict"], request["ports"] = "rename", "realloc-conflicts"
	preview = target.mustDo(http.MethodPost, "/api/config/import/preview", request, http.StatusOK)
	require.Equal(t, true, preview["valid"], preview)
	for _, entry := range preview["entries"].([]any) {
		entry := entry.(map[string]any)
		if entry["kind"] == "stack" {
			require.Equal(t, "rename", entry["action"])
			require.NotEmpty(t, entry["port_changes"])
		}
	}

	// CSV 用户导入。
	csv := "name,remark,quota_gb,reset_mode,reset_day,expire_at,uuid,ss_password,note\ncarol,Carol,10,monthly,1,2027-01-01,,,vip\n"
	preview = target.mustDo(http.MethodPost, "/api/config/import/apply", map[string]any{
		"filename": "users.csv", "content": csv, "csv": true, "mode": "merge", "conflict": "skip", "ports": "keep",
	}, http.StatusOK)
	require.Equal(t, true, preview["valid"], preview)
	users := target.mustDo(http.MethodGet, "/api/users", nil, http.StatusOK)["users"].([]any)
	require.Len(t, users, 2)
}

func jsonReader(t *testing.T, body any) io.Reader {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	return bytes.NewReader(data)
}
