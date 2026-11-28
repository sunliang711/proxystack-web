package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/store/dbq"
)

// TestAccountsMigration 验证旧账号升级后的权限，以及备份恢复时的角色和状态保留。
func TestAccountsMigration(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	provider, err := newProvider(st.DB)
	require.NoError(t, err)
	_, err = provider.UpTo(ctx, 27)
	require.NoError(t, err)
	_, err = st.DB.ExecContext(ctx, "INSERT INTO admins (username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?)", "legacy", "private-hash", 1, 1)
	require.NoError(t, err)
	require.NoError(t, st.Migrate(ctx))
	admin, err := st.Q.GetAdminByUsername(ctx, "legacy")
	require.NoError(t, err)
	require.Equal(t, "admin", admin.Role)
	require.Zero(t, admin.Disabled)
	require.Zero(t, admin.LastLoginAt)
	admin.Role, admin.Disabled, admin.LastLoginAt = "viewer", 1, 123
	require.NoError(t, st.Q.DeleteAllAdmins(ctx))
	require.NoError(t, st.Q.RestoreAdmin(ctx, dbq.RestoreAdminParams(admin)))
	got, err := st.Q.GetAdminByID(ctx, admin.ID)
	require.NoError(t, err)
	require.Equal(t, admin, got)
}
