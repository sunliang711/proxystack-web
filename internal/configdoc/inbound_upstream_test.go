package configdoc_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/generator/mihomo"
	"proxystack-web/internal/graph"
)

// TestInboundUpstreamProtocols verifies local addressing, credentials, transports and legacy coexistence.
func TestInboundUpstreamProtocols(t *testing.T) {
	for _, protocol := range []string{"socks5", "http", "vmess", "shadowsocks", "ss2022", "vmess-ws", "vmess-grpc"} {
		t.Run(protocol, func(t *testing.T) {
			doc := loadExampleDoc(t)
			inbound := &doc.Stacks[1].Xray.Inbounds[1]
			inbound.Listen, inbound.Server, inbound.Sub = "0.0.0.0", "public.example.com", false
			upstream := &doc.Stacks[0].Mihomo.Upstreams[0]
			upstream.Type, upstream.Inbound = "xray-inbound", inbound.Name
			inbound.Protocol = protocol
			switch protocol {
			case "http", "socks5":
				inbound.Network, inbound.UDP = "", protocol == "socks5"
				inbound.AuthType, inbound.AuthUsername, inbound.AuthPassword = "password", "relay-user", "relay-password"
			case "shadowsocks", "ss2022":
				inbound.Protocol, inbound.Network, inbound.Method = "shadowsocks", "", "aes-128-gcm"
				inbound.Password = "demo-server-password"
				if protocol == "ss2022" {
					inbound.Method = "2022-blake3-aes-128-gcm"
					inbound.Password = "MDEyMzQ1Njc4OWFiY2RlZg=="
					doc.Users[0].SSPassword = "ZmVkY2JhOTg3NjU0MzIxMA=="
				}
			case "vmess-ws":
				inbound.Protocol, inbound.Network, inbound.WSPath = "vmess", "ws", "/relay"
				inbound.WSHeaders = map[string]string{"Host": "relay.example.com"}
			case "vmess-grpc":
				inbound.Protocol, inbound.Network, inbound.GRPCServiceName = "vmess", "grpc", "relay-service"
			}
			if inbound.Protocol == "vmess" || inbound.Protocol == "shadowsocks" {
				upstream.User = "alice"
			}
			set := compileResolved(t, doc)
			rendered, err := mihomo.DumpsConfig(set, "auto")
			require.NoError(t, err)
			var config struct {
				Proxies []map[string]any `yaml:"proxies"`
			}
			require.NoError(t, yaml.Unmarshal([]byte(rendered), &config))
			proxy := config.Proxies[0]
			require.Equal(t, "127.0.0.1", proxy["server"])
			require.Equal(t, inbound.Port, proxy["port"])
			require.Equal(t, "socks5", config.Proxies[1]["type"])
			switch inbound.Protocol {
			case "socks5", "http":
				require.Equal(t, inbound.Protocol, proxy["type"])
				require.Equal(t, "relay-user", proxy["username"])
				require.Equal(t, "relay-password", proxy["password"])
				if inbound.Protocol == "http" {
					require.NotContains(t, proxy, "udp")
				}
			case "vmess":
				require.Equal(t, "vmess", proxy["type"])
				require.Equal(t, doc.Users[0].UUID, proxy["uuid"])
				if inbound.Network == "raw" {
					require.Equal(t, "tcp", proxy["network"])
				}
				if inbound.Network == "ws" {
					require.Equal(t, "/relay", proxy["ws-opts"].(map[string]any)["path"])
				}
				if inbound.Network == "grpc" {
					require.Equal(t, "relay-service", proxy["grpc-opts"].(map[string]any)["grpc-service-name"])
				}
			case "shadowsocks":
				require.Equal(t, "ss", proxy["type"])
				require.Equal(t, inbound.Method, proxy["cipher"])
				password := doc.Users[0].SSPassword
				if protocol == "ss2022" {
					password = inbound.Password + ":" + password
				}
				require.Equal(t, password, proxy["password"])
			}
			references, err := graph.BuildReferenceGraph(set)
			require.NoError(t, err)
			require.True(t, references.Dependencies[graph.ServiceNode{Stack: "auto", Component: "clash"}][graph.ServiceNode{Stack: "usa1", Component: "xray"}])
		})
	}
}

// TestInboundUpstreamUserSelection prevents ambiguous or stale credentials from being chosen silently.
func TestInboundUpstreamUserSelection(t *testing.T) {
	doc := loadExampleDoc(t)
	other := doc.Users[0]
	other.Name, other.UUID, other.SubToken = "bob", configdoc.RandomUUID(), "bob-token"
	other.Profiles = []configdoc.Profile{{Name: "mobile", UUID: configdoc.RandomUUID(), SSPassword: "mobile-password"}}
	doc.Users = append(doc.Users, other)
	inbound := &doc.Stacks[1].Xray.Inbounds[1]
	inbound.Users = append(inbound.Users, configdoc.UserRef{User: "bob", Profile: "mobile"})
	upstream := &doc.Stacks[0].Mihomo.Upstreams[0]
	upstream.Type, upstream.Inbound = "xray-inbound", "vmess"
	require.Contains(t, configdoc.Validate(doc).Error(), "select an inbound user/profile")
	upstream.User, upstream.Profile = "bob", "missing"
	require.Contains(t, configdoc.Validate(doc).Error(), "not attached")
	upstream.Profile = "mobile"
	set := compileResolved(t, doc)
	rendered, err := mihomo.DumpsConfig(set, "auto")
	require.NoError(t, err)
	require.Contains(t, rendered, doc.Users[1].Profiles[0].UUID)
	require.NotContains(t, rendered, doc.Users[0].UUID)
	configdoc.RenameUser(doc, "bob", "carol")
	doc.Users[1].Name = "carol"
	configdoc.RenameProfile(doc, "carol", "mobile", "travel")
	doc.Users[1].Profiles[0].Name = "travel"
	require.Equal(t, "carol", upstream.User)
	require.Equal(t, "travel", upstream.Profile)
	require.Empty(t, configdoc.Validate(doc))
	configdoc.RemoveProfileRefs(doc, "carol", "travel")
	require.Empty(t, upstream.Profile)
}

// TestInboundUpstreamReferenceOperations covers member synchronization, rename, clone and cycle detection.
func TestInboundUpstreamReferenceOperations(t *testing.T) {
	doc := loadExampleDoc(t)
	auto := &doc.Stacks[0]
	auto.Mihomo.Upstreams[0].Type, auto.Mihomo.Upstreams[0].Inbound = "xray-inbound", "vmess"
	require.True(t, configdoc.RelaysOnly(auto))
	configdoc.SetMembers(auto, []string{"usa1", "usa2"})
	require.Equal(t, "vmess", auto.Mihomo.Upstreams[0].Inbound)
	configdoc.RenameStack(doc, "usa1", "renamed")
	doc.Stacks[1].Name = "renamed"
	configdoc.RenameInbound(doc, "renamed", "vmess", "renamed-vmess")
	doc.Stacks[1].Xray.Inbounds[1].Name = "renamed-vmess"
	require.Empty(t, configdoc.Validate(doc))
	configdoc.SetMembers(auto, []string{"usa2"})
	require.Len(t, auto.Mihomo.Upstreams, 1)
	require.NotContains(t, auto.Mihomo.Groups[0].Proxies, "usa1-local")
	doc.Stacks[1].Mihomo.Upstreams = append(doc.Stacks[1].Mihomo.Upstreams, configdoc.Upstream{Name: "cycle", Type: "xray-inbound", Stack: "auto", Inbound: "relay"})
	auto.Mihomo.Upstreams[0].Stack, auto.Mihomo.Upstreams[0].Inbound, auto.Mihomo.Upstreams[0].Type = "renamed", "renamed-vmess", "xray-inbound"
	require.Contains(t, configdoc.Validate(doc).Error(), "dependency cycle")
	clone := configdoc.CloneStack(&doc.Stacks[1], "copy")
	require.Equal(t, "auto", clone.Mihomo.Upstreams[len(clone.Mihomo.Upstreams)-1].Stack)
}

// TestInboundUpstreamExport includes connection users even when the exporting stack has no inbound for them.
func TestInboundUpstreamExport(t *testing.T) {
	doc := loadExampleDoc(t)
	other := doc.Users[0]
	other.Name, other.UUID, other.SubToken = "bob", configdoc.RandomUUID(), "bob-token"
	doc.Users = append(doc.Users, other)
	doc.Stacks[1].Xray.Inbounds[1].Users = []configdoc.UserRef{{User: "bob"}}
	upstream := &doc.Stacks[0].Mihomo.Upstreams[0]
	upstream.Type, upstream.Inbound, upstream.User = "xray-inbound", "vmess", "bob"
	for _, include := range []bool{true, false} {
		exported, err := configdoc.Subset(doc, configdoc.ExportScope{Scope: configdoc.ScopeStacks, Stacks: []string{"auto"}, IncludeReferencedUsers: include}, time.Now())
		require.NoError(t, err)
		if include {
			require.GreaterOrEqual(t, exported.UserByName("bob"), 0)
		} else {
			require.Empty(t, exported.Users)
		}
		require.Len(t, exported.Stacks, 1)
		require.Equal(t, "bob", exported.Stacks[0].Mihomo.Upstreams[0].User)
		for _, format := range []string{configdoc.FormatJSON, configdoc.FormatYAML} {
			data, err := configdoc.Marshal(exported, format)
			require.NoError(t, err)
			parsed, err := configdoc.Parse(data)
			require.NoError(t, err)
			require.Equal(t, "xray-inbound", parsed.Stacks[0].Mihomo.Upstreams[0].Type)
			require.Equal(t, "bob", parsed.Stacks[0].Mihomo.Upstreams[0].User)
		}
	}
}
