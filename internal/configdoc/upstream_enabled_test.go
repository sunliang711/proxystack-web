package configdoc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/generator/mihomo"
)

func boolPtr(value bool) *bool { return &value }

// TestUpstreamEnableDisableCompileAndRender 验证停用上游不会写入 Mihomo 代理与代理组，保留原配置结构。
func TestUpstreamEnableDisableCompileAndRender(t *testing.T) {
	doc := loadExampleDoc(t)
	auto := &doc.Stacks[0]
	require.Len(t, auto.Mihomo.Upstreams, 2)
	auto.Mihomo.Upstreams[0].Enabled = boolPtr(false) // usa1-local disabled, usa2-local enabled

	require.Empty(t, configdoc.Validate(doc))
	set := compileResolved(t, doc)

	var compiledAuto *configdoc.Stack
	_ = compiledAuto
	for _, s := range set.Stacks {
		if s.Name == "auto" {
			require.Len(t, s.Clash.Upstreams, 1)
			require.Equal(t, "usa2-local", s.Clash.Upstreams[0].Name)
			require.Equal(t, []string{"usa2-local"}, s.Clash.Groups[0].Proxies)
			require.Equal(t, []string{"usa2-local"}, s.Clash.Groups[1].Proxies)
			require.Equal(t, []string{"AutoProxy", "BalanceProxy", "usa2-local", "DIRECT"}, s.Clash.Groups[2].Proxies)
		}
	}

	rendered, err := mihomo.DumpsConfig(set, "auto")
	require.NoError(t, err)
	require.NotContains(t, rendered, "usa1-local")
	require.Contains(t, rendered, "usa2-local")

	// 重新启用后原位置与代理组成员关系完好恢复。
	auto.Mihomo.Upstreams[0].Enabled = boolPtr(true)
	require.Empty(t, configdoc.Validate(doc))
	rendered, err = mihomo.DumpsConfig(compileResolved(t, doc), "auto")
	require.NoError(t, err)
	require.Contains(t, rendered, "usa1-local")
	require.Contains(t, rendered, "usa2-local")
}

// TestUpstreamDisableValidationRules 验证停用上游对代理组与规则的校验边界。
func TestUpstreamDisableValidationRules(t *testing.T) {
	t.Run("group with only disabled upstream fails", func(t *testing.T) {
		doc := loadExampleDoc(t)
		doc.Stacks[1].Mihomo.Upstreams[0].Enabled = boolPtr(false)
		doc.Stacks[1].Mihomo.Groups[0].Proxies = []string{"server-a"}
		issues := configdoc.Validate(doc)
		require.Contains(t, issues.Error(), "stacks[1].mihomo.groups[0].proxies: all referenced upstreams are disabled")
	})

	t.Run("group with disabled upstream and provider succeeds", func(t *testing.T) {
		doc := loadExampleDoc(t)
		doc.Stacks[1].Mihomo.Upstreams[0].Enabled = boolPtr(false)
		doc.Stacks[1].Mihomo.Providers = []configdoc.Provider{{
			Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600,
		}}
		doc.Stacks[1].Mihomo.Groups[0].Proxies = []string{"server-a"}
		doc.Stacks[1].Mihomo.Groups[0].Use = []string{"airport"}
		require.Empty(t, configdoc.Validate(doc))
	})

	t.Run("rules final targeting disabled upstream fails", func(t *testing.T) {
		doc := loadExampleDoc(t)
		doc.Stacks[1].Mihomo.Upstreams[0].Enabled = boolPtr(false)
		doc.Stacks[1].Mihomo.Rules.Final = "server-a"
		issues := configdoc.Validate(doc)
		require.Contains(t, issues.Error(), "stacks[1].mihomo.rules.final: target upstream is disabled: server-a")
	})

	t.Run("extra rule targeting disabled upstream fails", func(t *testing.T) {
		doc := loadExampleDoc(t)
		doc.Stacks[1].Mihomo.Upstreams[0].Enabled = boolPtr(false)
		doc.Stacks[1].Mihomo.Rules.Extra = []configdoc.Rule{{Type: "DOMAIN-SUFFIX", Value: "example.com", Target: "server-a"}}
		issues := configdoc.Validate(doc)
		require.Contains(t, issues.Error(), "stacks[1].mihomo.rules.extra[0].target: target upstream is disabled: server-a")
	})
}

// TestUpstreamDisableDirectAndRelaysOnly 验证停用上游对直连节点生成与 RelaysOnly 判定的影响。
func TestUpstreamDisableDirectAndRelaysOnly(t *testing.T) {
	doc := loadExampleDoc(t)
	usa1 := &doc.Stacks[1]
	usa1.Xray.Inbounds[0].Sub = false
	usa1.DirectUsers = []string{"alice"}
	second := directUpstream(t, "server-b", "type: socks5\nserver: b.example.com\nport: 1080")
	second.Enabled = boolPtr(false)
	usa1.Mihomo.Upstreams = append(usa1.Mihomo.Upstreams, second)

	// 只有 1 个启用外部上游，因此节点名不再拼接上游名；已停用的 server-b 不生成直连节点。
	nodes, err := configdoc.StackDirectNodes(doc)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.Equal(t, "usa1 vmess [DRT]", nodes[0].Remark)

	// 全部外部上游停用时不再生成直连节点。
	usa1.Mihomo.Upstreams[0].Enabled = boolPtr(false)
	nodes, err = configdoc.StackDirectNodes(doc)
	require.NoError(t, err)
	require.Empty(t, nodes)

	// auto stack 中所有上游停用时不再视为 RelaysOnly；若混入的 raw 上游被停用则仍视为 RelaysOnly。
	auto := &doc.Stacks[0]
	require.True(t, configdoc.RelaysOnly(auto))
	rawDisabled := directUpstream(t, "raw-off", "type: socks5\nserver: c.example.com\nport: 1080")
	rawDisabled.Enabled = boolPtr(false)
	auto.Mihomo.Upstreams = append(auto.Mihomo.Upstreams, rawDisabled)
	require.True(t, configdoc.RelaysOnly(auto))

	auto.Mihomo.Upstreams[0].Enabled = boolPtr(false)
	auto.Mihomo.Upstreams[1].Enabled = boolPtr(false)
	require.False(t, configdoc.RelaysOnly(auto))
}

// TestMapDomainPathWithDisabledUpstreams 验证过滤停用上游后的下标映射准确回到源文档下标。
func TestMapDomainPathWithDisabledUpstreams(t *testing.T) {
	doc := loadExampleDoc(t)
	doc.Stacks[0].Mihomo.Upstreams[0].Enabled = boolPtr(false)
	mapped := configdoc.MapDomainPath(doc, "stacks.auto.clash.upstreams[0].ref")
	require.Equal(t, "stacks[0].mihomo.upstreams[1].inbound", mapped)
}
