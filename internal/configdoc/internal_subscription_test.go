package configdoc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/generator/sub"
)

// TestAppendInternalNodes verifies authorization isolation and stable internal node fields.
func TestAppendInternalNodes(t *testing.T) {
	doc := &configdoc.Document{
		Settings: &configdoc.Settings{Basic: configdoc.BasicSettings{InternalHost: "10.2.1.115"}},
		Users: []configdoc.User{
			{Name: "alice", InternalNodes: true},
			{Name: "bob"},
		},
	}
	nodes := []sub.Node{
		{ID: "de1:vmess:alice", User: "alice", Server: "public.example.com", Tag: "vmess:4305:alice", Remark: "DE1"},
		{ID: "de1:vmess:bob", User: "bob", Server: "public.example.com", Tag: "vmess:4305:bob", Remark: "DE1 Bob"},
	}

	got := configdoc.AppendInternalNodes(doc, nodes)

	require.Len(t, got, 3)
	require.Equal(t, nodes[0], got[0])
	require.Equal(t, "de1:vmess:alice:lan", got[1].ID)
	require.Equal(t, "vmess:4305:alice:lan", got[1].Tag)
	require.Equal(t, "DE1 [LAN]", got[1].Remark)
	require.Equal(t, "10.2.1.115", got[1].Server)
	require.Equal(t, nodes[1], got[2])
	require.Equal(t, "public.example.com", nodes[0].Server, "the source node must not be modified")
}

// TestInternalNodesRequireValidHost verifies the setting and authorization dependency.
func TestInternalNodesRequireValidHost(t *testing.T) {
	doc := loadExampleDoc(t)
	doc.Users[0].InternalNodes = true
	require.Contains(t, configdoc.Validate(doc).Error(), "internal_nodes")

	doc.Settings.Basic.InternalHost = "not a host!"
	require.Contains(t, configdoc.Validate(doc).Error(), "internal_host")

	doc.Settings.Basic.InternalHost = "proxy.lan"
	require.NotContains(t, configdoc.Validate(doc).Error(), "internal_nodes")
	require.NotContains(t, configdoc.Validate(doc).Error(), "internal_host")
}

// TestInternalNodeNameCollision verifies that the generated suffix cannot shadow another public node.
func TestInternalNodeNameCollision(t *testing.T) {
	doc := &configdoc.Document{
		Settings: &configdoc.Settings{Basic: configdoc.BasicSettings{InternalHost: "10.2.1.115"}},
		Users:    []configdoc.User{{Name: "alice", InternalNodes: true}},
	}
	input := sub.Input{
		InputSchema: sub.InputSchema, InputVersion: sub.InputVersion, Source: "local", GeneratedAt: "now",
		Nodes: []sub.Node{
			{ID: "de1:vmess:alice", User: "alice", Protocol: "vmess", Server: "public.example.com", Port: 443, UUID: "11111111-1111-4111-8111-111111111111", Network: "raw", Tag: "de1", Remark: "DE1"},
			{ID: "de2:vmess:alice", User: "alice", Protocol: "vmess", Server: "public.example.com", Port: 443, UUID: "11111111-1111-4111-8111-111111111111", Network: "raw", Tag: "de2", Remark: "DE1 [LAN]"},
		},
	}
	input.Nodes = configdoc.AppendInternalNodes(doc, input.Nodes)

	require.Contains(t, input.Validate().Error(), "duplicate proxy name")
}
