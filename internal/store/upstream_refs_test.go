package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/testutil"
)

// TestUpstreamUserReferenceRoundTrip keeps user/profile references stable through persistence and renames.
func TestUpstreamUserReferenceRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	data, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "tests", "fixtures", "example-doc.yaml"))
	require.NoError(t, err)
	doc, err := configdoc.Parse(data)
	require.NoError(t, err)
	doc.Users[0].Profiles = []configdoc.Profile{{Name: "mobile", UUID: configdoc.RandomUUID(), SSPassword: "demo-mobile-password"}}
	doc.Stacks[1].Xray.Inbounds[1].Users = []configdoc.UserRef{{User: "alice", Profile: "mobile"}}
	upstream := &doc.Stacks[0].Mihomo.Upstreams[0]
	upstream.Type, upstream.Inbound, upstream.User, upstream.Profile = "xray-inbound", "vmess", "alice", "mobile"
	require.Empty(t, configdoc.Validate(doc))
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error { return store.SaveDocument(ctx, q, doc) }))
	var loaded *configdoc.Document
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
		loaded, err = store.LoadDocument(ctx, q)
		return err
	}))
	require.Equal(t, *upstream, loaded.Stacks[0].Mihomo.Upstreams[0])
	require.Empty(t, configdoc.Validate(loaded))
	configdoc.RenameUser(loaded, "alice", "bob")
	loaded.Users[0].Name = "bob"
	configdoc.RenameProfile(loaded, "bob", "mobile", "travel")
	loaded.Users[0].Profiles[0].Name = "travel"
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error { return store.SaveDocument(ctx, q, loaded) }))
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
		loaded, err = store.LoadDocument(ctx, q)
		return err
	}))
	require.Equal(t, upstream.ID, loaded.Stacks[0].Mihomo.Upstreams[0].ID)
	require.Equal(t, "bob", loaded.Stacks[0].Mihomo.Upstreams[0].User)
	require.Equal(t, "travel", loaded.Stacks[0].Mihomo.Upstreams[0].Profile)
	require.Equal(t, "xray-socks5", loaded.Stacks[0].Mihomo.Upstreams[1].Type)
	require.Empty(t, configdoc.Validate(loaded))

	// Default profiles must also be canonical when SaveDocument is called without Normalize.
	loaded.Stacks[1].Xray.Inbounds[1].Users[0].Profile = ""
	loaded.Stacks[0].Mihomo.Upstreams[0].Profile = configdoc.DefaultProfile
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error { return store.SaveDocument(ctx, q, loaded) }))
	require.Empty(t, loaded.Stacks[0].Mihomo.Upstreams[0].Profile)
	want := loaded.Stacks[0].Mihomo.Upstreams[0]
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
		loaded, err = store.LoadDocument(ctx, q)
		return err
	}))
	require.Equal(t, want, loaded.Stacks[0].Mihomo.Upstreams[0])
}
