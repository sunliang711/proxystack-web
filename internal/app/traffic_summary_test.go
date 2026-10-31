package app_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/traffic"
	"proxystack-web/internal/xrayapi"
)

// TestTrafficStackBreakdown 验证各时间粒度下的总和、用户档案合并和 stack 筛选使用同一统计口径。
func TestTrafficStackBreakdown(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stackID := doc.Stacks[doc.StackByName("usa1")].ID
	userID := h.alice(t).ID
	subject := strconv.FormatInt(userID, 10)
	const deletedStackID int64 = 999
	for table, bucket := range map[string]time.Time{
		"traffic_hour":  h.now.Truncate(time.Hour),
		"traffic_day":   traffic.DayStart(h.now, h.now.Location()),
		"traffic_month": traffic.MonthStart(h.now, h.now.Location()),
	} {
		for _, row := range []struct {
			stackID        int64
			scope, subject string
			up, down       int64
		}{
			{stackID, "user", subject, 10, 20},
			{stackID, "user", subject + ".7", 2, 3},
			{stackID, "user", "998", 50, 60},
			{deletedStackID, "user", subject, 7, 8},
			{deletedStackID, "user", "unknown", 11, 12},
			{stackID, "inbound", "shared-tag", 100, 200},
			{deletedStackID, "inbound", "shared-tag", 30, 40},
		} {
			_, err := h.svc.Store.DB.ExecContext(ctx, "INSERT INTO "+table+" (bucket_start, stack_id, scope, subject, up, down) VALUES (?, ?, ?, ?, ?, ?)", bucket.Unix(), row.stackID, row.scope, row.subject, row.up, row.down)
			require.NoError(t, err)
		}
	}
	for _, level := range []string{"hour", "day", "month", "year"} {
		for _, tc := range []struct {
			name, scope string
			subjects    []string
			stacks      []int64
			total       int64
			count       int
		}{
			{"all users", "user", nil, nil, 183, 2},
			{"one user and profiles", "user", []string{subject}, nil, 50, 2},
			{"one user one stack", "user", []string{subject}, []int64{stackID}, 35, 1},
			{"unknown user", "user", []string{"unknown"}, nil, 23, 1},
			{"same inbound tag", "inbound", nil, nil, 370, 2},
			{"no data", "user", []string{"997"}, nil, 0, 0},
		} {
			t.Run(level+"/"+tc.name, func(t *testing.T) {
				query := app.TrafficQuery{Level: level, Scope: tc.scope, Subjects: tc.subjects, Stacks: tc.stacks, GroupBy: "stack", From: h.now.AddDate(-1, 0, 0), To: h.now.AddDate(1, 0, 0)}
				result, err := h.svc.QueryTraffic(ctx, query)
				require.NoError(t, err)
				require.Equal(t, tc.total, result.Total.Total())
				require.Len(t, result.ByStack, tc.count)
				require.Len(t, result.Series, tc.count)
				var sum int64
				for _, series := range result.ByStack {
					sum += series.Total()
					if series.StackID == deletedStackID {
						require.Equal(t, "已删除 Stack #999", series.Label)
					}
				}
				require.Equal(t, result.Total.Total(), sum)
				csv, err := h.svc.TrafficCSV(ctx, result)
				require.NoError(t, err)
				if tc.count > 1 {
					require.Contains(t, string(csv), "已删除 Stack #999")
				}
			})
		}
	}
	for _, tc := range []struct{ userID, total int64 }{{0, 370}, {userID, 50}} {
		overview, err := h.svc.TrafficOverviewData(ctx, tc.userID)
		require.NoError(t, err)
		require.Equal(t, tc.total, overview.Today.Total())
		require.Equal(t, tc.total, overview.Month.Total())
		var today, month int64
		for _, stack := range overview.ByStack {
			today += stack.Today.Total()
			month += stack.Month.Total()
		}
		require.Equal(t, today, overview.Today.Total())
		require.Equal(t, month, overview.Month.Total())
	}
	_, err = h.svc.TrafficOverviewData(ctx, 997)
	require.ErrorIs(t, err, app.ErrNotFound)
}

// TestUserStackUsageIncludesPending 验证未落库的用户档案流量进入 stack 用量和实时速率。
func TestUserStackUsageIncludesPending(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	h.use(t, 100, 200)
	h.api.AddStats(usa1API,
		xrayapi.Stat{Name: "user>>>u1.p7>>>traffic>>>uplink", Value: 10},
		xrayapi.Stat{Name: "user>>>u1>>>traffic>>>downlink", Value: 15},
		xrayapi.Stat{Name: "inbound>>>vmess:24101:vmess>>>traffic>>>uplink", Value: 10},
		xrayapi.Stat{Name: "inbound>>>vmess:24101:vmess>>>traffic>>>downlink", Value: 15},
	)
	h.now = h.now.Add(5 * time.Second)
	h.svc.TrafficTick(ctx)
	usage, err := h.svc.UserUsageDetail(ctx, h.alice(t).ID)
	require.NoError(t, err)
	require.Len(t, usage.ByStack, 1)
	stack := usage.ByStack[0]
	require.Equal(t, "usa1", stack.Stack)
	require.Len(t, stack.Daily, 30)
	require.EqualValues(t, 325, stack.Today.Total())
	require.Equal(t, usage.Today, stack.Today)
	require.Equal(t, stack.Today, stack.Recent)
	require.Equal(t, usage.Daily[29], stack.Daily[29])
	overview, err := h.svc.TrafficOverviewData(ctx, h.alice(t).ID)
	require.NoError(t, err)
	require.Equal(t, usage.Today, overview.Today)
	point := overview.Live[len(overview.Live)-1].Users[h.alice(t).ID]
	require.EqualValues(t, 2, point.Up)
	require.EqualValues(t, 3, point.Down)
	require.Equal(t, point.Bytes, point.Stacks["usa1"])
}

// TestTrafficSubjectsIncludeUnusedUsers 验证没有流量记录的用户也能用于筛选。
func TestTrafficSubjectsIncludeUnusedUsers(t *testing.T) {
	h := newTrafficHarness(t)
	subjects, err := h.svc.TrafficSubjects(context.Background(), traffic.ScopeUser)
	require.NoError(t, err)
	require.Contains(t, subjects, app.TrafficSubject{Subject: strconv.FormatInt(h.alice(t).ID, 10), Label: "alice"})
}
