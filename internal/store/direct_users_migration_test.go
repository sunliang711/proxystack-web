package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDirectUsersMigration 验证第 8 版数据库升级时不为旧 Stack 隐式授予直连权限。
func TestDirectUsersMigration(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	provider, err := newProvider(st.DB)
	require.NoError(t, err)
	_, err = provider.UpTo(ctx, 8)
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
	users, err := st.Q.ListStackDirectUsers(ctx)
	require.NoError(t, err)
	require.Empty(t, users)
	stacks, err := st.Q.ListStacks(ctx)
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	require.Equal(t, "edge", stacks[0].Name)
	require.Equal(t, "error", stacks[0].XrayLoglevel)
}
