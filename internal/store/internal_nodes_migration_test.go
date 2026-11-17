package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInternalNodesMigration verifies that existing users remain unauthorized after upgrading from schema 23.
func TestInternalNodesMigration(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	provider, err := newProvider(st.DB)
	require.NoError(t, err)
	_, err = provider.UpTo(ctx, 23)
	require.NoError(t, err)
	_, err = st.DB.ExecContext(ctx, `INSERT INTO users (
		position, name, enabled, uuid, ss_password, remark, display_template, tag, sub_token,
		quota_bytes, reset_mode, reset_day, expire_at, note, daily_limit_bytes, daily_limit_action,
		pause_window_mode, pause_windows, blocked_domains
	) VALUES (0, 'alice', 1, '11111111-1111-4111-8111-111111111111', 'secret', '', '', '', 'token',
		0, 'none', 0, '', '', 0, 'pause', 'inherit', '', '')`)
	require.NoError(t, err)

	require.NoError(t, st.Migrate(ctx))
	users, err := st.Q.ListUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Zero(t, users[0].InternalNodes)
	_, err = st.DB.ExecContext(ctx, "UPDATE users SET internal_nodes = 1 WHERE name = ?", "alice")
	require.NoError(t, err)
	users, err = st.Q.ListUsers(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, users[0].InternalNodes)
}
