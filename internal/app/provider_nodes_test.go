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

// TestProviderDirectNodeSelectionAndSync verifies resolving, subscription delivery, config refresh, and sync-deletion on rename/remove.
func TestProviderDirectNodeSelectionAndSync(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	name := ""
	activeProxies := []string{"HK-node", "SG-node"}
	cacheYAML := `proxies:
  - name: HK-node
    type: vmess
    server: hk1.example.com
    port: 443
    uuid: 33333333-3333-4333-8333-333333333333
    tls: true
  - name: SG-node
    type: socks5
    server: sg1.example.com
    port: 1080
`
	var cachePath string
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			if cachePath != "" {
				require.NoError(t, os.WriteFile(cachePath, []byte(cacheYAML), 0o600))
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		proxies := make([]map[string]string, 0, len(activeProxies))
		for _, key := range activeProxies {
			proxies = append(proxies, map[string]string{"name": name + " | " + key, "type": "Vmess"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{name: map[string]any{"proxies": proxies}}})
	}))
	defer controller.Close()

	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stack := doc.Stacks[1]
	_, port, err := net.SplitHostPort(strings.TrimPrefix(controller.URL, "http://"))
	require.NoError(t, err)
	stack.Mihomo.ControllerPort, err = strconv.Atoi(port)
	require.NoError(t, err)
	stack.DirectUsers = []string{doc.Users[0].Name}
	stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600}}
	saved, err := h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	source := saved.Mihomo.Providers[0]
	name = source.RuntimeName()
	task, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, task).Status)

	cachePath = filepath.Join(h.svc.Layout.MihomoWorkDir(stack.Name), source.CachePath())
	require.NoError(t, os.MkdirAll(filepath.Dir(cachePath), 0o750))
	require.NoError(t, os.WriteFile(cachePath, []byte(cacheYAML), 0o600))

	// Selecting an unavailable node is rejected.
	bad := saved.Clone()
	bad.Mihomo.Providers[0].DirectNodes = []configdoc.ProviderDirectNode{{Name: "Missing-node"}}
	_, err = h.svc.UpdateStack(ctx, h.actor, bad.ID, bad)
	require.Error(t, err)

	// Selecting direct nodes on an unsaved provider (ID == 0) is rejected with FieldError.
	unsaved := saved.Clone()
	unsaved.Mihomo.Providers = append(unsaved.Mihomo.Providers, configdoc.Provider{
		Name: "new-sub", URL: "https://example.com/new", FetchMode: "direct", Interval: 3600,
		DirectNodes: []configdoc.ProviderDirectNode{{Name: "HK-node"}},
	})
	_, err = h.svc.UpdateStack(ctx, h.actor, unsaved.ID, unsaved)
	var fieldErr *app.FieldError
	require.ErrorAs(t, err, &fieldErr)
	require.Contains(t, fieldErr.Field, "direct_nodes")

	// Selecting an active node resolves its config snapshot and delivers it in subscription preview.
	saved.Mihomo.Providers[0].DirectNodes = []configdoc.ProviderDirectNode{{Name: "HK-node"}}
	saved, err = h.svc.UpdateStack(ctx, h.actor, saved.ID, *saved)
	require.NoError(t, err)
	require.False(t, saved.Mihomo.Providers[0].DirectNodes[0].Config.IsZero())
	subText, err := h.svc.PreviewSubscription(ctx, doc.Users[0].ID, "clash", nil)
	require.NoError(t, err)
	require.Contains(t, subText, "hk1.example.com")
	require.Contains(t, subText, "33333333-3333-4333-8333-333333333333")

	// Saving again with masked/empty Config preserves the existing snapshot without needing cache changes.
	masked := saved.Clone()
	configdoc.MaskStack(&masked)
	saved, err = h.svc.UpdateStack(ctx, h.actor, masked.ID, masked)
	require.NoError(t, err)
	require.False(t, saved.Mihomo.Providers[0].DirectNodes[0].Config.IsZero())

	// Modifying provider URL/filter without applying prevents selecting new direct nodes and skips stale sync.
	pendingEdit := saved.Clone()
	pendingEdit.Mihomo.Providers[0].URL = "https://example.com/sub-v2"
	pendingEdit.Mihomo.Providers[0].DirectNodes = append(pendingEdit.Mihomo.Providers[0].DirectNodes, configdoc.ProviderDirectNode{Name: "SG-node"})
	_, err = h.svc.UpdateStack(ctx, h.actor, pendingEdit.ID, pendingEdit)
	require.ErrorAs(t, err, &fieldErr)
	require.Contains(t, fieldErr.Message, "apply")

	pendingURLOnly := saved.Clone()
	pendingURLOnly.Mihomo.Providers[0].URL = "https://example.com/sub-v2"
	_, err = h.svc.UpdateStack(ctx, h.actor, pendingURLOnly.ID, pendingURLOnly)
	require.NoError(t, err)
	require.NoError(t, h.svc.UpdateStackProvider(ctx, h.actor, stack.ID, source.ID))
	reloadedPending, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	require.Len(t, reloadedPending.Stacks[reloadedPending.StackByID(stack.ID)].Mihomo.Providers[0].DirectNodes, 1)

	// Restore URL so applied == saved before testing normal sync.
	saved, err = h.svc.UpdateStack(ctx, h.actor, saved.ID, *saved)
	require.NoError(t, err)

	// Upstream returning 0 proxies preserves existing DirectNodes rather than wiping them.
	savedProxies := activeProxies
	activeProxies = nil
	cacheYAML = "proxies: []\n"
	require.NoError(t, h.svc.UpdateStackProvider(ctx, h.actor, stack.ID, source.ID))
	reloadedEmpty, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	require.Len(t, reloadedEmpty.Stacks[reloadedEmpty.StackByID(stack.ID)].Mihomo.Providers[0].DirectNodes, 1)
	activeProxies = savedProxies

	// Upstream refresh updates the node's config snapshot when credentials/server change.
	cacheYAML = `proxies:
  - name: HK-node
    type: vmess
    server: hk2.example.com
    port: 8443
    uuid: 44444444-4444-4444-8444-444444444444
    tls: true
  - name: SG-node
    type: socks5
    server: sg1.example.com
    port: 1080
`
	require.NoError(t, h.svc.UpdateStackProvider(ctx, h.actor, stack.ID, source.ID))
	subText, err = h.svc.PreviewSubscription(ctx, doc.Users[0].ID, "clash", nil)
	require.NoError(t, err)
	require.Contains(t, subText, "hk2.example.com")
	require.Contains(t, subText, "44444444-4444-4444-8444-444444444444")
	require.NotContains(t, subText, "33333333-3333-4333-8333-333333333333")

	// Upstream rename/removal synchronously removes the deleted node from DirectNodes.
	activeProxies = []string{"HK-renamed", "SG-node"}
	cacheYAML = `proxies:
  - name: HK-renamed
    type: vmess
    server: hk2.example.com
    port: 8443
    uuid: 44444444-4444-4444-8444-444444444444
    tls: true
  - name: SG-node
    type: socks5
    server: sg1.example.com
    port: 1080
`
	require.NoError(t, h.svc.UpdateStackProvider(ctx, h.actor, stack.ID, source.ID))
	reloaded, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	require.Empty(t, reloaded.Stacks[reloaded.StackByID(stack.ID)].Mihomo.Providers[0].DirectNodes)
	subText, err = h.svc.PreviewSubscription(ctx, doc.Users[0].ID, "clash", nil)
	require.NoError(t, err)
	require.NotContains(t, subText, "44444444-4444-4444-8444-444444444444")
}
