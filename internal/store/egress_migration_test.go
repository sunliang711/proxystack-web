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

// TestEgressRetriesMigration 验证升级默认计划、保留自定义设置及已确认故障，移除旧的跨周期计数。
func TestEgressRetriesMigration(t *testing.T) {
	for _, schedule := range []string{"* * * * *", "*/5 * * * *"} {
		t.Run(schedule, func(t *testing.T) {
			ctx := context.Background()
			st, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, st.Close()) })
			provider, err := newProvider(st.DB)
			require.NoError(t, err)
			_, err = provider.UpTo(ctx, 11)
			require.NoError(t, err)
			_, err = st.DB.ExecContext(ctx, `INSERT INTO jobs (kind,schedule,enabled,timeout,params) VALUES ('egress_check',?,0,240,'{"failure_threshold":4,"probe_timeout":20}')`, schedule)
			require.NoError(t, err)
			_, err = st.DB.ExecContext(ctx, `INSERT INTO jobs (kind,schedule,enabled,timeout,params) VALUES ('backup','0 4 * * *',1,600,'{"keep":7}')`)
			require.NoError(t, err)
			for _, name := range []string{"pending", "down"} {
				_, err = st.DB.ExecContext(ctx, `INSERT INTO stacks (
					position, name, enabled, desired_running, role, labels, xray_enabled, xray_loglevel,
					xray_api, xray_stats, xray_policy, xray_api_port, outbound_type, outbound_server,
					outbound_port, outbound_username, outbound_password, outbound_private_direct,
					mihomo_enabled, mihomo_mode, mihomo_loglevel, controller_port, controller_secret,
					rule_profile, rule_final, rule_extra
				) VALUES (0, ?, 1, 1, 'edge', '[]', 1, 'error', 'inherit', 'inherit', 'inherit',
					10001, 'direct', '', 0, '', '', 0, 0, 'inherit', 'inherit', 19000, '', 'default', 'DIRECT', '[]')`, name)
				require.NoError(t, err)
			}
			_, err = st.DB.ExecContext(ctx, `INSERT INTO stack_egress_health (stack_id,status,failures,failure_since,incident,alarmed) VALUES (1,'failing',1,100,0,0),(2,'down',4,200,3,1)`)
			require.NoError(t, err)
			require.NoError(t, st.Migrate(ctx))
			jobs, err := st.Q.ListJobs(ctx)
			require.NoError(t, err)
			require.Len(t, jobs, 2)
			expected := schedule
			if schedule == "* * * * *" {
				expected = "*/2 * * * *"
			}
			require.Equal(t, expected, jobs[0].Schedule)
			require.Zero(t, jobs[0].Enabled)
			require.EqualValues(t, 240, jobs[0].Timeout)
			require.JSONEq(t, `{"retry_count":3,"probe_timeout":20}`, jobs[0].Params)
			require.Equal(t, "0 4 * * *", jobs[1].Schedule)
			require.JSONEq(t, `{"keep":7}`, jobs[1].Params)
			rows, err := st.Q.ListStackEgressHealth(ctx)
			require.NoError(t, err)
			require.Len(t, rows, 2)
			require.Equal(t, "unknown", rows[0].Status)
			require.Zero(t, rows[0].Failures)
			require.Zero(t, rows[0].FailureSince)
			require.Equal(t, "down", rows[1].Status)
			require.Zero(t, rows[1].Failures)
			require.EqualValues(t, 200, rows[1].FailureSince)
			require.EqualValues(t, 3, rows[1].Incident)
			require.EqualValues(t, 1, rows[1].Alarmed)
		})
	}
}
