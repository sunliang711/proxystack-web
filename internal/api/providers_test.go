package api_test

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
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
