package configdoc_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/generator/sub"
)

func directUpstream(t *testing.T, name, config string) configdoc.Upstream {
	t.Helper()
	var raw configdoc.RawMapping
	require.NoError(t, yaml.Unmarshal([]byte(config), &raw))
	return configdoc.Upstream{Name: name, Type: "raw", Config: raw}
}

// TestStackDirectNodes 验证授权独立性、名称规则和原生参数保留，不受本机代理组选择影响。
func TestStackDirectNodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*configdoc.Document)
		want   []string
	}{
		{name: "single", want: []string{"usa1 vmess [DRT]"}},
		{name: "custom suffix preserves spaces", mutate: func(doc *configdoc.Document) {
			doc.Stacks[1].DirectSuffix = "  [直连]"
		}, want: []string{"usa1 vmess  [直连]"}},
		{name: "custom suffix without space", mutate: func(doc *configdoc.Document) {
			doc.Stacks[1].DirectSuffix = "[DIRECT]"
		}, want: []string{"usa1 vmess[DIRECT]"}},
		{name: "empty suffix uses default", mutate: func(doc *configdoc.Document) {
			doc.Stacks[1].DirectSuffix = ""
		}, want: []string{"usa1 vmess [DRT]"}},
		{name: "multiple", mutate: func(doc *configdoc.Document) {
			doc.Stacks[1].Mihomo.Upstreams = append(doc.Stacks[1].Mihomo.Upstreams,
				directUpstream(t, "server-b", "type: socks5\nserver: b.example.com\nport: 1080\nusername: remote-user\npassword: remote-password\nudp: true\ntls: true\nsni: b.example.com"),
				configdoc.Upstream{Name: "other-stack", Type: "xray-socks5", Stack: "usa2", Inbound: "relay"})
		}, want: []string{"usa1 vmess · server-a [DRT]", "usa1 vmess · server-b [DRT]"}},
		{name: "independent of inbound and processes", mutate: func(doc *configdoc.Document) {
			doc.Stacks[1].Xray.Inbounds[1].Users = nil
			doc.Stacks[1].Xray.Enabled = false
			doc.Stacks[1].Mihomo.Enabled = false
		}, want: []string{"usa1 vmess [DRT]"}},
		{name: "user template", mutate: func(doc *configdoc.Document) {
			doc.Users[0].DisplayTemplate = "{{ .stack | toUpper }} {{ .user }}"
		}, want: []string{"USA1 alice [DRT]"}},
		{name: "global template without user inbound", mutate: func(doc *configdoc.Document) {
			doc.Stacks[1].Xray.Inbounds[1].Users = nil
			doc.Stacks[1].Xray.Inbounds[1].DisplayTemplate = "unrelated inbound"
			doc.Settings.Subscription.DisplayTemplate = "{{ .stack | toUpper }} {{ .user }}"
		}, want: []string{"USA1 alice [DRT]"}},
		{name: "unrelated inbound template is ignored", mutate: func(doc *configdoc.Document) {
			doc.Stacks[1].Xray.Inbounds[1].Users = nil
			doc.Stacks[1].Xray.Inbounds[1].DisplayTemplate = "unrelated inbound"
		}, want: []string{"usa1 vmess [DRT]"}},
		{name: "profile and inbound override", mutate: func(doc *configdoc.Document) {
			doc.Users[0].Profiles = []configdoc.Profile{{Name: "mobile", UUID: doc.Users[0].UUID, SSPassword: "profile-password", Remark: "Profile"}}
			doc.Stacks[1].Xray.Inbounds[1].Users[0] = configdoc.UserRef{User: "alice", Profile: "mobile", DisplayTemplate: "{{ .remark }} {{ .profile }}"}
		}, want: []string{"Profile mobile [DRT]"}},
		{name: "no subscribed inbound", mutate: func(doc *configdoc.Document) { doc.Stacks[1].Xray.Inbounds = nil }, want: []string{"usa1 vmess [DRT]"}},
		{name: "no grant", mutate: func(doc *configdoc.Document) { doc.Stacks[1].DirectUsers = nil }},
		{name: "disabled stack", mutate: func(doc *configdoc.Document) { doc.Stacks[1].Enabled = false }},
		{name: "paused user", mutate: func(doc *configdoc.Document) { doc.Users[0].PausedStacks = []string{"usa1"} }},
		{name: "single provider direct node", mutate: func(doc *configdoc.Document) {
			doc.Stacks[1].Mihomo.Upstreams = nil
			doc.Stacks[1].Mihomo.Providers = []configdoc.Provider{{
				Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600,
				DirectNodes: []configdoc.ProviderDirectNode{{
					Name:   "HK-node",
					Config: directUpstream(t, "hk", "type: vmess\nserver: server-a.example.com\nport: 443\nuuid: 11111111-1111-4111-8111-111111111111\nalterId: 0\ncipher: auto\ntls: true\ndialer-proxy: other\ninterface-name: eth0\nrouting-mark: 100").Config,
				}},
			}}
		}, want: []string{"usa1 vmess [DRT]"}},
		{name: "mixed raw upstream and provider direct nodes with collision", mutate: func(doc *configdoc.Document) {
			cfgA := directUpstream(t, "a", "type: socks5\nserver: server-a.example.com\nport: 1080\nusername: remote-user\npassword: remote-password\nudp: true\ntls: true").Config
			cfgB := directUpstream(t, "b", "type: socks5\nserver: b.example.com\nport: 1080\nusername: remote-user\npassword: remote-password\nudp: true\ntls: true").Config
			doc.Stacks[1].Mihomo.Providers = []configdoc.Provider{
				{Name: "p1", URL: "https://example.com/1", FetchMode: "direct", Interval: 3600, DirectNodes: []configdoc.ProviderDirectNode{{Name: "HK", Config: cfgA}}},
				{Name: "p2", URL: "https://example.com/2", FetchMode: "direct", Interval: 3600, DirectNodes: []configdoc.ProviderDirectNode{{Name: "HK", Config: cfgB}}},
			}
		}, want: []string{"usa1 vmess · server-a [DRT]", "usa1 vmess · p1/HK [DRT]", "usa1 vmess · p2/HK [DRT]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := loadExampleDoc(t)
			doc.Stacks[1].Xray.Inbounds[0].Sub = false
			doc.Stacks[1].DirectUsers = []string{"alice"}
			doc.Stacks[1].Mihomo.Upstreams[0].Config.Set("dialer-proxy", "other-stack")
			doc.Stacks[1].Mihomo.Upstreams[0].Config.Set("interface-name", "eth0")
			doc.Stacks[1].Mihomo.Upstreams[0].Config.Set("routing-mark", "100")
			if tc.mutate != nil {
				tc.mutate(doc)
			}
			before, err := json.Marshal(doc)
			require.NoError(t, err)
			nodes, err := configdoc.StackDirectNodes(doc)
			require.NoError(t, err)
			require.Len(t, nodes, len(tc.want))
			for index, node := range nodes {
				require.Equal(t, tc.want[index], node.Remark)
				require.True(t, node.Direct)
				require.Equal(t, "alice", node.User)
				require.True(t, strings.Contains(node.ID, "usa1:direct:") || strings.Contains(node.ID, "usa1:direct-provider:"))
				proxy, err := yaml.Marshal(sub.RenderClashProxy(node))
				require.NoError(t, err)
				require.NotContains(t, string(proxy), "dialer-proxy")
				require.NotContains(t, string(proxy), "interface-name")
				require.NotContains(t, string(proxy), "routing-mark")
				require.Contains(t, string(proxy), "tls: true")
				if strings.Contains(node.ID, "usa1:direct:") {
					if index == 0 {
						require.Equal(t, "server-a.example.com", node.Server)
						require.Contains(t, sub.RenderDirectSurgeProxy(node), "tls=true")
					} else {
						require.Contains(t, sub.RenderDirectSurgeProxy(node), `username="remote-user", password="remote-password"`)
						require.Contains(t, sub.RenderDirectSurgeProxy(node), "socks5-tls")
					}
				}
			}
			after, err := json.Marshal(doc)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "generation must not mutate the upstream")
		})
	}
}

// TestDirectUserValidationAndLifecycle 验证引用校验、重名检查、改名删除和带引用导出。
func TestDirectUserValidationAndLifecycle(t *testing.T) {
	doc := loadExampleDoc(t)
	doc.Stacks[1].Xray.Inbounds[0].Sub = false
	doc.Stacks[1].DirectUsers = []string{"missing"}
	require.Contains(t, configdoc.Validate(doc).Error(), "direct_users[0]")
	doc.Stacks[1].DirectUsers = []string{"alice", "alice"}
	require.Contains(t, configdoc.Validate(doc).Error(), "duplicate direct user")
	doc.Stacks[1].DirectUsers = []string{"alice"}
	require.Empty(t, configdoc.Validate(doc))
	doc.ManualNodes = []configdoc.ManualNode{{User: "alice", Mode: configdoc.NodeStandard, Protocol: "socks5", Server: "manual.example.com", Port: 1080, AuthType: "noauth", Remark: "usa1 vmess [DRT]"}}
	require.Contains(t, configdoc.Validate(doc).Error(), "already has a proxy named usa1 vmess [DRT]")
	doc.ManualNodes = nil
	doc.Stacks[1].Xray.Inbounds[1].Users = nil
	doc.Stacks[1].Xray.Inbounds[0].Users = nil
	exported, err := configdoc.Subset(doc, configdoc.ExportScope{Scope: configdoc.ScopeStacks, Stacks: []string{"usa1"}, IncludeReferencedUsers: true}, time.Now())
	require.NoError(t, err)
	require.Len(t, exported.Users, 1)
	require.Equal(t, "alice", exported.Users[0].Name)
	configdoc.RenameUser(doc, "alice", "bob")
	require.Equal(t, []string{"bob"}, doc.Stacks[1].DirectUsers)
	configdoc.RemoveUserRefs(doc, "bob")
	require.Empty(t, doc.Stacks[1].DirectUsers)
}

// TestProviderDirectNodeValidationAndMasking 验证订阅直连节点的校验与凭据遮蔽恢复。
func TestProviderDirectNodeValidationAndMasking(t *testing.T) {
	doc := loadExampleDoc(t)
	rawCfg := directUpstream(t, "hk", "type: socks5\nserver: hk.example.com\nport: 1080\nusername: u\npassword: p").Config
	doc.Stacks[1].Mihomo.Providers = []configdoc.Provider{{
		ID: 7, Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600,
		DirectNodes: []configdoc.ProviderDirectNode{{Name: "HK-1", Config: rawCfg}},
	}}
	require.Empty(t, configdoc.Validate(doc))

	masked := doc.Stacks[1].Clone()
	configdoc.MaskStack(&masked)
	require.True(t, masked.Mihomo.Providers[0].DirectNodes[0].Config.IsZero())

	configdoc.UnmaskStack(&masked, &doc.Stacks[1])
	require.False(t, masked.Mihomo.Providers[0].DirectNodes[0].Config.IsZero())
	server, ok := masked.Mihomo.Providers[0].DirectNodes[0].Config.Get("server")
	require.True(t, ok)
	require.Equal(t, "hk.example.com", server)

	doc.Stacks[1].Mihomo.Providers[0].DirectNodes = []configdoc.ProviderDirectNode{
		{Name: "HK-1", Config: rawCfg},
		{Name: "HK-1", Config: rawCfg},
	}
	require.Contains(t, configdoc.Validate(doc).Error(), "duplicate direct node")

	doc.Stacks[1].Mihomo.Providers[0].DirectNodes = []configdoc.ProviderDirectNode{{Name: "HK-1"}}
	require.Contains(t, configdoc.Validate(doc).Error(), "config is required")
}

// TestProviderDirectNodeSecretsDiffCloneAndHysteria 验证加密导出/解密、修订差异遮蔽、克隆以及 Hysteria 标量 auth / 字符串端口兼容。
func TestProviderDirectNodeSecretsDiffCloneAndHysteria(t *testing.T) {
	doc := loadExampleDoc(t)
	doc.Stacks[1].DirectUsers = []string{"alice"}
	hysteriaCfg := directUpstream(t, "hy", "type: hysteria\nserver: hy.example.com\nport: \"443\"\nauth: hy-secret-pass\nup: 10\ndown: 100").Config
	require.NoError(t, configdoc.CheckDirectRawConfig(hysteriaCfg))
	doc.Stacks[1].Mihomo.Providers = []configdoc.Provider{{
		ID: 7, Name: "airport", URL: "https://example.com/sub?token=sub-secret", FetchMode: "direct", Interval: 3600,
		DirectNodes: []configdoc.ProviderDirectNode{{Name: "HY-1", Config: hysteriaCfg}},
	}}
	// Direct node generation + YAML/JSON roundtrip preserves Hysteria scalar auth and numeric port.
	nodes, err := configdoc.StackDirectNodes(doc)
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	hyNode := nodes[1]
	rawJSON, err := json.Marshal(hyNode)
	require.NoError(t, err)
	var decodedJSON sub.Node
	require.NoError(t, json.Unmarshal(rawJSON, &decodedJSON))
	require.Equal(t, 443, decodedJSON.Port)
	clashYAML, err := yaml.Marshal(sub.RenderClashProxy(decodedJSON))
	require.NoError(t, err)
	require.Contains(t, string(clashYAML), "hy-secret-pass")

	// Diff masks secrets inside Provider.DirectNodes[].Config.
	before := doc.Clone()
	before.Stacks[1].Mihomo.Providers[0].DirectNodes[0].Config.Set("auth", "old-hy-secret")
	entries := configdoc.Diff(before, doc)
	diffJSON, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NotContains(t, string(diffJSON), "hy-secret-pass")
	require.NotContains(t, string(diffJSON), "old-hy-secret")

	// SecretsEncrypted encrypts DirectNodes[].Config and Decrypt restores it cleanly.
	exported, err := configdoc.Subset(doc, configdoc.ExportScope{Scope: configdoc.ScopeStacks, Stacks: []string{"usa1"}, IncludeReferencedUsers: true}, time.Now())
	require.NoError(t, err)
	require.NoError(t, configdoc.ApplySecrets(exported, configdoc.SecretsEncrypted, "strong-passphrase"))
	encJSON, err := json.Marshal(exported)
	require.NoError(t, err)
	require.NotContains(t, string(encJSON), "hy-secret-pass")
	require.NotEmpty(t, exported.Stacks[0].Mihomo.Providers[0].DirectNodes[0].EncryptedConfig)
	require.True(t, exported.Stacks[0].Mihomo.Providers[0].DirectNodes[0].Config.IsZero())
	require.NoError(t, configdoc.Decrypt(exported, "strong-passphrase"))
	authVal, ok := exported.Stacks[0].Mihomo.Providers[0].DirectNodes[0].Config.Get("auth")
	require.True(t, ok)
	require.Equal(t, "hy-secret-pass", authVal)

	// CloneStack clears IDs while keeping DirectNodes.
	cloned := configdoc.CloneStack(&doc.Stacks[1], "usa3")
	require.Zero(t, cloned.Mihomo.Providers[0].ID)
	require.Len(t, cloned.Mihomo.Providers[0].DirectNodes, 1)
}
