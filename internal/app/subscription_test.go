package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/api"
	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/subserver"
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

// subServer 是一个独立目录中的订阅端，信任给定的来源。
type subServer struct {
	layout subserver.Layout
	server *subserver.Server
	http   *httptest.Server
}

func newSubServer(t *testing.T, sources ...subserver.SourceConfig) *subServer {
	t.Helper()
	layout := subserver.Layout{Dir: t.TempDir()}
	for _, dir := range []string{layout.DataDir(), layout.InboxDir()} {
		require.NoError(t, os.MkdirAll(dir, 0o750))
	}
	config := subserver.DefaultConfig()
	config.Sources = sources
	require.NoError(t, subserver.SaveConfig(layout, config))
	server, err := subserver.New(layout)
	require.NoError(t, err)
	s := &subServer{layout: layout, server: server, http: httptest.NewServer(server.Handler())}
	t.Cleanup(s.http.Close)
	return s
}

// deliver 把订阅包放进收件目录并让订阅端处理（相当于手工拷贝到订阅机）。
func (s *subServer) deliver(t *testing.T, bundle *app.PublishedBundle) {
	t.Helper()
	path := filepath.Join(s.layout.InboxDir(), bundle.Filename)
	require.NoError(t, os.WriteFile(path, bundle.Data, 0o640))
	past := time.Now().Add(-3 * time.Second)
	require.NoError(t, os.Chtimes(path, past, past))
	s.server.Refresh()
}

func sourceOf(t *testing.T, h *harness, primary bool) subserver.SourceConfig {
	t.Helper()
	status, err := h.svc.SubscriptionBundleStatus(context.Background())
	require.NoError(t, err)
	return subserver.SourceConfig{Name: status.Source, PublicKey: status.PublicKey, Primary: primary}
}

func publish(t *testing.T, h *harness) *app.PublishedBundle {
	t.Helper()
	bundle, err := h.svc.PublishSubscriptionBundle(context.Background(), h.actor)
	require.NoError(t, err)
	return bundle
}

// TestSubscriptionBundleServesAppliedNodes 验证管理端签发的订阅包在订阅端导入后提供订阅：
// 只包含已应用的节点，按 token 鉴权，手工节点、暂停和停用规则在下一次发布后生效。
func TestSubscriptionBundleServesAppliedNodes(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	subs := newSubServer(t, sourceOf(t, h, true))

	status, body, _ := get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusServiceUnavailable, status, body)

	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
	bundleStatus, err := h.svc.SubscriptionBundleStatus(ctx)
	require.NoError(t, err)
	require.True(t, bundleStatus.Pending)
	require.Zero(t, bundleStatus.Sequence)

	subs.deliver(t, publish(t, h))
	bundleStatus, err = h.svc.SubscriptionBundleStatus(ctx)
	require.NoError(t, err)
	require.False(t, bundleStatus.Pending)
	require.EqualValues(t, 1, bundleStatus.Sequence)

	status, body, headers := get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "name: usa1 vmess")
	require.Contains(t, body, "name: auto relay socks")
	require.Contains(t, headers.Get("Content-Disposition"), "alice.yaml")
	require.Empty(t, headers.Get("Subscription-Userinfo"), "the subscription server carries no traffic data")

	status, body, _ = get(t, subs.http, "/s/alice-token/surge")
	require.Equal(t, http.StatusOK, status)
	require.True(t, strings.HasPrefix(body, "#!MANAGED-CONFIG "+subs.http.URL+"/s/alice-token/surge"), body[:80])

	status, _, _ = get(t, subs.http, "/s/wrong-token/clash")
	require.Equal(t, http.StatusForbidden, status)

	// 手工节点和逐 stack 暂停在下一次发布后出现在订阅中。
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
	_, body, _ = get(t, subs.http, "/s/alice-token/clash")
	require.NotContains(t, body, "Manual Relay", "not published yet")
	subs.deliver(t, publish(t, h))
	_, body, _ = get(t, subs.http, "/s/alice-token/clash")
	require.Contains(t, body, "name: Manual Relay")
	require.NotContains(t, body, "usa1 vmess")
	require.Contains(t, body, "usa2 relay socks")

	_, err = h.svc.SetUserPause(ctx, h.actor, alice.ID, false, nil)
	require.NoError(t, err)
	subs.deliver(t, publish(t, h))
	status, _, _ = get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusNotFound, status, "disabled users get 404 by default")

	health := subs.server.Health()
	require.Equal(t, "ok", health.Status)
	require.EqualValues(t, 3, health.Source[0].Sequence)
}

// TestSubscriptionBundleRejections 验证订阅端拒收被篡改的包、未受信任来源的包和旧包，拒收后保留原有数据。
func TestSubscriptionBundleRejections(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
	subs := newSubServer(t, sourceOf(t, h, true))
	config, err := subserver.LoadConfig(subs.layout)
	require.NoError(t, err)

	first := publish(t, h)
	second := publish(t, h)
	_, err = subserver.Import(subs.layout, config, second.Data, false)
	require.NoError(t, err)
	_, err = subserver.Import(subs.layout, config, first.Data, false)
	require.ErrorIs(t, err, subserver.ErrStale)
	_, err = subserver.Import(subs.layout, config, first.Data, true)
	require.NoError(t, err, "force accepts an older bundle")

	tampered := strings.Replace(string(second.Data), `"sequence": 2`, `"sequence": 9`, 1)
	_, err = subserver.Import(subs.layout, config, []byte(tampered), false)
	require.Error(t, err)

	other := newRuntimeHarness(t)
	_, err = subserver.Import(subs.layout, config, publish(t, other).Data, false)
	require.Error(t, err, "same source name, different key")

	// 收件目录中被拒的文件移到 rejected，订阅照常。
	subs.deliver(t, &app.PublishedBundle{Filename: "bad.bundle", Data: []byte("not a bundle")})
	_, err = os.Stat(filepath.Join(subs.layout.RejectedDir(), "bad.bundle.error"))
	require.NoError(t, err)
	status, _, _ := get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status)
}

// TestSubscriptionMergesNodeSources 验证订阅端把其他来源的节点按用户名并入主来源的用户。
func TestSubscriptionMergesNodeSources(t *testing.T) {
	ctx := context.Background()
	primary := newRuntimeHarness(t)
	id, err := primary.svc.Apply(ctx, primary.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, primary.waitTask(t, id).Status)

	// 第二台管理端：只有一个 alice 的手工节点，来源名为 east。
	east := newRuntimeHarness(t)
	doc, err := east.svc.LoadConfig(ctx)
	require.NoError(t, err)
	for _, stack := range doc.Stacks {
		require.NoError(t, east.svc.DeleteStack(ctx, east.actor, stack.ID))
	}
	settings := *doc.Settings
	settings.Subscription.SourceName = "east"
	_, err = east.svc.UpdateSettings(ctx, east.actor, settings)
	require.NoError(t, err)
	_, err = east.svc.SaveManualNodes(ctx, east.actor, []configdoc.ManualNode{{
		User: "alice", Mode: configdoc.NodeStandard, Protocol: "socks5", Server: "east.example.com", Port: 1080,
		Remark: "East Relay", AuthType: "noauth",
	}})
	require.NoError(t, err)

	subs := newSubServer(t, sourceOf(t, primary, true), sourceOf(t, east, false))
	subs.deliver(t, publish(t, east))
	status, _, _ := get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusServiceUnavailable, status, "no primary bundle yet")

	subs.deliver(t, publish(t, primary))
	status, body, _ := get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "name: usa1 vmess")
	require.Contains(t, body, "name: East Relay")
}

// TestRemoteSourceSyncsExportedNodes 验证一个实例开启节点导出后，另一个实例通过远端节点源拉取并合并到订阅数据中。
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
	exportServer := httptest.NewServer(api.New(exporter.svc, api.Options{PanelPath: "panel"}))
	defer exportServer.Close()

	status, _, _ := get(t, exportServer, "/panel/export/nodes", "Authorization", "Bearer wrong")
	require.Equal(t, http.StatusForbidden, status)

	// 导入端：只有用户 alice，没有任何 stack。
	importer := newRuntimeHarness(t)
	imported, err := importer.svc.LoadConfig(ctx)
	require.NoError(t, err)
	for _, stack := range imported.Stacks {
		require.NoError(t, importer.svc.DeleteStack(ctx, importer.actor, stack.ID))
	}
	_, err = importer.svc.SaveRemoteSources(ctx, importer.actor, []configdoc.RemoteSource{{
		Name: "east", URL: exportServer.URL + "/panel/export/nodes", Token: "export-token-0123456789", Enabled: true,
	}})
	require.NoError(t, err)
	require.NoError(t, importer.svc.SyncRemoteSources(ctx, 0))

	subs := newSubServer(t, sourceOf(t, importer, true))
	subs.deliver(t, publish(t, importer))
	status, body, _ := get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "name: usa1 vmess")
}
