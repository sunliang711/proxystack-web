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

// TestStackUsageScopes 验证入站总量、用户档案合并、Stack 隔离及自然日和自然月边界。
func TestStackUsageScopes(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	h.now = time.Date(2026, 12, 31, 12, 0, 0, 0, h.now.Location())
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stackID := doc.Stacks[doc.StackByName("usa1")].ID
	otherID := doc.Stacks[doc.StackByName("usa2")].ID
	userID := h.alice(t).ID
	subject := strconv.FormatInt(userID, 10)
	day := traffic.DayStart(h.now, h.now.Location())
	for _, period := range []struct {
		table  string
		bucket time.Time
		factor int64
	}{
		{"traffic_day", day, 1},
		{"traffic_day", day.AddDate(0, 0, -29), 2},
		{"traffic_day", day.AddDate(0, 0, -30), 4},
		{"traffic_month", traffic.MonthStart(h.now, h.now.Location()), 8},
		{"traffic_month", traffic.MonthStart(h.now, h.now.Location()).AddDate(0, -1, 0), 16},
	} {
		for _, row := range []struct {
			stackID        int64
			scope, subject string
			up, down       int64
		}{
			{stackID, "user", subject, 10, 20},
			{stackID, "user", subject + ".7", 2, 3},
			{stackID, "user", "999", 50, 60},
			{stackID, "inbound", "vmess", 100, 200},
			{stackID, "inbound", "shared", 5, 7},
			{otherID, "user", subject, 900, 900},
			{otherID, "inbound", "vmess", 900, 900},
		} {
			_, err := h.svc.Store.DB.ExecContext(ctx, "INSERT INTO "+period.table+" (bucket_start, stack_id, scope, subject, up, down) VALUES (?, ?, ?, ?, ?, ?)", period.bucket.Unix(), row.stackID, row.scope, row.subject, row.up*period.factor, row.down*period.factor)
			require.NoError(t, err)
		}
	}
	for _, tc := range []struct {
		name        string
		userID, sum int64
	}{
		{"all inbound traffic", 0, 312},
		{"one user with profiles", userID, 35},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage, err := h.svc.StackUsageDetail(ctx, stackID, tc.userID)
			require.NoError(t, err)
			require.Equal(t, stackID, usage.StackID)
			require.Equal(t, "usa1", usage.Stack)
			require.Equal(t, tc.sum, usage.Today.Total())
			require.Equal(t, tc.sum*8, usage.Month.Total())
			require.Equal(t, tc.sum*3, usage.Recent.Total())
			require.Len(t, usage.Daily, 30)
			require.Equal(t, "2026-12-02", usage.Daily[0].Day)
			require.Equal(t, tc.sum*2, usage.Daily[0].Total())
			require.Equal(t, "2026-12-31", usage.Daily[29].Day)
			require.Equal(t, usage.Today, usage.Daily[29].Bytes)
			require.Zero(t, usage.Daily[1].Total())
		})
	}
	// 空 Stack 仍返回完整的零值日序列，未知实体应明确返回不存在。
	_, err = h.svc.Store.DB.ExecContext(ctx, "DELETE FROM traffic_day WHERE stack_id = ?", otherID)
	require.NoError(t, err)
	_, err = h.svc.Store.DB.ExecContext(ctx, "DELETE FROM traffic_month WHERE stack_id = ?", otherID)
	require.NoError(t, err)
	empty, err := h.svc.StackUsageDetail(ctx, otherID, userID)
	require.NoError(t, err)
	require.Len(t, empty.Daily, 30)
	require.Zero(t, empty.Today.Total()+empty.Month.Total()+empty.Recent.Total())
	require.Equal(t, otherID, empty.Collection.StackID)
	_, err = h.svc.StackUsageDetail(ctx, 9999, 0)
	require.ErrorIs(t, err, app.ErrNotFound)
	_, err = h.svc.StackUsageDetail(ctx, stackID, 9999)
	require.ErrorIs(t, err, app.ErrNotFound)
}

// TestStackUsagePending 验证未落库增量只计入当前 Stack，落库前后统计保持一致。
func TestStackUsagePending(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	h.use(t, 100, 200)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stackID := doc.Stacks[doc.StackByName("usa1")].ID
	h.api.AddStats(usa1API,
		xrayapi.Stat{Name: "user>>>u1.p7>>>traffic>>>uplink", Value: 10},
		xrayapi.Stat{Name: "user>>>u1>>>traffic>>>downlink", Value: 15},
		xrayapi.Stat{Name: "inbound>>>vmess:24101:vmess>>>traffic>>>uplink", Value: 10},
		xrayapi.Stat{Name: "inbound>>>vmess:24101:vmess>>>traffic>>>downlink", Value: 15},
	)
	h.now = h.now.Add(5 * time.Second)
	h.svc.TrafficTick(ctx)
	before := map[int64]*app.StackUsage{}
	for _, userID := range []int64{0, h.alice(t).ID} {
		usage, err := h.svc.StackUsageDetail(ctx, stackID, userID)
		require.NoError(t, err)
		require.EqualValues(t, 325, usage.Today.Total())
		require.Equal(t, usage.Today, usage.Month)
		require.Equal(t, usage.Today, usage.Recent)
		require.False(t, usage.Collection.LastAt.IsZero())
		before[userID] = usage
	}
	h.now = h.now.Add(time.Minute)
	h.svc.TrafficTick(ctx)
	for userID, usage := range before {
		after, err := h.svc.StackUsageDetail(ctx, stackID, userID)
		require.NoError(t, err)
		require.Equal(t, usage.Today, after.Today)
		require.Equal(t, usage.Month, after.Month)
		require.Equal(t, usage.Daily, after.Daily)
	}
}
