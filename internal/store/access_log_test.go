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

// TestAccessLogRoundTrip 验证全局关闭和三态覆盖可以持久化，保存其它字段后不会丢失。
func TestAccessLogRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	data, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "tests", "fixtures", "example-doc.yaml"))
	require.NoError(t, err)
	doc, err := configdoc.Parse(data)
	require.NoError(t, err)
	off := false
	doc.Settings.Defaults.Xray.AccessLog = &off
	for index, mode := range []string{configdoc.Inherit, configdoc.On, configdoc.Off} {
		doc.Stacks[index].Xray.AccessLog = mode
	}
	for range 2 {
		require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error { return store.SaveDocument(ctx, q, doc) }))
		require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
			doc, err = store.LoadDocument(ctx, q)
			return err
		}))
		require.NotNil(t, doc.Settings.Defaults.Xray.AccessLog)
		require.False(t, *doc.Settings.Defaults.Xray.AccessLog)
		for index, mode := range []string{configdoc.Inherit, configdoc.On, configdoc.Off} {
			require.Equal(t, mode, doc.Stacks[index].Xray.AccessLog)
		}
	}
}

// TestLegacyAccessLogSettings 验证旧数据库的 defaults 分组缺少开关时保持开启。
func TestLegacyAccessLogSettings(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	require.NoError(t, store.PutJSON(ctx, st.Q, store.SettingDefaults, map[string]any{
		"xray": map[string]any{"loglevel": "error"},
	}))
	settings, err := store.LoadSettings(ctx, st.Q)
	require.NoError(t, err)
	require.NotNil(t, settings.Defaults.Xray.AccessLog)
	require.True(t, *settings.Defaults.Xray.AccessLog)
}
