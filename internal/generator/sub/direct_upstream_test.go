package sub_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	subgen "proxystack-web/internal/generator/sub"
)

// TestDirectSurgeCredentials 保证内置和自定义模板均原样输出认证串，不能经过 HTML 转义。
func TestDirectSurgeCredentials(t *testing.T) {
	var node subgen.Node
	require.NoError(t, yaml.Unmarshal([]byte(`
id: usa1:direct:http:alice
user: alice
direct: true
type: http
name: Remote & secure[DRT]
server: remote.example.com
port: 8443
tls: true
username: user&name
password: secret<value>&test
sni: remote.example.com
`), &node))
	index, err := subgen.BuildIndex([]subgen.Node{node}, []string{"local"}, subgen.Access{Type: "none"}, fixedGeneratedAt)
	require.NoError(t, err)
	for _, template := range []string{"", "{% for line in surge_proxy_lines %}{{ line }}\n{% endfor %}"} {
		output, err := subgen.Render(subgen.FormatSurge, index, "alice", template, subgen.SurgeManaged{})
		require.NoError(t, err)
		require.Contains(t, output, `username="user&name", password="secret<value>&test"`)
		require.Contains(t, output, "Remote & secure[DRT]")
		require.NotContains(t, output, "&quot;")
		require.NotContains(t, output, "&amp;")
	}
}

// TestDirectSurgeUnsupportedExtensions 验证无法正确转换的上游不进入 Surge，Clash 仍保留原配置。
func TestDirectSurgeUnsupportedExtensions(t *testing.T) {
	for _, config := range []string{
		"type: ss\ncipher: aes-128-gcm\npassword: secret\nplugin: v2ray-plugin\nplugin-opts: {tls: true}",
		"type: vmess\nuuid: 11111111-1111-4111-8111-111111111111\nalterId: 64",
	} {
		var node subgen.Node
		require.NoError(t, yaml.Unmarshal([]byte("id: direct:test\nuser: alice\ndirect: true\nname: Unsupported[DRT]\nserver: remote.example.com\nport: 443\n"+config), &node))
		require.NoError(t, node.Validate())
		require.Empty(t, subgen.RenderSurgeProxyLines([]subgen.Node{node}))
		proxy, err := yaml.Marshal(subgen.RenderClashProxy(node))
		require.NoError(t, err)
		require.Contains(t, string(proxy), "Unsupported[DRT]")
		if node.Protocol == "shadowsocks" {
			require.Contains(t, string(proxy), "plugin: v2ray-plugin")
		}
	}
}
