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

// TestDirectUsersRoundTrip 验证授权保存、用户改名和撤销后读回的结果。
func TestDirectUsersRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	data, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "tests", "fixtures", "example-doc.yaml"))
	require.NoError(t, err)
	doc, err := configdoc.Parse(data)
	require.NoError(t, err)
	doc.Stacks[1].DirectUsers = []string{"alice"}
	doc.Stacks[1].DirectSuffix = "  [直连]"
	saveAndLoad := func() {
		t.Helper()
		require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error { return store.SaveDocument(ctx, q, doc) }))
		require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
			doc, err = store.LoadDocument(ctx, q)
			return err
		}))
	}
	for range 2 {
		saveAndLoad()
		require.Equal(t, []string{"alice"}, doc.Stacks[1].DirectUsers)
		require.Empty(t, doc.Stacks[0].DirectUsers)
		require.Equal(t, "  [直连]", doc.Stacks[1].DirectSuffix)
		require.Equal(t, configdoc.DefaultDirectSuffix, doc.Stacks[0].DirectSuffix)
		encoded, err := configdoc.Marshal(doc, configdoc.FormatYAML)
		require.NoError(t, err)
		doc, err = configdoc.Parse(encoded)
		require.NoError(t, err)
		require.Equal(t, "  [直连]", doc.Stacks[1].DirectSuffix)
	}
	configdoc.RenameUser(doc, "alice", "renamed")
	doc.Users[0].Name = "renamed"
	saveAndLoad()
	require.Equal(t, []string{"renamed"}, doc.Stacks[1].DirectUsers)
	doc.Stacks[1].DirectUsers = nil
	saveAndLoad()
	require.Empty(t, doc.Stacks[1].DirectUsers)
}

// TestDirectOverridesRoundTrip 验证直连覆盖的保存、顺序、改名和随用户删除清理。
func TestDirectOverridesRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	data, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "tests", "fixtures", "example-doc.yaml"))
	require.NoError(t, err)
	doc, err := configdoc.Parse(data)
	require.NoError(t, err)
	bob := doc.Users[0]
	bob.Name, bob.UUID, bob.SubToken = "bob", "22222222-2222-4222-8222-222222222222", "bob-token"
	doc.Users = append(doc.Users, bob)
	doc.Stacks[1].DirectUsers = []string{"alice", "bob"}
	override := func(user, uuid string) configdoc.DirectOverride {
		var config configdoc.RawMapping
		require.NoError(t, config.UnmarshalJSON([]byte(`{"uuid":"`+uuid+`","ws_opts":{"path":"/`+user+`"}}`)))
		return configdoc.DirectOverride{User: user, Upstream: "server-a", Config: config}
	}
	doc.Stacks[1].DirectOverrides = []configdoc.DirectOverride{
		override("bob", "dddddddd-dddd-4ddd-8ddd-dddddddddddd"),
		override("alice", "cccccccc-cccc-4ccc-8ccc-cccccccccccc"),
	}
	require.Empty(t, configdoc.Validate(doc))
	saveAndLoad := func() {
		t.Helper()
		require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error { return store.SaveDocument(ctx, q, doc) }))
		require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
			doc, err = store.LoadDocument(ctx, q)
			return err
		}))
	}
	for range 2 {
		saveAndLoad()
		overrides := doc.Stacks[1].DirectOverrides
		require.Len(t, overrides, 2)
		require.Equal(t, "bob", overrides[0].User)
		require.Equal(t, "alice", overrides[1].User)
		config, err := overrides[1].Config.MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, `{"uuid":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","ws_opts":{"path":"/alice"}}`, string(config))
		require.NotNil(t, doc.Stacks[0].DirectOverrides)
		require.Empty(t, doc.Stacks[0].DirectOverrides)
	}
	configdoc.RenameUser(doc, "alice", "renamed")
	doc.Users[0].Name = "renamed"
	saveAndLoad()
	require.Equal(t, "renamed", doc.Stacks[1].DirectOverrides[1].User)
	configdoc.RemoveUserRefs(doc, "bob")
	doc.Users = doc.Users[:1]
	saveAndLoad()
	require.Len(t, doc.Stacks[1].DirectOverrides, 1)
	require.Equal(t, "renamed", doc.Stacks[1].DirectOverrides[0].User)
}
