package api_test

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
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

// providerFilterChecker models the external core boundary without requiring a binary for API tests.
type providerFilterChecker struct{}

// Check rejects the invalid expression used by the API validation case.
func (providerFilterChecker) Check(_ context.Context, _, _ string, content []byte) error {
	var config struct {
		Providers map[string]struct {
			Filter string `yaml:"filter"`
		} `yaml:"proxy-providers"`
	}
	if err := yaml.Unmarshal(content, &config); err != nil {
		return err
	}
	if config.Providers["validation"].Filter == "[" {
		return fmt.Errorf("invalid native expression")
	}
	return nil
}

// TestProviderNodeDetailAPI verifies masking, explicit audited disclosure and request authorization.
func TestProviderNodeDetailAPI(t *testing.T) {
	h := newAdminHarness(t)
	ctx := context.Background()
	_, err := h.svc.CreateUser(ctx, app.Actor{Name: "admin"}, configdoc.User{Name: "alice", Enabled: true})
	require.NoError(t, err)
	stack, err := h.svc.CreateStack(ctx, app.Actor{Name: "admin"}, configdoc.NewStackOptions{Template: "pair", Name: "nodes"})
	require.NoError(t, err)
	stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600}}
	stack, err = h.svc.UpdateStack(ctx, app.Actor{Name: "admin"}, stack.ID, *stack)
	require.NoError(t, err)
	source := stack.Mihomo.Providers[0]
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"providers":{"%s":{"proxies":[{"name":"%s | node","type":"Trojan"}]}}}`, source.RuntimeName(), source.RuntimeName())
	}))
	defer controller.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(controller.URL, "http://"))
	require.NoError(t, err)
	controllerPort, err := strconv.Atoi(port)
	require.NoError(t, err)
	config := map[string]any{"external-controller": fmt.Sprintf("127.0.0.1:%d", controllerPort), "proxy-providers": map[string]any{source.RuntimeName(): map[string]any{"path": source.CachePath()}}}
	content, err := yaml.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.svc.Layout.GeneratedMihomo(stack.Name)), 0o750))
	require.NoError(t, os.WriteFile(h.svc.Layout.GeneratedMihomo(stack.Name), content, 0o600))
	cache := filepath.Join(h.svc.Layout.MihomoWorkDir(stack.Name), source.CachePath())
	require.NoError(t, os.MkdirAll(filepath.Dir(cache), 0o750))
	require.NoError(t, os.WriteFile(cache, []byte("proxies:\n  - {name: node, type: trojan, server: node.example.com, port: 443, password: private-node-password, tls: true}\n"), 0o600))
	require.NoError(t, h.svc.Units.Start(ctx, units.Name(units.Mihomo, stack.Name)))
	path := fmt.Sprintf("/api/stacks/%d/providers/%d/nodes/detail", stack.ID, source.ID)
	masked := h.mustDo(http.MethodPost, path, map[string]any{"name": "node", "reveal": false}, http.StatusOK)
	require.True(t, masked["masked"].(bool))
	require.NotContains(t, masked["config"], "private-node-password")
	response, plain := h.do(http.MethodPost, path, map[string]any{"name": "node", "reveal": true})
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	require.Contains(t, plain["config"], "private-node-password")
	audits, err := h.svc.ListAudit(ctx, app.ActionConfigSecretView, "admin", 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), audits.Total)
	h.csrf = ""
	response, _ = h.do(http.MethodPost, path, map[string]any{"name": "node", "reveal": true})
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	h.client = newClient(t)
	response, _ = h.do(http.MethodPost, path, map[string]any{"name": "node", "reveal": false})
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
}

// TestProviderAPI verifies authenticated CRUD, URL masking and update access controls.
func TestProviderAPI(t *testing.T) {
	h := newAdminHarness(t)
	h.svc.Checker = providerFilterChecker{}
	_, err := h.svc.CreateUser(context.Background(), app.Actor{Name: "admin"}, configdoc.User{Name: "alice", Enabled: true})
	require.NoError(t, err)
	created := h.mustDo(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": "edge1", "labels": []string{}, "members": []string{}}, http.StatusCreated)
	id := int64(created["id"].(float64))
	path := fmt.Sprintf("/api/stacks/%d", id)
	stack := h.mustDo(http.MethodGet, path, nil, http.StatusOK)
	stack["mihomo"].(map[string]any)["providers"] = []map[string]any{{"name": "airport", "url": "https://example.com/sub?token=secret", "fetch_mode": "direct", "auto_update": false, "interval": 3600, "filter": "(?i)HK", "exclude_filter": "expired"}}
	response := h.mustDo(http.MethodPut, path, stack, http.StatusOK)
	provider := response["mihomo"].(map[string]any)["providers"].([]any)[0].(map[string]any)
	require.Equal(t, configdoc.MaskedValue, provider["url"])
	providerID := int64(provider["id"].(float64))
	provider["name"] = "renamed"
	h.mustDo(http.MethodPut, path, response, http.StatusOK)
	doc, err := h.svc.LoadConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, "https://example.com/sub?token=secret", doc.Stacks[0].Mihomo.Providers[0].URL)
	provider["filter"] = "["
	resp, invalid := h.do(http.MethodPut, path, response)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Contains(t, errorLocations(invalid)[0], "mihomo.providers[0].filter")
	updatePath := fmt.Sprintf("%s/providers/%d/update", path, providerID)
	csrf := h.csrf
	h.csrf = ""
	resp, _ = h.do(http.MethodPost, updatePath, nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	h.csrf = csrf
	resp, _ = h.do(http.MethodPost, updatePath, nil)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "a stopped stack cannot refresh")
	h.client = newClient(t)
	resp, _ = h.do(http.MethodGet, path+"/providers", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
