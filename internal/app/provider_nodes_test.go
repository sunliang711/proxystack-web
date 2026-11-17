package app_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/units"
)

// TestProviderNodeDetails verifies applied-cache selection, secret disclosure and filtered membership.
func TestProviderNodeDetails(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	name := ""
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{name: map[string]any{"proxies": []map[string]string{{"name": name + " | HK-node", "type": "Vmess"}}}}})
	}))
	defer controller.Close()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stack := doc.Stacks[1]
	_, port, err := net.SplitHostPort(strings.TrimPrefix(controller.URL, "http://"))
	require.NoError(t, err)
	stack.Mihomo.ControllerPort, err = strconv.Atoi(port)
	require.NoError(t, err)
	stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600}}
	saved, err := h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	source := saved.Mihomo.Providers[0]
	name = source.RuntimeName()
	task, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, task).Status)
	path := filepath.Join(h.svc.Layout.MihomoWorkDir(stack.Name), source.CachePath())
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(`proxies:
  - name: HK-node
    type: vmess
    server: node.example.com
    port: 443
    uuid: private-uuid
    tls: true
    network: ws
    servername: sni.example.com
    ws-opts:
      path: /proxy
      headers: {Authorization: bearer-private}
    plugin-opts: {obfs-password: private-obfs}
    peers: [{private-key: private-key-value}]
    tlsmirror-opts: {primary-key: private-primary}
    kcp-opts: {seed: private-seed}
  - {name: JP-excluded, type: socks5, server: excluded.example.com, port: 1080}
`), 0o600))
	// Saving a new URL without applying it must not select another cache.
	saved.Mihomo.Providers[0].URL = "https://example.com/pending-subscription"
	_, err = h.svc.UpdateStack(ctx, h.actor, saved.ID, *saved)
	require.NoError(t, err)
	nodes, err := h.svc.ListProviderNodes(ctx, stack.ID, source.ID)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.Equal(t, "HK-node", nodes[0].Key)
	require.Equal(t, "node.example.com", nodes[0].Server)
	require.Equal(t, "443", nodes[0].Port)
	details, err := h.svc.GetProviderNodeDetails(ctx, h.actor, stack.ID, source.ID, nodes[0].Key, false)
	require.NoError(t, err)
	require.True(t, details.Masked)
	for _, secret := range []string{"private-uuid", "bearer-private", "private-obfs", "private-key-value", "private-primary", "private-seed"} {
		require.NotContains(t, details.Config, secret)
	}
	require.Contains(t, details.Config, "tls: true")
	require.Contains(t, details.Config, "network: ws")
	plain, err := h.svc.GetProviderNodeDetails(ctx, h.actor, stack.ID, source.ID, nodes[0].Key, true)
	require.NoError(t, err)
	require.False(t, plain.Masked)
	require.Contains(t, plain.Config, "private-uuid")
	require.Contains(t, plain.Config, "private-primary")
	require.Contains(t, plain.Config, "private-seed")
	audits, err := h.svc.ListAudit(ctx, app.ActionConfigSecretView, h.actor.Name, 10, 0)
	require.NoError(t, err)
	require.Len(t, audits.Items, 1)
	_, err = h.svc.GetProviderNodeDetails(ctx, h.actor, stack.ID, source.ID, "JP-excluded", false)
	require.Error(t, err)
	_, err = h.svc.ListProviderNodes(ctx, doc.Stacks[2].ID, source.ID)
	require.ErrorIs(t, err, app.ErrNotFound)
	require.NoError(t, h.units.Stop(ctx, units.Name(units.Mihomo, stack.Name)))
	_, err = h.svc.ListProviderNodes(ctx, stack.ID, source.ID)
	require.Error(t, err)
}
