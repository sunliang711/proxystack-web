package configdoc_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/generator/mihomo"
)

// TestProviderGenerationAndValidation covers native references and both download proxy kinds.
func TestProviderGenerationAndValidation(t *testing.T) {
	for _, mode := range []string{"direct", "custom-http", "custom-socks5", "stack"} {
		t.Run(mode, func(t *testing.T) {
			doc := loadExampleDoc(t)
			stack := &doc.Stacks[1]
			source := configdoc.Provider{ID: 17, Name: "airport", URL: "https://example.com/sub?token=secret", FetchMode: "direct", Interval: 3600, Filter: "(?i)HK|香港", ExcludeFilter: "到期"}
			if mode != "direct" {
				source.FetchMode = "system"
				proxy := &doc.Settings.DownloadProxy
				*proxy = configdoc.DownloadProxySettings{Mode: "custom", Protocol: "http", Host: "127.0.0.1", Port: 1080, Username: "user", Password: "secret", RemoteDNS: true}
				if mode == "custom-socks5" {
					proxy.Protocol = "socks5"
				} else if mode == "stack" {
					*proxy = configdoc.DownloadProxySettings{Mode: "stack", Stack: "usa2", Listener: "socks"}
				}
			}
			stack.Mihomo.Providers = []configdoc.Provider{source}
			stack.Mihomo.Groups[0].Proxies = nil
			stack.Mihomo.Groups[0].Use = []string{source.Name}
			set := compileResolved(t, doc)
			text, err := mihomo.DumpsConfig(set, stack.Name)
			require.NoError(t, err)
			var native map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(text), &native))
			provider := native["proxy-providers"].(map[string]any)[source.RuntimeName()].(map[string]any)
			require.Equal(t, 0, provider["interval"])
			require.Equal(t, source.CachePath(), provider["path"])
			require.Equal(t, source.Filter, provider["filter"])
			if mode == "direct" {
				require.Equal(t, "DIRECT", provider["proxy"])
				require.NotContains(t, text, configdoc.ProviderDownloadProxy)
			} else {
				require.Equal(t, configdoc.ProviderDownloadProxy, provider["proxy"])
				require.Contains(t, text, "name: "+configdoc.ProviderDownloadProxy)
			}
			require.Equal(t, []any{source.RuntimeName()}, native["proxy-groups"].([]any)[0].(map[string]any)["use"])
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*configdoc.Document)
	}{
		{"oversized regex", func(doc *configdoc.Document) { doc.Stacks[1].Mihomo.Providers[0].Filter = strings.Repeat("x", 4097) }},
		{"invalid URL", func(doc *configdoc.Document) { doc.Stacks[1].Mihomo.Providers[0].URL = "file:///tmp/token" }},
		{"missing reference", func(doc *configdoc.Document) { doc.Stacks[1].Mihomo.Groups[0].Use = []string{"missing"} }},
		{"self dependency", func(doc *configdoc.Document) {
			doc.Settings.DownloadProxy = configdoc.DownloadProxySettings{Mode: "stack", Stack: "usa1", Listener: "socks"}
			doc.Stacks[1].Mihomo.Providers[0].FetchMode = "system"
		}},
		{"dependency cycle", func(doc *configdoc.Document) {
			doc.Settings.DownloadProxy = configdoc.DownloadProxySettings{Mode: "stack", Stack: "auto", Listener: "socks"}
			doc.Stacks[1].Mihomo.Providers[0].FetchMode = "system"
		}},
		{"unsupported local DNS", func(doc *configdoc.Document) {
			doc.Settings.DownloadProxy = configdoc.DownloadProxySettings{Mode: "custom", Protocol: "socks5", Host: "localhost", Port: 1080}
			doc.Stacks[1].Mihomo.Providers[0].FetchMode = "system"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := loadExampleDoc(t)
			doc.Stacks[1].Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600}}
			test.mutate(doc)
			require.NotEmpty(t, configdoc.Validate(doc))
		})
	}
}

// TestProviderIdentityAndSecrets protects stable cache names, API masking and encrypted export.
func TestProviderIdentityAndSecrets(t *testing.T) {
	doc := loadExampleDoc(t)
	p := configdoc.Provider{ID: 1, Name: "old", URL: "https://example.com/sub?token=secret", FetchMode: "direct", Interval: 3600}
	doc.Stacks[1].Mihomo.Providers = []configdoc.Provider{p}
	renamed := p
	renamed.Name = "new"
	require.Equal(t, p.RuntimeName(), renamed.RuntimeName())
	require.Equal(t, p.CachePath(), renamed.CachePath())
	renamed.URL += "2"
	require.NotEqual(t, p.CachePath(), renamed.CachePath())
	masked := doc.Clone()
	configdoc.MaskStack(&masked.Stacks[1])
	require.Equal(t, configdoc.MaskedValue, masked.Stacks[1].Mihomo.Providers[0].URL)
	configdoc.UnmaskStack(&masked.Stacks[1], &doc.Stacks[1])
	require.Equal(t, p.URL, masked.Stacks[1].Mihomo.Providers[0].URL)
	subset, err := configdoc.Subset(doc, configdoc.ExportScope{Scope: configdoc.ScopeAll}, time.Now())
	require.NoError(t, err)
	require.Zero(t, subset.Stacks[1].Mihomo.Providers[0].ID)
	require.NoError(t, configdoc.ApplySecrets(subset, configdoc.SecretsEncrypted, "test-passphrase"))
	require.True(t, strings.HasPrefix(subset.Stacks[1].Mihomo.Providers[0].URL, "enc:"))
}

// TestProviderStopsRelayOnlyClassification keeps mixed subscription egress in independent traffic totals.
func TestProviderStopsRelayOnlyClassification(t *testing.T) {
	doc := loadExampleDoc(t)
	stack := &doc.Stacks[0]
	require.True(t, configdoc.RelaysOnly(stack))
	stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport"}}
	require.False(t, configdoc.RelaysOnly(stack))
}

// TestProviderDependencyErrorPath locates a disabled download dependency in the provider form.
func TestProviderDependencyErrorPath(t *testing.T) {
	doc := loadExampleDoc(t)
	stack := &doc.Stacks[1]
	stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "system", Interval: 3600}}
	doc.Settings.DownloadProxy = configdoc.DownloadProxySettings{Mode: "stack", Stack: "usa2", Listener: "socks"}
	doc.Stacks[2].Enabled = false
	issues := configdoc.Validate(doc)
	require.NotEmpty(t, issues)
	found := false
	for _, issue := range issues {
		found = found || issue.Path == "stacks[1].mihomo.providers[0]"
	}
	require.True(t, found, issues)
}

// TestProviderOmittedExportRetainsURL verifies external URLs survive omitted export and overwrite/replace import.
func TestProviderOmittedExportRetainsURL(t *testing.T) {
	for _, mode := range []string{configdoc.ImportMerge, configdoc.ImportReplace} {
		t.Run(mode, func(t *testing.T) {
			doc := loadExampleDoc(t)
			url := "https://example.com/sub?token=external-secret"
			doc.Stacks[1].Mihomo.Providers = []configdoc.Provider{{ID: 9, Name: "airport", URL: url, FetchMode: "direct", Interval: 3600}}
			subset, err := configdoc.Subset(doc, configdoc.ExportScope{Scope: configdoc.ScopeAll}, time.Now())
			require.NoError(t, err)
			require.NoError(t, configdoc.ApplySecrets(subset, configdoc.SecretsOmitted, ""))
			require.Equal(t, url, subset.Stacks[1].Mihomo.Providers[0].URL)
			require.Empty(t, subset.Stacks[1].Mihomo.ControllerSecret)
			data, err := configdoc.Marshal(subset, configdoc.FormatYAML)
			require.NoError(t, err)
			incoming, err := configdoc.Parse(data)
			require.NoError(t, err)
			plan, err := configdoc.PlanImport(doc, incoming, configdoc.ImportOptions{Mode: mode, Conflict: configdoc.ConflictOverwrite, Ports: configdoc.PortsKeep})
			require.NoError(t, err)
			require.Empty(t, configdoc.Validate(plan.Result))
			require.Equal(t, url, plan.Result.Stacks[plan.Result.StackByName("usa1")].Mihomo.Providers[0].URL)
		})
	}
}
