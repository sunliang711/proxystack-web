package app_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
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
	for _, tc := range []struct{ userID, total, userTotal int64 }{{0, 370, 160}, {userID, 50, 50}} {
		overview, err := h.svc.TrafficOverviewData(ctx, tc.userID)
		require.NoError(t, err)
		require.Equal(t, tc.total, overview.Today.Total())
		require.Equal(t, tc.total, overview.Month.Total())
		require.Equal(t, tc.userTotal, overview.UserToday.Total())
		require.Equal(t, tc.userTotal, overview.UserMonth.Total())
		var today, month, userToday, userMonth int64
		for _, stack := range overview.ByStack {
			today += stack.Today.Total()
			month += stack.Month.Total()
			userToday += stack.UserToday.Total()
			userMonth += stack.UserMonth.Total()
		}
		require.Equal(t, today, overview.Today.Total())
		require.Equal(t, month, overview.Month.Total())
		require.Equal(t, userToday, overview.UserToday.Total())
		require.Equal(t, userMonth, overview.UserMonth.Total())
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
	require.Equal(t, usage.Today, overview.UserToday)
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

// 今日和本月独立排名，日边界采用系统时区，并合并用户的凭据档案。
func TestTrafficOverviewDailyAndMonthlyRankings(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	h.now = time.Date(2026, 11, 10, 23, 30, 0, 0, h.now.Location())
	h.svc.Now = func() time.Time { return h.now.UTC() }
	alice := h.alice(t)
	bob, err := h.svc.CreateUser(ctx, h.actor, configdoc.User{Name: "bob", Enabled: true, ResetMode: "none", Profiles: []configdoc.Profile{}})
	require.NoError(t, err)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stackID := doc.Stacks[doc.StackByName("usa1")].ID
	day := traffic.DayStart(h.now, h.now.Location())
	month := traffic.MonthStart(h.now, h.now.Location())
	insert := func(table string, bucket time.Time, subject string, up, down int64) {
		t.Helper()
		_, err := h.svc.Store.DB.ExecContext(ctx, "INSERT INTO "+table+" (bucket_start, stack_id, scope, subject, up, down) VALUES (?, ?, 'user', ?, ?, ?)", bucket.Unix(), stackID, subject, up, down)
		require.NoError(t, err)
	}
	before, err := h.svc.TrafficOverviewData(ctx, 0)
	require.NoError(t, err)
	require.NotNil(t, before.TopUsersToday)
	require.Empty(t, before.TopUsersToday)
	aliceSubject, bobSubject := strconv.FormatInt(alice.ID, 10), strconv.FormatInt(bob.ID, 10)
	insert("traffic_day", day, aliceSubject, 40, 60)
	insert("traffic_day", day, aliceSubject+".7", 5, 5)
	insert("traffic_day", day, bobSubject, 100, 300)
	insert("traffic_day", day.AddDate(0, 0, -1), aliceSubject, 500, 500)
	insert("traffic_day", day.AddDate(0, 0, 1), bobSubject, 9999, 9999)
	insert("traffic_month", month, aliceSubject, 540, 560)
	insert("traffic_month", month, aliceSubject+".7", 5, 5)
	insert("traffic_month", month, bobSubject, 100, 300)
	insert("traffic_month", month.AddDate(0, -1, 0), bobSubject, 9999, 9999)
	insert("traffic_month", month.AddDate(0, 1, 0), bobSubject, 9999, 9999)
	for _, userID := range []int64{0, alice.ID} {
		overview, err := h.svc.TrafficOverviewData(ctx, userID)
		require.NoError(t, err)
		if userID == 0 {
			require.Len(t, overview.TopUsersToday, 2)
			require.Equal(t, bob.ID, overview.TopUsersToday[0].ID)
			require.EqualValues(t, 400, overview.TopUsersToday[0].Total())
			require.Equal(t, alice.ID, overview.TopUsers[0].ID)
			require.EqualValues(t, 510, overview.UserToday.Total())
			require.EqualValues(t, 1510, overview.UserMonth.Total())
		} else {
			require.Len(t, overview.TopUsersToday, 1)
			require.Len(t, overview.TopUsers, 1)
			require.Equal(t, overview.Today, overview.UserToday)
			require.Equal(t, overview.Month, overview.UserMonth)
		}
		for _, user := range overview.TopUsersToday {
			if user.ID == alice.ID {
				require.EqualValues(t, 110, user.Total())
			}
		}
		require.EqualValues(t, 1110, overview.TopUsers[0].Total())
	}
	// 本月前十没有今日第一名时，今日排行也必须保留该用户。
	for id := int64(1000); id < 1011; id++ {
		insert("traffic_month", month, strconv.FormatInt(id, 10), 10000, 0)
	}
	overview, err := h.svc.TrafficOverviewData(ctx, 0)
	require.NoError(t, err)
	require.Len(t, overview.TopUsers, 10)
	require.EqualValues(t, 1000, overview.TopUsers[0].ID)
	require.Equal(t, bob.ID, overview.TopUsersToday[0].ID)
}

// 聚合 stack 只经成员的 relay 出网，同一份流量会在成员 stack 的入站再计一次：
// 多个 stack 加总入站和出站时排除它，单个 stack 的数字和用户维度保持原样。
func TestTrafficTotalsSkipAggregatorStacks(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	autoID := doc.Stacks[doc.StackByName("auto")].ID
	edgeID := doc.Stacks[doc.StackByName("usa1")].ID
	subject := strconv.FormatInt(h.alice(t).ID, 10)
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
			// 客户端经聚合 stack 进入，再经 usa1 的 relay 出去：入站各计一次，用户只在入口计一次。
			{autoID, "inbound", "vmess", 100, 200},
			{autoID, "user", subject, 100, 200},
			{edgeID, "inbound", "relay", 100, 200},
			// usa1 自己的直连用户。
			{edgeID, "inbound", "vmess", 10, 20},
			{edgeID, "user", subject, 10, 20},
		} {
			_, err := h.svc.Store.DB.ExecContext(ctx, "INSERT INTO "+table+" (bucket_start, stack_id, scope, subject, up, down) VALUES (?, ?, ?, ?, ?, ?)", bucket.Unix(), row.stackID, row.scope, row.subject, row.up, row.down)
			require.NoError(t, err)
		}
	}

	overview, err := h.svc.TrafficOverviewData(ctx, 0)
	require.NoError(t, err)
	require.EqualValues(t, 330, overview.Today.Total())
	require.EqualValues(t, 330, overview.Month.Total())
	require.EqualValues(t, 330, overview.UserToday.Total())
	for _, stack := range overview.ByStack {
		switch stack.StackID {
		case autoID:
			require.True(t, stack.Aggregator)
			require.EqualValues(t, 300, stack.Today.Total(), "单个 stack 的数字不变")
		case edgeID:
			require.False(t, stack.Aggregator)
			require.EqualValues(t, 330, stack.Today.Total())
		}
	}

	query := app.TrafficQuery{Level: "day", GroupBy: "stack", From: h.now.AddDate(0, 0, -1), To: h.now.AddDate(0, 0, 1)}
	query.Scope = "inbound"
	result, err := h.svc.QueryTraffic(ctx, query)
	require.NoError(t, err)
	require.EqualValues(t, 330, result.Total.Total())
	require.Len(t, result.ByStack, 2)
	query.Scope = "user"
	result, err = h.svc.QueryTraffic(ctx, query)
	require.NoError(t, err)
	require.EqualValues(t, 330, result.Total.Total(), "用户维度不排除聚合 stack")
}

// 实时总速率同样不重复相加聚合 stack，各 stack 自己的速率照常保留。
func TestLiveRateSkipsAggregatorStacks(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	h.svc.TrafficTick(ctx)
	h.api.AddStats("127.0.0.1:10150",
		xrayapi.Stat{Name: "inbound>>>vmess>>>traffic>>>uplink", Value: 600},
		xrayapi.Stat{Name: "inbound>>>vmess>>>traffic>>>downlink", Value: 1200},
	)
	h.api.AddStats(usa1API,
		xrayapi.Stat{Name: "inbound>>>relay>>>traffic>>>uplink", Value: 600},
		xrayapi.Stat{Name: "inbound>>>relay>>>traffic>>>downlink", Value: 1200},
	)
	h.now = h.now.Add(time.Minute)
	h.svc.TrafficTick(ctx)
	overview, err := h.svc.TrafficOverviewData(ctx, 0)
	require.NoError(t, err)
	require.NotEmpty(t, overview.Live)
	point := overview.Live[len(overview.Live)-1]
	require.EqualValues(t, 10, point.Up)
	require.EqualValues(t, 20, point.Down)
	require.EqualValues(t, 10, point.Stacks["auto"].Up)
	require.EqualValues(t, 10, point.Stacks["usa1"].Up)
}
