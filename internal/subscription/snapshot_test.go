package subscription_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/generator/sub"
	"proxystack-web/internal/subscription"
)

func socksNode(id, user, tag, remark string) sub.Node {
	return sub.Node{ID: id, User: user, Protocol: "socks5", Server: "198.51.100.1", Port: 4301, Tag: tag, Remark: remark, Auth: &sub.Auth{Type: "noauth"}}
}

func snapshot(source string, users []subscription.User, nodes ...sub.Node) *subscription.Snapshot {
	return &subscription.Snapshot{
		Source: source, Sequence: 1, GeneratedAt: time.Unix(1_800_000_000, 0).UTC(),
		Settings: subscription.Settings{DisabledResponse: subscription.DisabledNotFound},
		Users:    users, Nodes: nodes,
	}
}

// TestMergeIgnoresTagsAndSkipsDuplicateNames 验证两台机器 tag 相同的节点都保留（默认端口下 tag 必然相同），
// 同一用户下代理名重复的节点被跳过并给出警告，主来源没有的用户的节点被忽略。
func TestMergeIgnoresTagsAndSkipsDuplicateNames(t *testing.T) {
	users := []subscription.User{{Name: "alice", Token: "alice-token", Enabled: true}}
	primary := snapshot("main", users, socksNode("edge1:socks:alice", "alice", "socks:4301:alice", "edge1 socks"))
	east := snapshot("east", []subscription.User{{Name: "alice", Token: "other", Enabled: true}, {Name: "bob", Token: "bob-token", Enabled: true}},
		socksNode("edge2:socks:alice", "alice", "socks:4301:alice", "edge2 socks"),
		socksNode("edge1:socks:alice", "alice", "socks:4302:alice", "edge1 socks"),
		socksNode("edge2:socks:bob", "bob", "socks:4301:bob", "edge2 socks"),
	)
	view, err := subscription.Merge(primary, []*subscription.Snapshot{east})
	require.NoError(t, err)
	nodes := sub.NodesForUser(view.Index, "alice")
	require.Len(t, nodes, 2)
	require.Equal(t, "edge2 socks", nodes[1].Remark)
	require.Equal(t, "east:edge2:socks:alice", nodes[1].ID)
	require.Empty(t, sub.NodesForUser(view.Index, "bob"))
	require.Len(t, view.Warnings, 1)
	require.Contains(t, view.Warnings[0], "edge1 socks")
	require.Nil(t, view.UserByToken("other"), "tokens come from the primary source only")
}

// TestServeHonoursExpiryAtRequestTime 验证到期按请求时刻判断，不需要重新发布。
func TestServeHonoursExpiryAtRequestTime(t *testing.T) {
	expire := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	data := snapshot("main", []subscription.User{{Name: "alice", Token: "t", Enabled: true, ExpireAt: expire.Format(time.RFC3339)}},
		socksNode("edge1:socks:alice", "alice", "tag", "edge1 socks"))
	view, err := subscription.Merge(data, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, view.Serve("t", sub.FormatClash, nil, expire.Add(-time.Hour)).Status)
	require.Equal(t, http.StatusNotFound, view.Serve("t", sub.FormatClash, nil, expire).Status)
}

// TestBundleSealAndOpen 验证签名往返，以及错误公钥被拒绝。
func TestBundleSealAndOpen(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	data := snapshot("main", []subscription.User{{Name: "alice", Token: "t", Enabled: true}}, socksNode("n", "alice", "tag", "node"))
	sealed, err := subscription.Seal(data, private)
	require.NoError(t, err)

	opened, err := subscription.Open(sealed, func(string) (ed25519.PublicKey, error) { return public, nil })
	require.NoError(t, err)
	require.Equal(t, "main", opened.Source)
	require.Len(t, opened.Nodes, 1)

	_, err = subscription.Open(sealed, func(string) (ed25519.PublicKey, error) { return other, nil })
	require.ErrorIs(t, err, subscription.ErrBadSignature)

	parsed, err := subscription.ParsePublicKey(subscription.FormatPublicKey(public))
	require.NoError(t, err)
	require.True(t, public.Equal(parsed))
}
