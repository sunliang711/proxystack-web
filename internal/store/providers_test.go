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

// TestProviderStorageKeepsState verifies that full-document saves preserve IDs and runtime state.
func TestProviderStorageKeepsState(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	data, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "tests", "fixtures", "example-doc.yaml"))
	require.NoError(t, err)
	doc, err := configdoc.Parse(data)
	require.NoError(t, err)
	doc.Stacks[1].Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600}}
	doc.Stacks[1].Mihomo.Groups[0].Use = []string{"airport"}
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error { return store.SaveDocument(ctx, q, doc) }))
	id := doc.Stacks[1].Mihomo.Providers[0].ID
	require.Positive(t, id)
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
		return q.PutMihomoProviderState(ctx, dbq.PutMihomoProviderStateParams{ProviderID: id, ConfigHash: "hash", LastAttemptAt: 10, LastSuccessAt: 9, LastError: "failed"})
	}))
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
		loaded, err := store.LoadDocument(ctx, q)
		if err != nil {
			return err
		}
		require.Equal(t, id, loaded.Stacks[1].Mihomo.Providers[0].ID)
		require.Equal(t, []string{"airport"}, loaded.Stacks[1].Mihomo.Groups[0].Use)
		loaded.Stacks[1].Mihomo.Providers[0].Name = "renamed"
		loaded.Stacks[1].Mihomo.Groups[0].Use = []string{"renamed"}
		return store.SaveDocument(ctx, q, loaded)
	}))
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
		states, err := q.ListMihomoProviderState(ctx)
		require.Len(t, states, 1)
		require.Equal(t, int64(9), states[0].LastSuccessAt)
		return err
	}))
	doc.Stacks[1].Mihomo.Providers = nil
	doc.Stacks[1].Mihomo.Groups[0].Use = nil
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error { return store.SaveDocument(ctx, q, doc) }))
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
		states, err := q.ListMihomoProviderState(ctx)
		require.Empty(t, states)
		return err
	}))
}
