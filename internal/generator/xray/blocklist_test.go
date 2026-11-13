package xray_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/domain"
	"proxystack-web/internal/generator/xray"
)

// renderJSON 生成 stack 的 xray 配置并解析为通用结构。
func renderJSON(t *testing.T, stackSet domain.StackSet, name string) map[string]any {
	t.Helper()
	output, err := xray.DumpsConfig(stackSet, name)
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(output), &parsed))
	return parsed
}

// TestRenderConfigWithoutBlocklistHasNoSniffingOrRouting 验证没有域名黑名单时生成的配置不开嗅探、不加黑洞和路由。
func TestRenderConfigWithoutBlocklistHasNoSniffingOrRouting(t *testing.T) {
	stackSet := makeStackSet(t, makeStack(t, "vm", noAuthSocksOutboundYAML, asListItem(twoUserVmessInboundYAML), ""))
	stackSet.Config.UserBlockedDomains = map[string][]string{"carol": {"domain:example.com"}}

	parsed := renderJSON(t, stackSet, "vm")

	require.NotContains(t, parsed["inbounds"].([]any)[0], "sniffing")
	require.Nil(t, parsed["routing"])
	require.Len(t, parsed["outbounds"], 1, "users without a list on this stack add nothing")
}

// TestRenderConfigBlocksDomainsGloballyAndPerUser 验证全局和按用户的名单生成黑洞出口、路由规则和只用于路由的嗅探。
func TestRenderConfigBlocksDomainsGloballyAndPerUser(t *testing.T) {
	stackSet := makeStackSet(t, makeStack(t, "socksprivate", `type: socks5
server: socks.example.com
port: 1080
private_direct: true`, asListItem(twoUserVmessInboundYAML), ""))
	stackSet.Config.BlockedDomains = []string{"domain:ads.example", "full:www.tracker.example"}
	stackSet.Config.UserBlockedDomains = map[string][]string{"bob": {"domain:video.example"}}
	// 暂停的用户也保留拦截规则：恢复经 API 热添加，不重新生成配置。
	stackSet.DisabledUsers = domain.DisabledUserSet{}
	stackSet.DisabledUsers.Add("socksprivate", "bob")

	parsed := renderJSON(t, stackSet, "socksprivate")

	inbound := parsed["inbounds"].([]any)[0].(map[string]any)
	require.Equal(t, map[string]any{"enabled": true, "destOverride": []any{"http", "tls", "quic"}, "routeOnly": true}, inbound["sniffing"])

	outbounds := parsed["outbounds"].([]any)
	require.Len(t, outbounds, 3)
	require.Equal(t, "egress-socksprivate", outbounds[0].(map[string]any)["tag"], "the default egress stays first")
	require.Equal(t, map[string]any{"tag": "block-socksprivate", "protocol": "blackhole", "settings": map[string]any{}}, outbounds[2])

	rules := parsed["routing"].(map[string]any)["rules"].([]any)
	require.Len(t, rules, 4, "two block rules before the two private-direct rules")
	require.Equal(t, map[string]any{
		"type": "field", "user": []any{"bob"}, "domain": []any{"domain:video.example"}, "outboundTag": "block-socksprivate",
	}, rules[0])
	require.Equal(t, map[string]any{
		"type": "field", "domain": []any{"domain:ads.example", "full:www.tracker.example"}, "outboundTag": "block-socksprivate",
	}, rules[1])
	require.Equal(t, "private-direct-socksprivate", rules[2].(map[string]any)["outboundTag"])
}

// TestRenderBlockRulesUsesEveryProfileEmail 验证按用户的规则匹配该用户在本 stack 引用的全部档案 email。
func TestRenderBlockRulesUsesEveryProfileEmail(t *testing.T) {
	global := domain.GlobalConfig{UserBlockedDomains: map[string][]string{"alice": {"domain:example.com"}}}
	inbounds := []domain.Inbound{
		{Users: []domain.InboundUser{{User: "alice", Email: "alice.u1"}, {User: "bob", Email: "bob.u2"}}},
		{Users: []domain.InboundUser{{User: "alice", Profile: "phone", Email: "alice.u1.p3"}, {User: "alice", Email: "alice.u1"}}},
	}

	rules := xray.RenderBlockRules(global, inbounds, "block-s")

	require.Equal(t, []xray.RoutingRule{
		{Type: "field", User: []string{"alice.u1", "alice.u1.p3"}, Domain: []string{"domain:example.com"}, OutboundTag: "block-s"},
	}, rules)
	require.Empty(t, xray.RenderBlockRules(domain.GlobalConfig{}, inbounds, "block-s"))
}
