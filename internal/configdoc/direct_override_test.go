package configdoc_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/generator/sub"
)

func rawMapping(t *testing.T, text string) configdoc.RawMapping {
	t.Helper()
	var raw configdoc.RawMapping
	require.NoError(t, yaml.Unmarshal([]byte(text), &raw))
	return raw
}

// directOverrideDoc 准备 usa1 上 alice 和 bob 两个直连用户，只保留外部上游 server-a。
func directOverrideDoc(t *testing.T) *configdoc.Document {
	t.Helper()
	doc := loadExampleDoc(t)
	bob := doc.Users[0]
	bob.Name, bob.UUID, bob.SubToken = "bob", "22222222-2222-4222-8222-222222222222", "bob-token"
	doc.Users = append(doc.Users, bob)
	doc.Stacks[1].Xray.Inbounds[0].Sub = false
	doc.Stacks[1].DirectUsers = []string{"alice", "bob"}
	doc.Stacks[1].Mihomo.Upstreams[0].Config.Set("dialer-proxy", "other-stack")
	require.Empty(t, configdoc.Validate(doc))
	return doc
}

func directNodesByUser(t *testing.T, doc *configdoc.Document) map[string]sub.Node {
	t.Helper()
	nodes, err := configdoc.StackDirectNodes(doc)
	require.NoError(t, err)
	byUser := map[string]sub.Node{}
	for _, node := range nodes {
		byUser[node.User] = node
	}
	return byUser
}

// TestDirectOverrideMerge 验证覆盖只作用于对应用户、顶层字段整体替换、服务器字段仍被删除且不改写原配置。
func TestDirectOverrideMerge(t *testing.T) {
	doc := directOverrideDoc(t)
	doc.Stacks[1].DirectOverrides = []configdoc.DirectOverride{{
		User: "alice", Upstream: "server-a",
		Config: rawMapping(t, "uuid: cccccccc-cccc-4ccc-8ccc-cccccccccccc\nws_opts:\n  headers:\n    Host: cdn.example.com\nservername: sni.example.com"),
	}}
	require.Empty(t, configdoc.Validate(doc))
	before, err := json.Marshal(doc)
	require.NoError(t, err)

	nodes := directNodesByUser(t, doc)
	require.Len(t, nodes, 2)
	alice, bob := nodes["alice"], nodes["bob"]
	require.Equal(t, "cccccccc-cccc-4ccc-8ccc-cccccccccccc", alice.UUID)
	require.Equal(t, "server-a.example.com", alice.Server)
	require.Equal(t, 443, alice.Port)
	require.NotNil(t, alice.WSOpts)
	require.Empty(t, alice.WSOpts.Path, "nested mappings are replaced as a whole")
	require.Equal(t, "cdn.example.com", alice.WSOpts.Headers["Host"])
	require.Equal(t, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", bob.UUID)
	require.Equal(t, "/vmess", bob.WSOpts.Path)
	for _, node := range nodes {
		proxy, err := yaml.Marshal(sub.RenderClashProxy(node))
		require.NoError(t, err)
		require.NotContains(t, string(proxy), "dialer-proxy")
	}
	aliceProxy, err := yaml.Marshal(sub.RenderClashProxy(alice))
	require.NoError(t, err)
	require.Contains(t, string(aliceProxy), "sni.example.com")

	after, err := json.Marshal(doc)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "generation must not mutate the upstream or the override")

	// 即使覆盖绕过校验带入服务器专用字段，下发配置中也不保留。
	stack := doc.Stacks[1]
	stack.DirectOverrides[0].Config.Set("interface-name", "eth0")
	config := configdoc.DirectNodeConfig(stack, "alice", stack.Mihomo.Upstreams[0])
	_, found := config.Get("interface-name")
	require.False(t, found)
	_, found = config.Get("dialer-proxy")
	require.False(t, found)
	value, _ := config.Get("uuid")
	require.Equal(t, "cccccccc-cccc-4ccc-8ccc-cccccccccccc", value)
	keys := []string{}
	for index := 0; index < len(config.Node.Content); index += 2 {
		keys = append(keys, config.Node.Content[index].Value)
	}
	require.Equal(t, []string{"type", "server", "port", "uuid", "network", "tls", "ws_opts", "servername"}, keys,
		"replaced fields keep their position and new fields are appended")
}

// TestDirectOverrideFieldAliases 验证连字符和下划线两种写法按同一字段替换，输出中只保留覆盖的写法。
func TestDirectOverrideFieldAliases(t *testing.T) {
	for _, tc := range []struct{ name, override, key string }{
		{name: "dash overrides underscore", override: "ws-opts:\n  path: /alice", key: "ws-opts"},
		{name: "underscore overrides underscore", override: "ws_opts:\n  path: /alice", key: "ws_opts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := directOverrideDoc(t)
			doc.Stacks[1].DirectOverrides = []configdoc.DirectOverride{{User: "alice", Upstream: "server-a", Config: rawMapping(t, tc.override)}}
			require.Empty(t, configdoc.Validate(doc))
			stack := doc.Stacks[1]
			config := configdoc.DirectNodeConfig(stack, "alice", stack.Mihomo.Upstreams[0])
			keys := []string{}
			for index := 0; index < len(config.Node.Content); index += 2 {
				keys = append(keys, config.Node.Content[index].Value)
			}
			require.Equal(t, []string{"type", "server", "port", "uuid", "network", "tls", tc.key}, keys)
			for _, format := range []func(sub.Node) string{
				func(node sub.Node) string { out, _ := yaml.Marshal(sub.RenderClashProxy(node)); return string(out) },
				sub.RenderDirectSurgeProxy,
			} {
				output := format(directNodesByUser(t, doc)["alice"])
				require.Contains(t, output, "/alice")
				require.NotContains(t, output, "/vmess")
			}
			require.Contains(t, sub.RenderDirectSurgeProxy(directNodesByUser(t, doc)["bob"]), "/vmess")
		})
	}
	doc := directOverrideDoc(t)
	doc.Stacks[1].DirectOverrides = []configdoc.DirectOverride{{User: "alice", Upstream: "server-a", Config: rawMapping(t, "dialer_proxy: other")}}
	require.Contains(t, configdoc.Validate(doc).Error(), "field dialer_proxy cannot be overridden")
}

// TestDirectOverrideValidation 验证引用、重复、空覆盖、禁止字段和合并后节点有效性的校验。
func TestDirectOverrideValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override configdoc.DirectOverride
		mutate   func(*configdoc.Document)
		want     string
	}{
		{name: "user not granted", override: configdoc.DirectOverride{User: "carol", Upstream: "server-a", Config: rawMapping(t, "uuid: x")},
			want: "stacks[1].direct_overrides[0].user"},
		{name: "user revoked", override: configdoc.DirectOverride{User: "bob", Upstream: "server-a", Config: rawMapping(t, "uuid: x")},
			mutate: func(doc *configdoc.Document) { doc.Stacks[1].DirectUsers = []string{"alice"} },
			want:   "must be one of the direct users"},
		{name: "missing upstream", override: configdoc.DirectOverride{User: "alice", Upstream: "server-z", Config: rawMapping(t, "uuid: x")},
			want: "upstream does not exist: server-z"},
		{name: "reference upstream", override: configdoc.DirectOverride{User: "alice", Upstream: "relay", Config: rawMapping(t, "uuid: x")},
			mutate: func(doc *configdoc.Document) {
				doc.Stacks[1].Mihomo.Upstreams = append(doc.Stacks[1].Mihomo.Upstreams, configdoc.Upstream{Name: "relay", Type: "xray-socks5", Stack: "usa2", Inbound: "relay"})
			},
			want: "must reference an external (raw) upstream"},
		{name: "empty config", override: configdoc.DirectOverride{User: "alice", Upstream: "server-a"},
			want: "must override at least one field"},
		{name: "type", override: configdoc.DirectOverride{User: "alice", Upstream: "server-a", Config: rawMapping(t, "type: trojan")},
			want: "field type cannot be overridden"},
		{name: "name", override: configdoc.DirectOverride{User: "alice", Upstream: "server-a", Config: rawMapping(t, "name: custom")},
			want: "field name cannot be overridden"},
		{name: "server-only field", override: configdoc.DirectOverride{User: "alice", Upstream: "server-a", Config: rawMapping(t, "dialer-proxy: other")},
			want: "field dialer-proxy cannot be overridden"},
		{name: "invalid merged port", override: configdoc.DirectOverride{User: "alice", Upstream: "server-a", Config: rawMapping(t, "port: 70000")},
			want: "port must be between 1 and 65535"},
		{name: "empty merged uuid", override: configdoc.DirectOverride{User: "alice", Upstream: "server-a", Config: rawMapping(t, "uuid: ''")},
			want: "stacks[1].direct_overrides[0].config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := directOverrideDoc(t)
			if tc.mutate != nil {
				tc.mutate(doc)
			}
			doc.Stacks[1].DirectOverrides = []configdoc.DirectOverride{tc.override}
			err := configdoc.Validate(doc)
			require.NotEmpty(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}

	doc := directOverrideDoc(t)
	override := configdoc.DirectOverride{User: "alice", Upstream: "server-a", Config: rawMapping(t, "uuid: cccccccc-cccc-4ccc-8ccc-cccccccccccc")}
	doc.Stacks[1].DirectOverrides = []configdoc.DirectOverride{override, override}
	require.Contains(t, configdoc.Validate(doc).Error(), "duplicate override for user alice on upstream server-a")

	// 上游自身无效时只报告上游错误。
	doc = directOverrideDoc(t)
	doc.Stacks[1].Mihomo.Upstreams[0].Config.Delete("uuid")
	doc.Stacks[1].DirectOverrides = []configdoc.DirectOverride{{User: "alice", Upstream: "server-a", Config: rawMapping(t, "servername: sni.example.com")}}
	err := configdoc.Validate(doc).Error()
	require.Contains(t, err, "mihomo.upstreams[0].config")
	require.NotContains(t, err, "direct_overrides")
}

// TestDirectOverrideLifecycle 验证用户改名删除、上游改名、克隆、导出和文档往返时覆盖随之维护。
func TestDirectOverrideLifecycle(t *testing.T) {
	doc := directOverrideDoc(t)
	doc.Stacks[1].DirectOverrides = []configdoc.DirectOverride{
		{User: "alice", Upstream: "server-a", Config: rawMapping(t, "uuid: cccccccc-cccc-4ccc-8ccc-cccccccccccc")},
		{User: "bob", Upstream: "server-a", Config: rawMapping(t, "uuid: dddddddd-dddd-4ddd-8ddd-dddddddddddd")},
	}
	require.Empty(t, configdoc.Validate(doc))

	encoded, err := configdoc.Marshal(doc, configdoc.FormatYAML)
	require.NoError(t, err)
	parsed, err := configdoc.Parse(encoded)
	require.NoError(t, err)
	require.Len(t, parsed.Stacks[1].DirectOverrides, 2)
	require.Empty(t, parsed.Stacks[0].DirectOverrides)
	value, _ := parsed.Stacks[1].DirectOverrides[1].Config.Get("uuid")
	require.Equal(t, "dddddddd-dddd-4ddd-8ddd-dddddddddddd", value)

	exported, err := configdoc.Subset(doc, configdoc.ExportScope{Scope: configdoc.ScopeStacks, Stacks: []string{"usa1"}, IncludeReferencedUsers: true}, time.Now())
	require.NoError(t, err)
	require.Len(t, exported.Stacks[0].DirectOverrides, 2)

	clone := configdoc.CloneStack(&doc.Stacks[1], "usa3")
	require.Len(t, clone.DirectOverrides, 2)
	clone.DirectOverrides[0].Config.Set("uuid", "changed")
	value, _ = doc.Stacks[1].DirectOverrides[0].Config.Get("uuid")
	require.Equal(t, "cccccccc-cccc-4ccc-8ccc-cccccccccccc", value, "clone must deep copy override config")

	configdoc.RenameDirectOverrideUpstream(&doc.Stacks[1], "server-a", "server-x")
	require.Equal(t, "server-x", doc.Stacks[1].DirectOverrides[0].Upstream)
	require.Equal(t, "server-x", doc.Stacks[1].DirectOverrides[1].Upstream)
	configdoc.RenameDirectOverrideUpstream(&doc.Stacks[1], "server-x", "server-a")

	configdoc.RenameUser(doc, "alice", "alice2")
	doc.Users[0].Name = "alice2"
	require.Equal(t, "alice2", doc.Stacks[1].DirectOverrides[0].User)
	require.Empty(t, configdoc.Validate(doc))

	configdoc.RemoveUserRefs(doc, "bob")
	require.Len(t, doc.Stacks[1].DirectOverrides, 1)
	require.Equal(t, "alice2", doc.Stacks[1].DirectOverrides[0].User)
}
