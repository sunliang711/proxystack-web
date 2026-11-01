package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEgressMigration 验证第 10 版数据库升级保留原通知重试信息，不伪造出口状态和故障。
func TestEgressMigration(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	provider, err := newProvider(st.DB)
	require.NoError(t, err)
	_, err = provider.UpTo(ctx, 10)
	require.NoError(t, err)
	_, err = st.DB.ExecContext(ctx, `INSERT INTO notifications (at,event,user_id,user_name,dedup_key,subject,body,status,error,attempts,next_attempt_at)
		VALUES (1,'daily_limit',7,'alice','old-notification','subject','body','pending','SMTP unavailable',2,100)`)
	require.NoError(t, err)
	require.NoError(t, st.Migrate(ctx))
	rows, err := st.Q.ListNotifications(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 7, rows[0].UserID)
	require.EqualValues(t, 2, rows[0].Attempts)
	require.EqualValues(t, 100, rows[0].NextAttemptAt)
	require.Zero(t, rows[0].StackID)
	require.Empty(t, rows[0].StackName)
	health, err := st.Q.ListStackEgressHealth(ctx)
	require.NoError(t, err)
	require.Empty(t, health)
}
