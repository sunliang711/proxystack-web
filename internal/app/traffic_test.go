package app_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/notify"
	"proxystack-web/internal/store"
	"proxystack-web/internal/xrayapi"
)

const usa1API = "127.0.0.1:10091"

// trafficHarness 是已应用配置、时钟可控的测试实例。
type trafficHarness struct {
	*harness
	now   time.Time
	mu    sync.Mutex
	mails []string
	fail  error
}

func newTrafficHarness(t *testing.T) *trafficHarness {
	t.Helper()
	h := &trafficHarness{harness: newRuntimeHarness(t)}
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	h.now = time.Date(2026, 11, 10, 12, 0, 30, 0, loc)
	h.svc.Now = func() time.Time { return h.now }
	h.svc.SendMail = func(_ context.Context, _ notify.Config, subject, _ string) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.fail != nil {
			return h.fail
		}
		h.mails = append(h.mails, subject)
		return nil
	}
	ctx := context.Background()
	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
	return h
}

// use 模拟 alice 产生流量，并推进到下一分钟落库。
func (h *trafficHarness) use(t *testing.T, up, down int64) {
	t.Helper()
	h.api.AddStats(usa1API,
		xrayapi.Stat{Name: "user>>>u1>>>traffic>>>uplink", Value: up},
		xrayapi.Stat{Name: "user>>>u1>>>traffic>>>downlink", Value: down},
		xrayapi.Stat{Name: "inbound>>>vmess:24101:vmess>>>traffic>>>uplink", Value: up},
		xrayapi.Stat{Name: "inbound>>>vmess:24101:vmess>>>traffic>>>downlink", Value: down},
	)
	h.tick(t, time.Minute)
}

func (h *trafficHarness) tick(t *testing.T, advance time.Duration) {
	t.Helper()
	h.svc.TrafficTick(context.Background())
	h.now = h.now.Add(advance)
	h.svc.TrafficTick(context.Background())
}

func (h *trafficHarness) alice(t *testing.T) configdoc.User {
	t.Helper()
	doc, err := h.svc.LoadConfig(context.Background())
	require.NoError(t, err)
	return doc.Users[doc.UserByName("alice")]
}

func (h *trafficHarness) update(t *testing.T, change func(*configdoc.User)) {
	t.Helper()
	user := h.alice(t)
	change(&user)
	_, err := h.svc.UpdateUser(context.Background(), h.actor, user.ID, user)
	require.NoError(t, err)
}

func (h *trafficHarness) pauses(t *testing.T) []string {
	t.Helper()
	usage, err := h.svc.UserUsageDetail(context.Background(), h.alice(t).ID)
	require.NoError(t, err)
	out := []string{}
	for _, pause := range usage.AutoPauses {
		out = append(out, pause.Reason)
	}
	return out
}

func (h *trafficHarness) generated(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(h.svc.Layout.GeneratedXray("usa1"))
	require.NoError(t, err)
	return string(data)
}

func (h *trafficHarness) revisions(t *testing.T) int {
	t.Helper()
	var count int
	require.NoError(t, h.svc.Store.DB.QueryRow("SELECT count(*) FROM config_revisions").Scan(&count))
	return count
}

func (h *trafficHarness) calls() string { return strings.Join(h.api.Calls, "\n") }

// TestTrafficCollectsAndAggregates 验证计数进入分钟明细、用量表和各级汇总，改名后历史仍然连续，
// 未落库的部分也计入总览。
func TestTrafficCollectsAndAggregates(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.use(t, 100, 200)
	h.use(t, 10, 20)

	usage, err := h.svc.UserUsageDetail(ctx, h.alice(t).ID)
	require.NoError(t, err)
	require.EqualValues(t, 330, usage.Today.Total())
	require.EqualValues(t, 330, usage.Period.Total())
	require.Len(t, usage.Daily, 30)
	require.EqualValues(t, 330, usage.Daily[29].Total())

	h.update(t, func(u *configdoc.User) { u.Name = "alicia" })
	result, err := h.svc.QueryTraffic(ctx, app.TrafficQuery{
		Level: "hour", Scope: "user", From: h.now.Add(-2 * time.Hour), To: h.now.Add(time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, result.Series, 1)
	require.Equal(t, "alicia", result.Series[0].Label)
	require.EqualValues(t, 330, result.Series[0].Total())

	for _, level := range []string{"day", "month", "year"} {
		result, err = h.svc.QueryTraffic(ctx, app.TrafficQuery{
			Level: level, Scope: "inbound", From: h.now.AddDate(-1, 0, 0), To: h.now.AddDate(0, 1, 0),
		})
		require.NoError(t, err)
		require.Len(t, result.Series, 1, level)
		require.EqualValues(t, 330, result.Series[0].Total(), level)
	}

	// 只采集未落库：总览合并内存中的部分。
	h.api.AddStats(usa1API, xrayapi.Stat{Name: "inbound>>>vmess:24101:vmess>>>traffic>>>downlink", Value: 5},
		xrayapi.Stat{Name: "user>>>u1>>>traffic>>>downlink", Value: 5})
	h.svc.TrafficTick(ctx)
	overview, err := h.svc.TrafficOverviewData(ctx, 0)
	require.NoError(t, err)
	require.EqualValues(t, 335, overview.Today.Total())
	require.EqualValues(t, 335, overview.Month.Total())
	require.Len(t, overview.TopUsers, 1)
	require.Equal(t, "alicia", overview.TopUsers[0].Name)
	require.EqualValues(t, 335, overview.TopUsers[0].Total())
	require.Len(t, overview.TopUsersToday, 1)
	require.EqualValues(t, 335, overview.TopUsersToday[0].Total())
}

// TestTrafficCollectBeforeRestart 验证重启 xray 前补采，两次采集之间的流量不丢。
func TestTrafficCollectBeforeRestart(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.api.AddStats(usa1API, xrayapi.Stat{Name: "user>>>u1>>>traffic>>>uplink", Value: 42})
	require.NoError(t, h.svc.UnitAction(ctx, h.actor, "usa1", "xray", "restart"))
	require.Empty(t, h.api.Stats[usa1API], "counters were read before the restart")
	usage, err := h.svc.UserUsageDetail(ctx, h.alice(t).ID)
	require.NoError(t, err)
	require.EqualValues(t, 42, usage.Today.Total())
}

// TestStopFlushesPendingTraffic 验证服务正常停止时，内存中未落库的用量写入数据库，WaitBackground 返回后数据已落盘。
func TestStopFlushesPendingTraffic(t *testing.T) {
	h := newTrafficHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, h.svc.StartBackground(ctx))
	require.Eventually(t, func() bool {
		h.api.AddStats(usa1API, xrayapi.Stat{Name: "user>>>u1>>>traffic>>>uplink", Value: 7})
		return len(h.api.Stats) > 0
	}, time.Second, 10*time.Millisecond)
	cancel()
	h.svc.WaitBackground()
	var up int64
	require.NoError(t, h.svc.Store.DB.QueryRow("SELECT COALESCE(SUM(up), 0) FROM user_daily_usage").Scan(&up))
	require.GreaterOrEqual(t, up, int64(7))
}

// TestDailyLimitPausesAndRecoversNextDay 验证达到每日阈值后自动暂停、不改配置，解除后当天不再暂停，次日恢复判定。
func TestDailyLimitPausesAndRecoversNextDay(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.update(t, func(u *configdoc.User) { u.DailyLimitBytes = 1000 })
	before := h.revisions(t)

	h.use(t, 300, 300)
	require.Empty(t, h.pauses(t))
	h.use(t, 300, 200)
	require.Equal(t, []string{app.PauseDaily}, h.pauses(t))
	require.Contains(t, h.calls(), "rmu "+usa1API+" vmess:24101:vmess alice.u1")
	require.Contains(t, h.generated(t), "psw-placeholder")
	require.True(t, h.alice(t).Enabled, "an automatic pause does not touch the config")
	require.Equal(t, before, h.revisions(t), "an automatic pause adds no config revision")
	pending, err := h.svc.Pending(ctx)
	require.NoError(t, err)
	require.Empty(t, filterKind(pending.Changes, "xray", "usa1"), "an automatic pause is not a pending change")

	// 解除：恢复用户，当天再超也不暂停。
	require.NoError(t, h.svc.ReleaseAutoPause(ctx, h.actor, h.alice(t).ID, app.PauseDaily))
	require.Empty(t, h.pauses(t))
	require.Contains(t, h.api.Calls[len(h.api.Calls)-1], "adu "+usa1API)
	h.use(t, 5000, 5000)
	require.Empty(t, h.pauses(t))

	// 次日：解除标记失效，用量归零。
	h.now = h.now.Add(24 * time.Hour)
	h.tick(t, time.Minute)
	usage, err := h.svc.UserUsageDetail(ctx, h.alice(t).ID)
	require.NoError(t, err)
	require.Zero(t, usage.Today.Total())
	require.False(t, usage.Exempt)
	h.use(t, 2000, 0)
	require.Equal(t, []string{app.PauseDaily}, h.pauses(t))

	// 再过一天：未解除的暂停自动恢复。
	h.now = h.now.Add(24 * time.Hour)
	h.tick(t, time.Minute)
	require.Empty(t, h.pauses(t))
	require.NotContains(t, h.generated(t), "psw-placeholder")
}

// TestDailyLimitNotifyOnly 验证动作为只通知时不暂停，通知一天只记录一次。
func TestDailyLimitNotifyOnly(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.update(t, func(u *configdoc.User) {
		u.DailyLimitBytes = 1000
		u.DailyLimitAction = configdoc.DailyActionNotify
	})
	h.use(t, 2000, 0)
	h.use(t, 2000, 0)
	require.Empty(t, h.pauses(t))
	items, err := h.svc.ListNotifications(ctx, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, configdoc.EventDailyLimit, items[0].Event)
	require.Equal(t, app.NotifySkipped, items[0].Status, "mail is not enabled")
}

// TestQuotaPausesAndResetsWithPeriod 验证额度超限暂停，到重置日自动恢复；调高额度当场解除。
func TestQuotaPausesAndResetsWithPeriod(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.update(t, func(u *configdoc.User) {
		u.QuotaBytes = 1000
		u.ResetMode, u.ResetDay = "monthly", 15
	})
	h.use(t, 600, 600)
	require.Equal(t, []string{app.PauseQuota}, h.pauses(t))
	usage, err := h.svc.UserUsageDetail(ctx, h.alice(t).ID)
	require.NoError(t, err)
	require.True(t, usage.NextReset.Equal(time.Date(2026, 11, 15, 0, 0, 0, 0, h.now.Location())))
	require.True(t, usage.AutoPauses[0].ResumeAt.Equal(usage.NextReset))

	require.Error(t, h.svc.ReleaseAutoPause(ctx, h.actor, h.alice(t).ID, app.PauseQuota))

	h.update(t, func(u *configdoc.User) { u.QuotaBytes = 5000 })
	require.Empty(t, h.pauses(t), "raising the quota lifts the pause at once")

	h.update(t, func(u *configdoc.User) { u.QuotaBytes = 1000 })
	require.Equal(t, []string{app.PauseQuota}, h.pauses(t))

	h.now = time.Date(2026, 11, 15, 0, 0, 10, 0, h.now.Location())
	h.tick(t, time.Minute)
	require.Empty(t, h.pauses(t))
	usage, err = h.svc.UserUsageDetail(ctx, h.alice(t).ID)
	require.NoError(t, err)
	require.Zero(t, usage.Period.Total())
}

// TestExpiryPausesAndExtends 验证到期自动暂停，延长到期时间当场解除；手动暂停和自动暂停互不影响。
func TestExpiryPausesAndExtends(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.update(t, func(u *configdoc.User) { u.ExpireAt = h.now.Add(time.Hour).Format(time.RFC3339) })
	require.Empty(t, h.pauses(t))
	items, err := h.svc.ListNotifications(ctx, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, configdoc.EventExpiring, items[0].Event)

	h.now = h.now.Add(2 * time.Hour)
	h.tick(t, time.Minute)
	require.Equal(t, []string{app.PauseExpired}, h.pauses(t))

	// 手动恢复开关不能放出到期用户：用户仍在自动暂停中。
	removed := strings.Count(h.calls(), "rmu ")
	_, err = h.svc.SetUserPause(ctx, h.actor, h.alice(t).ID, false, nil)
	require.NoError(t, err)
	_, err = h.svc.SetUserPause(ctx, h.actor, h.alice(t).ID, true, nil)
	require.NoError(t, err)
	require.Equal(t, removed, strings.Count(h.calls(), "rmu "), "already paused: nothing to remove")
	require.Contains(t, h.generated(t), "psw-placeholder")

	h.update(t, func(u *configdoc.User) { u.ExpireAt = "" })
	require.Empty(t, h.pauses(t))
	require.NotContains(t, h.generated(t), "psw-placeholder")
}

// TestDisconnectOnPauseRestartsXray 验证开启「暂停时断开现有连接」后，自动暂停重启受影响的 xray。
func TestDisconnectOnPauseRestartsXray(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Traffic.DisconnectOnPause = true
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
	h.update(t, func(u *configdoc.User) { u.DailyLimitBytes = 10 })
	calls := len(h.units.Calls)
	h.use(t, 100, 0)
	require.Equal(t, []string{app.PauseDaily}, h.pauses(t))
	require.Contains(t, strings.Join(h.units.Calls[calls:], "\n"), "restart psw-xray@usa1.service")
}

// TestNotificationDeliveryRetries 验证邮件走队列发送，失败按退避重试，重试用完记为失败，可以手动重发。
func TestNotificationDeliveryRetries(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Notify.Enabled = true
	settings.Notify.Host, settings.Notify.Port = "smtp.example.com", 587
	settings.Notify.From, settings.Notify.To = "panel@example.com", []string{"admin@example.com"}
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
	h.update(t, func(u *configdoc.User) { u.DailyLimitBytes = 10 })

	h.fail = errors.New("connection refused")
	h.use(t, 100, 0)
	for attempt := 1; attempt <= 4; attempt++ {
		h.svc.DeliverNotifications(ctx)
		items, err := h.svc.ListNotifications(ctx, 10)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.EqualValues(t, attempt, items[0].Attempts)
		require.Equal(t, "connection refused", items[0].Error)
		if attempt < 4 {
			require.Equal(t, app.NotifyPending, items[0].Status)
			h.svc.DeliverNotifications(ctx)
			again, err := h.svc.ListNotifications(ctx, 10)
			require.NoError(t, err)
			require.EqualValues(t, attempt, again[0].Attempts, "waits for the backoff")
			h.now = h.now.Add(16 * time.Minute)
		} else {
			require.Equal(t, app.NotifyFailed, items[0].Status)
		}
	}
	overview, err := h.svc.TrafficOverviewData(ctx, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, overview.FailedNotifications)

	h.fail = nil
	items, err := h.svc.ListNotifications(ctx, 10)
	require.NoError(t, err)
	require.NoError(t, h.svc.ResendNotification(ctx, items[0].ID))
	require.Eventually(t, func() bool {
		h.svc.DeliverNotifications(ctx)
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.mails) >= 1
	}, time.Second, 10*time.Millisecond)
	require.Contains(t, h.mails[0], "达到每日用量阈值")
	require.Contains(t, h.mails[0], "alice")
	items, err = h.svc.ListNotifications(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, app.NotifySent, items[0].Status)
}

// TestTrafficRetentionPrunes 验证超过保留期的明细被清理，永久保留的级别不受影响。
func TestTrafficRetentionPrunes(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.use(t, 100, 100)
	first := h.now
	h.now = first.AddDate(0, 0, 8)
	h.tick(t, time.Minute)
	h.svc.PruneNow(ctx)
	rows, err := h.svc.Store.QueryTraffic(ctx, store.LevelMinute, "user", 0, h.now.Unix(), nil)
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = h.svc.Store.QueryTraffic(ctx, store.LevelHour, "user", 0, h.now.Unix(), nil)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
}
