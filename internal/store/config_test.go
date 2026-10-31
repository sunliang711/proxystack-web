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

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	return st
}

// TestSaveLoadDocumentRoundTrip 验证文档写入配置表再读出后内容一致，ID 稳定。
func TestSaveLoadDocumentRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	data, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "tests", "fixtures", "example-doc.yaml"))
	require.NoError(t, err)
	doc, err := configdoc.Parse(data)
	require.NoError(t, err)
	doc.Users[0].Profiles = []configdoc.Profile{{Name: "mobile", UUID: configdoc.RandomUUID(), SSPassword: "x"}}
	doc.Stacks[1].Xray.Inbounds[1].Users[0].Profile = "mobile"

	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error { return store.SaveDocument(ctx, q, doc) }))
	var loaded *configdoc.Document
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
		loaded, err = store.LoadDocument(ctx, q)
		return err
	}))
	want, err := configdoc.Marshal(doc, configdoc.FormatJSON)
	require.NoError(t, err)
	got, err := configdoc.Marshal(loaded, configdoc.FormatJSON)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))

	// 改名并删除一个 stack 后再保存：ID 不变，引用跟随改名。
	usa1ID := loaded.Stacks[1].ID
	configdoc.RenameStack(loaded, "usa1", "us-east")
	loaded.Stacks[1].Name = "us-east"
	loaded.Stacks = loaded.Stacks[:2]
	configdoc.SetMembers(&loaded.Stacks[0], []string{"us-east"})
	require.Empty(t, configdoc.Validate(loaded))
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error { return store.SaveDocument(ctx, q, loaded) }))
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
		loaded, err = store.LoadDocument(ctx, q)
		return err
	}))
	require.Len(t, loaded.Stacks, 2)
	require.Equal(t, usa1ID, loaded.Stacks[1].ID)
	require.Equal(t, "us-east", loaded.Stacks[0].Mihomo.Upstreams[0].Stack)
}
