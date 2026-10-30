package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/eagle/proxystack-web/internal/app"
	"github.com/eagle/proxystack-web/internal/configdoc"
	"github.com/eagle/proxystack-web/internal/subserver"
)

func get(t *testing.T, server *httptest.Server, path string, header ...string) (int, string, http.Header) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
	require.NoError(t, err)
	if len(header) == 2 {
		request.Header.Set(header[0], header[1])
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, string(body), response.Header
}

// TestSubscriptionServesAppliedNodes 验证订阅只包含已应用的节点、按 token 鉴权，手工节点、暂停和停用规则生效。
func TestSubscriptionServesAppliedNodes(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	server := httptest.NewServer(subserver.Handler(h.svc))
	defer server.Close()

	status, _, _ := get(t, server, "/s/alice-token/clash")
	require.Equal(t, http.StatusNotFound, status, "nothing is applied yet")

	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)

	status, body, headers := get(t, server, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "name: usa1 vmess")
	require.Contains(t, body, "name: auto relay socks")
	require.Contains(t, headers.Get("Content-Disposition"), "alice.yaml")
	require.Contains(t, headers.Get("Subscription-Userinfo"), "total=0")

	status, body, _ = get(t, server, "/s/alice-token/surge")
	require.Equal(t, http.StatusOK, status)
	require.True(t, strings.HasPrefix(body, "#!MANAGED-CONFIG "+server.URL+"/s/alice-token/surge"), body[:80])

	status, _, _ = get(t, server, "/s/wrong-token/clash")
	require.Equal(t, http.StatusForbidden, status)

	// 手工节点立即出现在订阅中；逐 stack 暂停去掉该 stack 的节点。
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	_, err = h.svc.SaveManualNodes(ctx, h.actor, append(doc.ManualNodes, configdoc.ManualNode{
		User: "alice", Mode: configdoc.NodeStandard, Protocol: "socks5", Server: "manual.example.com", Port: 1080,
		Remark: "Manual Relay", AuthType: "noauth",
	}))
	require.NoError(t, err)
	alice := doc.Users[doc.UserByName("alice")]
	_, err = h.svc.SetUserPause(ctx, h.actor, alice.ID, true, []string{"usa1"})
	require.NoError(t, err)
	_, body, _ = get(t, server, "/s/alice-token/clash")
	require.Contains(t, body, "name: Manual Relay")
	require.NotContains(t, body, "usa1 vmess")
	require.Contains(t, body, "usa2 relay socks")

	_, err = h.svc.SetUserPause(ctx, h.actor, alice.ID, false, nil)
	require.NoError(t, err)
	status, _, _ = get(t, server, "/s/alice-token/clash")
	require.Equal(t, http.StatusNotFound, status, "disabled users get 404 by default")
}

// TestRemoteSourceSyncsExportedNodes 验证一个实例开启节点导出后，另一个实例通过远端节点源拉取并提供给同名用户。
func TestRemoteSourceSyncsExportedNodes(t *testing.T) {
	ctx := context.Background()
	exporter := newRuntimeHarness(t)
	id, err := exporter.svc.Apply(ctx, exporter.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, exporter.waitTask(t, id).Status)
	doc, err := exporter.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.NodeExport = configdoc.NodeExportSettings{Enabled: true, Token: "export-token-0123456789"}
	_, err = exporter.svc.UpdateSettings(ctx, exporter.actor, settings)
	require.NoError(t, err)
	exportServer := httptest.NewServer(subserver.Handler(exporter.svc))
	defer exportServer.Close()

	status, _, _ := get(t, exportServer, "/export/nodes", "Authorization", "Bearer wrong")
	require.Equal(t, http.StatusForbidden, status)

	// 导入端：只有用户 alice，没有任何 stack。
	importer := newRuntimeHarness(t)
	imported, err := importer.svc.LoadConfig(ctx)
	require.NoError(t, err)
	for _, stack := range imported.Stacks {
		require.NoError(t, importer.svc.DeleteStack(ctx, importer.actor, stack.ID))
	}
	_, err = importer.svc.SaveRemoteSources(ctx, importer.actor, []configdoc.RemoteSource{{
		Name: "east", URL: exportServer.URL + "/export/nodes", Token: "export-token-0123456789", Enabled: true,
	}})
	require.NoError(t, err)
	require.NoError(t, importer.svc.SyncRemoteSources(ctx, 0))

	server := httptest.NewServer(subserver.Handler(importer.svc))
	defer server.Close()
	status, body, _ := get(t, server, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "name: usa1 vmess")
}
