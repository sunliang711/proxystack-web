package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAccessLogMigration 验证已有数据库升级后旧 Stack 自动继承全局，原配置不变。
func TestAccessLogMigration(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	provider, err := newProvider(st.DB)
	require.NoError(t, err)
	_, err = provider.UpTo(ctx, 7)
	require.NoError(t, err)
	_, err = st.DB.ExecContext(ctx, `INSERT INTO stacks (
		position, name, enabled, desired_running, role, labels, xray_enabled, xray_loglevel,
		xray_api, xray_stats, xray_policy, xray_api_port, outbound_type, outbound_server,
		outbound_port, outbound_username, outbound_password, outbound_private_direct,
		mihomo_enabled, mihomo_mode, mihomo_loglevel, controller_port, controller_secret,
		rule_profile, rule_final, rule_extra
	) VALUES (0, 'edge', 1, 1, 'edge', '[]', 1, 'error', 'inherit', 'inherit', 'inherit',
		10001, 'direct', '', 0, '', '', 0, 0, 'inherit', 'inherit', 19000, '', 'default', 'DIRECT', '[]')`)
	require.NoError(t, err)
	require.NoError(t, st.Migrate(ctx))
	var access, level string
	require.NoError(t, st.DB.QueryRowContext(ctx, "SELECT xray_access_log, xray_loglevel FROM stacks WHERE name = ?", "edge").Scan(&access, &level))
	require.Equal(t, "inherit", access)
	require.Equal(t, "error", level)
}
