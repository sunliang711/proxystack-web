package app_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/traffic"
	"proxystack-web/internal/units"
	"proxystack-web/internal/xrayapi"
)

type trafficRow struct {
	stackID        int64
	scope, subject string
	up, down       int64
}

func (h *trafficHarness) insertTraffic(t *testing.T, table string, bucket time.Time, rows ...trafficRow) {
	t.Helper()
	for _, row := range rows {
		_, err := h.svc.Store.DB.ExecContext(context.Background(), "INSERT INTO "+table+" (bucket_start, stack_id, scope, subject, up, down) VALUES (?, ?, ?, ?, ?, ?)",
			bucket.Unix(), row.stackID, row.scope, row.subject, row.up, row.down)
		require.NoError(t, err)
	}
}

// insertAll 把同一组明细写入当前小时、当天和当月的汇总表。
func (h *trafficHarness) insertAll(t *testing.T, rows ...trafficRow) {
	t.Helper()
	loc := h.now.Location()
	h.insertTraffic(t, "traffic_hour", h.now.Truncate(time.Hour), rows...)
	h.insertTraffic(t, "traffic_day", traffic.DayStart(h.now, loc), rows...)
	h.insertTraffic(t, "traffic_month", traffic.MonthStart(h.now, loc), rows...)
}

func (h *trafficHarness) stackID(t *testing.T, name string) int64 {
	t.Helper()
	doc, err := h.svc.LoadConfig(context.Background())
	require.NoError(t, err)
	return doc.Stacks[doc.StackByName(name)].ID
}

func findRow(rows []app.TrafficStatsRow, kind string, id int64) *app.TrafficStatsRow {
	for index := range rows {
		if rows[index].Kind == kind && rows[index].ID == id {
			return &rows[index]
		}
	}
	return nil
}

func sum(values []int64) int64 {
	var out int64
	for _, value := range values {
		out += value
	}
	return out
}

// TestTrafficStatsUserView 验证按用户统计在各粒度下补齐时间桶、合并档案、展开各 stack，并保留已删除对象的名字。
func TestTrafficStatsUserView(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	usa1, usa2 := h.stackID(t, "usa1"), h.stackID(t, "usa2")
	alice := h.alice(t).ID
	subject := strconv.FormatInt(alice, 10)
	_, err := h.svc.Store.DB.ExecContext(ctx, "INSERT INTO traffic_names (kind, id, name, role) VALUES ('user', 998, 'carol', '')")
	require.NoError(t, err)
	h.insertAll(t,
		trafficRow{usa1, "user", subject, 10, 20},
		trafficRow{usa1, "user", subject + ".7", 2, 3},
		trafficRow{usa2, "user", subject, 1, 1},
		trafficRow{usa1, "user", "998", 50, 60},
		trafficRow{999, "user", subject, 7, 8},
		trafficRow{999, "user", "unknown", 11, 12},
		trafficRow{usa1, "inbound", "vmess", 100, 200},
	)
	loc := h.now.Location()
	day, month := traffic.DayStart(h.now, loc), traffic.MonthStart(h.now, loc)
	year := time.Date(h.now.Year(), 1, 1, 0, 0, 0, 0, loc)
	for _, tc := range []struct {
		level    string
		from, to time.Time
		buckets  int
	}{
		{"hour", day, day.AddDate(0, 0, 1), 24},
		{"day", month, month.AddDate(0, 1, 0), 30},
		{"month", year, year.AddDate(1, 0, 0), 12},
		{"year", year, year.AddDate(1, 0, 0), 1},
	} {
		t.Run(tc.level, func(t *testing.T) {
			result, err := h.svc.TrafficStats(ctx, app.TrafficStatsQuery{View: app.StatsViewUser, Level: tc.level, From: tc.from, To: tc.to})
			require.NoError(t, err)
			require.Len(t, result.Buckets, tc.buckets)
			require.EqualValues(t, 185, result.Total.Total())
			require.Len(t, result.Total.SeriesUp, tc.buckets)
			require.EqualValues(t, 185, sum(result.Total.SeriesUp)+sum(result.Total.SeriesDown))
			require.Len(t, result.Rows, 3)
			row := findRow(result.Rows, app.StatsRowUser, alice)
			require.NotNil(t, row)
			require.EqualValues(t, 52, row.Total())
			require.False(t, row.Deleted)
			require.EqualValues(t, 35, findRow(row.Children, app.StatsRowStack, usa1).Total())
			require.EqualValues(t, 2, findRow(row.Children, app.StatsRowStack, usa2).Total())
			deletedStack := findRow(row.Children, app.StatsRowStack, 999)
			require.Equal(t, "Stack #999", deletedStack.Name)
			require.True(t, deletedStack.Deleted)
			require.Nil(t, deletedStack.SeriesUp, "多个用户时子行不带趋势")
			require.Len(t, row.Profiles, 2)
			require.EqualValues(t, 47, findRow(row.Profiles, app.StatsRowProfile, 0).Total())
			require.Equal(t, "档案 #7", findRow(row.Profiles, app.StatsRowProfile, 7).Name)
			carol := findRow(result.Rows, app.StatsRowUser, 998)
			require.Equal(t, "carol", carol.Name)
			require.True(t, carol.Deleted)
			require.EqualValues(t, 23, findRow(result.Rows, app.StatsRowUnknown, 0).Total())
		})
	}
	one, err := h.svc.TrafficStats(ctx, app.TrafficStatsQuery{View: app.StatsViewUser, Level: "day", From: month, To: month.AddDate(0, 1, 0), Users: []int64{alice}})
	require.NoError(t, err)
	require.True(t, one.SeriesDetail)
	require.Len(t, one.Rows, 1)
	require.EqualValues(t, 52, one.Total.Total())
	require.Len(t, one.Rows[0].Children[0].SeriesUp, 30)
	filtered, err := h.svc.TrafficStats(ctx, app.TrafficStatsQuery{View: app.StatsViewUser, Level: "day", From: month, To: month.AddDate(0, 1, 0), Stacks: []int64{usa1}})
	require.NoError(t, err)
	require.EqualValues(t, 145, filtered.Total.Total())
	require.Nil(t, findRow(filtered.Rows, app.StatsRowUnknown, 0))
	csv := string(h.svc.TrafficStatsCSV(filtered))
	require.Contains(t, csv, "carol（已删除）")
	require.Contains(t, csv, "合计")
}

// auto stack 的流量经 edge stack 的 relay 入站出去，会在 edge stack 再计一次：按 stack 统计的合计排除 auto stack，
// edge stack 未归属到用户的部分拆成 relay 转入和共享账号；按用户统计或筛选用户时没有重复，不排除。
func TestTrafficStatsStackViewSkipsAuto(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	auto, usa1 := h.stackID(t, "auto"), h.stackID(t, "usa1")
	alice := h.alice(t).ID
	subject := strconv.FormatInt(alice, 10)
	h.insertAll(t,
		trafficRow{auto, "inbound", "vmess", 100, 200},
		trafficRow{auto, "user", subject, 100, 200},
		trafficRow{usa1, "inbound", "socks:24001:relay", 100, 200},
		trafficRow{usa1, "inbound", "vmess", 10, 20},
		trafficRow{usa1, "user", subject, 10, 20},
		trafficRow{usa1, "inbound", "http:24003:shared", 5, 5},
	)
	day := traffic.DayStart(h.now, h.now.Location())
	query := app.TrafficStatsQuery{View: app.StatsViewStack, Level: "hour", From: day, To: day.AddDate(0, 0, 1)}
	result, err := h.svc.TrafficStats(ctx, query)
	require.NoError(t, err)
	require.EqualValues(t, 340, result.Total.Total())
	autoRow := findRow(result.Rows, app.StatsRowStack, auto)
	require.True(t, autoRow.Excluded)
	require.Equal(t, "auto", autoRow.Role)
	require.EqualValues(t, 300, autoRow.Total())
	require.Len(t, autoRow.Children, 1, "auto stack 的入站全部归属到用户")
	edge := findRow(result.Rows, app.StatsRowStack, usa1)
	require.False(t, edge.Excluded)
	require.EqualValues(t, 340, edge.Total())
	require.EqualValues(t, 30, findRow(edge.Children, app.StatsRowUser, alice).Total())
	require.EqualValues(t, 300, findRow(edge.Children, app.StatsRowRelay, 0).Total())
	require.EqualValues(t, 10, findRow(edge.Children, app.StatsRowShared, 0).Total())
	require.NotNil(t, findRow(result.Rows, app.StatsRowStack, h.stackID(t, "usa2")), "零用量的 stack 也列出")

	query.Users = []int64{alice}
	result, err = h.svc.TrafficStats(ctx, query)
	require.NoError(t, err)
	require.EqualValues(t, 330, result.Total.Total())
	require.False(t, findRow(result.Rows, app.StatsRowStack, auto).Excluded)

	result, err = h.svc.TrafficStats(ctx, app.TrafficStatsQuery{View: app.StatsViewUser, Level: "day", From: day, To: day.AddDate(0, 0, 1)})
	require.NoError(t, err)
	require.EqualValues(t, 330, result.Total.Total())

	overview, err := h.svc.TrafficOverviewData(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 340, overview.Today.Total.Total())
	require.EqualValues(t, 330, overview.Today.Users.Total())
	require.EqualValues(t, 340, overview.Month.Total.Total())
	for _, stack := range overview.ByStack {
		switch stack.StackID {
		case auto:
			require.True(t, stack.Excluded)
			require.EqualValues(t, 300, stack.Today.Total.Total())
			require.EqualValues(t, 0, stack.Today.Relay.Total()+stack.Today.Shared.Total())
		case usa1:
			require.EqualValues(t, 300, stack.Today.Relay.Total())
			require.EqualValues(t, 10, stack.Today.Shared.Total())
			require.EqualValues(t, 30, stack.Today.Users.Total())
		}
	}
	require.Len(t, overview.TopToday, 1)
	require.EqualValues(t, 330, overview.TopToday[0].Total())

	usage, err := h.svc.StackUsageDetail(ctx, usa1, 0)
	require.NoError(t, err)
	require.Len(t, usage.Unattributed, 2)
	require.Equal(t, app.StatsRowRelay, usage.Unattributed[0].Kind)
	require.EqualValues(t, 300, usage.Unattributed[0].Today.Total())
	require.EqualValues(t, 300, usage.Unattributed[0].Month.Total())
	require.EqualValues(t, 10, usage.Unattributed[1].Recent.Total())
}

// 小时明细超过保留期时给出可用起点；对比上一期时当前周期只取同等已过时长；峰值取单分钟最大值。
func TestTrafficStatsRetentionCompareAndPeak(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	usa1 := h.stackID(t, "usa1")
	loc := h.now.Location()
	day := traffic.DayStart(h.now, loc)

	old, err := h.svc.TrafficStats(ctx, app.TrafficStatsQuery{View: app.StatsViewStack, Level: "hour", From: day.AddDate(0, 0, -200), To: day})
	require.NoError(t, err)
	require.Equal(t, 180, old.RetentionDays)
	require.True(t, old.AvailableFrom.After(day.AddDate(0, 0, -181)))
	require.False(t, old.PeakAvailable)
	_, err = h.svc.TrafficStats(ctx, app.TrafficStatsQuery{View: app.StatsViewStack, Level: "hour", From: day.AddDate(-2, 0, 0), To: day})
	var field *app.FieldError
	require.True(t, errors.As(err, &field))

	yesterday := day.AddDate(0, 0, -1)
	// 上一期按小时明细取整点部分，最后不足一小时的部分取分钟明细。
	h.insertTraffic(t, "traffic_hour", yesterday.Add(time.Hour), trafficRow{usa1, "inbound", "vmess", 20, 40})
	h.insertTraffic(t, "traffic_minute", yesterday.Add(12*time.Hour), trafficRow{usa1, "inbound", "vmess", 3, 7})
	h.insertTraffic(t, "traffic_minute", yesterday.Add(20*time.Hour), trafficRow{usa1, "inbound", "vmess", 500, 500})
	h.insertTraffic(t, "traffic_hour", yesterday.Add(20*time.Hour), trafficRow{usa1, "inbound", "vmess", 500, 500})
	h.insertTraffic(t, "traffic_minute", day.Add(10*time.Hour), trafficRow{usa1, "inbound", "vmess", 1000, 5000})
	h.insertTraffic(t, "traffic_minute", day.Add(10*time.Hour+time.Minute), trafficRow{usa1, "inbound", "vmess", 600, 600})
	h.insertTraffic(t, "traffic_day", day, trafficRow{usa1, "inbound", "vmess", 1600, 5600})
	result, err := h.svc.TrafficStats(ctx, app.TrafficStatsQuery{View: app.StatsViewStack, Level: "day", From: day, To: day.AddDate(0, 0, 1), Compare: true})
	require.NoError(t, err)
	require.True(t, result.AvailableFrom.IsZero())
	require.Equal(t, yesterday, result.PreviousFrom.In(loc))
	require.Equal(t, yesterday.Add(h.now.Sub(day)), result.PreviousTo.In(loc))
	require.NotNil(t, result.Total.Previous)
	require.EqualValues(t, 70, result.Total.Previous.Total())
	require.True(t, result.PeakAvailable)
	edge := findRow(result.Rows, app.StatsRowStack, usa1)
	require.EqualValues(t, 100, *edge.Peak)
	require.EqualValues(t, 100, *result.Total.Peak)

	overview, err := h.svc.TrafficOverviewData(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 70, overview.Today.Previous.Total())
	require.EqualValues(t, 7200, overview.Today.Total.Total())

	// 本年与去年同期对比：整月取月明细，其余整天取日明细，不依赖已过保留期的小时明细。
	year := time.Date(h.now.Year(), 1, 1, 0, 0, 0, 0, loc)
	lastYear := year.AddDate(-1, 0, 0)
	h.insertTraffic(t, "traffic_month", lastYear, trafficRow{usa1, "inbound", "vmess", 100, 100})
	h.insertTraffic(t, "traffic_day", time.Date(lastYear.Year(), h.now.Month(), 5, 0, 0, 0, 0, loc), trafficRow{usa1, "inbound", "vmess", 1, 1})
	h.insertTraffic(t, "traffic_day", time.Date(lastYear.Year(), h.now.Month(), 20, 0, 0, 0, 0, loc), trafficRow{usa1, "inbound", "vmess", 999, 999})
	yearly, err := h.svc.TrafficStats(ctx, app.TrafficStatsQuery{View: app.StatsViewStack, Level: "year", From: year, To: year.AddDate(1, 0, 0), Compare: true})
	require.NoError(t, err)
	require.EqualValues(t, 202, yearly.Total.Previous.Total())
}

// 排行列出全部现有用户（含零用量）和有用量的已删除用户，可指定 stack，并对比上一期。
func TestTrafficRanking(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	usa1, usa2 := h.stackID(t, "usa1"), h.stackID(t, "usa2")
	alice := h.alice(t).ID
	bob, err := h.svc.CreateUser(ctx, h.actor, configdoc.User{Name: "bob", Enabled: true, ResetMode: "none", Profiles: []configdoc.Profile{}})
	require.NoError(t, err)
	_, err = h.svc.Store.DB.ExecContext(ctx, "INSERT INTO traffic_names (kind, id, name, role) VALUES ('user', 998, 'carol', '')")
	require.NoError(t, err)
	day := traffic.DayStart(h.now, h.now.Location())
	subject := strconv.FormatInt(alice, 10)
	h.insertTraffic(t, "traffic_day", day,
		trafficRow{usa1, "user", subject, 10, 20},
		trafficRow{usa2, "user", subject + ".3", 100, 200},
		trafficRow{usa1, "user", "998", 1, 1},
		trafficRow{usa1, "user", "unknown", 50, 50},
	)
	h.insertTraffic(t, "traffic_day", day.AddDate(0, 0, -1), trafficRow{usa1, "user", strconv.FormatInt(bob.ID, 10), 7, 0})
	result, err := h.svc.TrafficRanking(ctx, app.TrafficRankingQuery{From: day, To: day.AddDate(0, 0, 1)})
	require.NoError(t, err)
	require.Len(t, result.Rows, 3)
	require.Equal(t, alice, result.Rows[0].UserID)
	require.EqualValues(t, 330, result.Rows[0].Total())
	require.Equal(t, "carol", result.Rows[1].Name)
	require.True(t, result.Rows[1].Deleted)
	require.Equal(t, bob.ID, result.Rows[2].UserID)
	require.EqualValues(t, 0, result.Rows[2].Total())
	require.EqualValues(t, 332, result.Total.Total())

	result, err = h.svc.TrafficRanking(ctx, app.TrafficRankingQuery{From: day.AddDate(0, 0, -1), To: day.AddDate(0, 0, 1), Stack: usa1, Compare: true})
	require.NoError(t, err)
	require.EqualValues(t, 30, result.Rows[0].Total())
	require.Equal(t, day.AddDate(0, 0, -3), result.PreviousFrom.In(h.now.Location()))
	require.NotNil(t, result.Previous)
	_, err = h.svc.TrafficRanking(ctx, app.TrafficRankingQuery{From: day, To: day})
	require.Error(t, err)
}

// 读取计数失败时记录延后缺口并在恢复后结束；xray 在两次采集之间重启时记录丢失缺口。
func TestTrafficGaps(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	usa1 := h.stackID(t, "usa1")
	h.svc.TrafficTick(ctx)
	h.api.Fail = errors.New("connection refused")
	h.now = h.now.Add(30 * time.Second)
	h.svc.TrafficTick(ctx)
	h.api.Fail = nil
	h.now = h.now.Add(30 * time.Second)
	h.svc.TrafficTick(ctx)
	restart := h.now.Add(20 * time.Second)
	h.units.SetSince(units.Name(units.Xray, "usa1"), restart)
	h.now = h.now.Add(30 * time.Second)
	h.svc.TrafficTick(ctx)

	day := traffic.DayStart(h.now, h.now.Location())
	result, err := h.svc.TrafficStats(ctx, app.TrafficStatsQuery{View: app.StatsViewStack, Level: "hour", From: day, To: day.AddDate(0, 0, 1)})
	require.NoError(t, err)
	var delayed, lost []app.TrafficGap
	for _, gap := range result.Gaps {
		if gap.Kind == app.GapDelayed {
			delayed = append(delayed, gap)
		} else {
			lost = append(lost, gap)
		}
	}
	require.NotEmpty(t, delayed)
	for _, gap := range delayed {
		require.False(t, gap.Open)
		require.Contains(t, gap.Reason, "connection refused")
	}
	require.Len(t, lost, 1)
	require.Equal(t, usa1, lost[0].StackID)
	require.Equal(t, "usa1", lost[0].Stack)
	require.Equal(t, restart.Unix(), lost[0].To.Unix())
	overview, err := h.svc.TrafficOverviewData(ctx)
	require.NoError(t, err)
	require.Len(t, overview.Gaps, len(result.Gaps))
}

// 面板停机超过几个采集间隔时，恢复后的首轮采集记录整个面板的延后缺口。
func TestTrafficGapAfterDowntime(t *testing.T) {
	h := &trafficHarness{harness: newRuntimeHarness(t)}
	h.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	h.svc.Now = func() time.Time { return h.now }
	ctx := context.Background()
	_, err := h.svc.Store.DB.ExecContext(ctx, "INSERT INTO traffic_heartbeat (id, at) VALUES (1, ?)", h.now.Add(-time.Hour).Unix())
	require.NoError(t, err)
	h.svc.TrafficTick(ctx)
	result, err := h.svc.TrafficRanking(ctx, app.TrafficRankingQuery{From: h.now.Add(-2 * time.Hour), To: h.now.Add(time.Hour)})
	require.NoError(t, err)
	require.Len(t, result.Gaps, 1)
	require.EqualValues(t, 0, result.Gaps[0].StackID)
	require.Equal(t, app.GapDelayed, result.Gaps[0].Kind)
	require.Equal(t, h.now.Add(-time.Hour).Unix(), result.Gaps[0].From.Unix())
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
	overview, err := h.svc.TrafficOverviewData(ctx)
	require.NoError(t, err)
	require.Equal(t, usage.Today, overview.Today.Users)
	require.Equal(t, usage.Today, overview.Today.Total)
	point := overview.Live[len(overview.Live)-1].Users[h.alice(t).ID]
	require.EqualValues(t, 2, point.Up)
	require.EqualValues(t, 3, point.Down)
	require.Equal(t, point.Bytes, point.Stacks["usa1"])
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
	overview, err := h.svc.TrafficOverviewData(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, overview.Live)
	point := overview.Live[len(overview.Live)-1]
	require.EqualValues(t, 10, point.Up)
	require.EqualValues(t, 20, point.Down)
	require.EqualValues(t, 10, point.Stacks["auto"].Up)
	require.EqualValues(t, 10, point.Stacks["usa1"].Up)
}
