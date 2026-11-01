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
	}
	configdoc.RenameUser(doc, "alice", "renamed")
	doc.Users[0].Name = "renamed"
	saveAndLoad()
	require.Equal(t, []string{"renamed"}, doc.Stacks[1].DirectUsers)
	doc.Stacks[1].DirectUsers = nil
	saveAndLoad()
	require.Empty(t, doc.Stacks[1].DirectUsers)
}
