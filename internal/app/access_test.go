package app_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/units"
)

// setAccessStats 打开或关闭访问域名统计。
func (h *trafficHarness) setAccessStats(t *testing.T, enabled bool) {
	t.Helper()
	ctx := context.Background()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Traffic.AccessStats = enabled
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
}

// visit 模拟 usa1 的 xray 写出一条访问日志。
func (h *trafficHarness) visit(email, target string) {
	h.units.AppendLog(units.LogEntry{
		Unit: units.Name(units.Xray, "usa1"), Time: h.now,
		Message: fmt.Sprintf("%s from 1.2.3.4:5678 accepted %s [vmess -> out] email: %s", h.now.Format("2006/01/02 15:04:05.000000"), target, email),
	})
}

// waitFollowing 等待日志读取开始；之后写出的日志一定会被读到。
func (h *trafficHarness) waitFollowing(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return h.units.Following() == 1 }, 3*time.Second, 5*time.Millisecond)
}

// accessCounts 返回用户今天各主域名的访问次数。
func accessCounts(t *testing.T, svc *app.Service, userID int64, now time.Time) map[string]int64 {
	t.Helper()
	result, err := svc.AccessUserDomains(context.Background(), userID, app.AccessQuery{From: now.Add(-time.Hour), To: now.Add(time.Hour)})
	require.NoError(t, err)
	out := map[string]int64{}
	for _, row := range result.Domains {
		out[row.Domain] = row.Count
	}
	return out
}

// waitAccess 反复执行 AccessTick，直到用户今天的访问次数等于 want。
func waitAccess(t *testing.T, svc *app.Service, userID int64, now time.Time, want map[string]int64) {
	t.Helper()
	require.Eventually(t, func() bool {
		svc.AccessTick(context.Background())
		got := accessCounts(t, svc, userID, now)
		if len(got) != len(want) {
			return false
		}
		for domain, count := range want {
			if got[domain] != count {
				return false
			}
		}
		return true
	}, 3*time.Second, 20*time.Millisecond, "want %v, got %v", want, accessCounts(t, svc, userID, now))
}

func TestAccessStatsCountsDomainsPerUser(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	alice := h.alice(t).ID
	t.Cleanup(func() { h.svc.StopAccess(context.Background()) })

	// 关闭时不读取日志。
	h.svc.AccessTick(ctx)
	status, err := h.svc.AccessStatusData(ctx)
	require.NoError(t, err)
	require.False(t, status.Enabled)
	require.False(t, status.Running)

	// 开启前写出的日志不计入：首次开启只统计之后的访问。
	h.visit(fmt.Sprintf("alice.u%d", alice), "tcp:old.example.com:443")
	h.setAccessStats(t, true)
	h.svc.AccessTick(ctx)
	h.waitFollowing(t)

	email := fmt.Sprintf("alice.u%d", alice)
	h.visit(email, "tcp:www.google.com:443")
	h.visit(email, "udp:mail.google.com:443")
	h.visit(email, "tcp:news.bbc.co.uk:80")
	h.visit(email, "tcp:[2001:db8::1]:443")
	h.visit("ghost.u999", "tcp:www.google.com:443")
	h.units.AppendLog(units.LogEntry{Unit: units.Name(units.Xray, "usa1"), Time: h.now, Message: "from 127.0.0.1:1 accepted tcp:relay.example.com:443 [relay -> out]"})
	h.units.AppendLog(units.LogEntry{Unit: units.Name(units.Xray, "usa1"), Time: h.now, Message: "[Warning] core: something happened"})
	waitAccess(t, h.svc, alice, h.now, map[string]int64{"google.com": 2, "bbc.co.uk": 1})

	top, err := h.svc.AccessTopDomains(ctx, app.AccessQuery{From: h.now.Add(-time.Hour), To: h.now.Add(time.Hour)})
	require.NoError(t, err)
	require.Equal(t, int64(3), top.Total, "deleted users and IP targets are dropped")
	require.Equal(t, int64(2), top.DomainCount)
	require.Equal(t, "google.com", top.Domains[0].Domain)
	require.Equal(t, int64(1), top.Domains[0].Users)

	users, err := h.svc.AccessDomainUsersData(ctx, "WWW.Google.com", app.AccessQuery{From: h.now.Add(-time.Hour), To: h.now.Add(time.Hour)})
	require.NoError(t, err)
	require.Equal(t, "google.com", users.Domain)
	require.Len(t, users.Users, 1)
	require.Equal(t, "alice", users.Users[0].Name)
	require.Equal(t, int64(2), users.Users[0].Count)

	csv := string(app.AccessDomainsCSV(top, false))
	require.Contains(t, csv, "domain,count,blocked,users\ngoogle.com,2,0,1\n")

	// 重启：新进程从数据库中的书签继续，停机期间写出的日志补读一次，已计入的不重复。
	h.svc.StopAccess(ctx)
	require.Zero(t, h.units.Following())
	h.visit(email, "tcp:www.google.com:443")
	restarted := app.New(h.svc.Store, h.svc.Layout)
	restarted.Units, restarted.Logs, restarted.XrayAPI, restarted.Now = h.units, h.units, h.api, h.svc.Now
	t.Cleanup(func() { restarted.StopAccess(context.Background()) })
	waitAccess(t, restarted, alice, h.now, map[string]int64{"google.com": 3, "bbc.co.uk": 1})

	// 关闭后再开启：关闭期间的访问不补读。
	h.setAccessStats(t, false)
	restarted.AccessTick(ctx)
	require.Zero(t, h.units.Following())
	h.visit(email, "tcp:www.google.com:443")
	h.setAccessStats(t, true)
	restarted.AccessTick(ctx)
	h.waitFollowing(t)
	h.visit(email, "tcp:example.org:443")
	waitAccess(t, restarted, alice, h.now, map[string]int64{"google.com": 3, "bbc.co.uk": 1, "example.org": 1})
}

func TestAccessStatsDaysFollowTimezoneAndRetention(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	alice := h.alice(t).ID
	email := fmt.Sprintf("alice.u%d", alice)
	t.Cleanup(func() { h.svc.StopAccess(context.Background()) })
	h.setAccessStats(t, true)
	h.svc.AccessTick(ctx)
	h.waitFollowing(t)

	// 日期按日志时间和设置时区（Asia/Shanghai）计算，不按读取时间。
	today := h.now
	old := today.AddDate(0, 0, -100)
	h.now = old
	h.visit(email, "tcp:old.example.com:443")
	h.now = today
	h.visit(email, "tcp:new.example.com:443")
	waitAccess(t, h.svc, alice, today, map[string]int64{"example.com": 1})
	require.Equal(t, map[string]int64{"example.com": 1}, accessCounts(t, h.svc, alice, old))

	// 超过保留天数（缺省 90 天）的统计被清理。
	h.svc.PruneNow(ctx)
	require.Empty(t, accessCounts(t, h.svc, alice, old))
	require.Equal(t, map[string]int64{"example.com": 1}, accessCounts(t, h.svc, alice, today))

	_, err := h.svc.AccessTopDomains(ctx, app.AccessQuery{From: today, To: today})
	require.Error(t, err)
	_, err = h.svc.AccessDomainUsersData(ctx, "bad host", app.AccessQuery{From: today, To: today.Add(time.Hour)})
	require.Error(t, err)
}

// 关闭后在下一轮检查之前重启：新进程启动时统计处于关闭状态，也要清掉数据库里的书签，之后重新开启不补读关闭期间的日志。
func TestAccessStatsDisabledAtStartupDropsCursor(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	alice := h.alice(t).ID
	email := fmt.Sprintf("alice.u%d", alice)
	h.setAccessStats(t, true)
	h.svc.AccessTick(ctx)
	h.waitFollowing(t)
	h.visit(email, "tcp:www.google.com:443")
	waitAccess(t, h.svc, alice, h.now, map[string]int64{"google.com": 1})

	h.setAccessStats(t, false)
	h.svc.StopAccess(ctx)
	h.visit(email, "tcp:www.google.com:443")

	restarted := app.New(h.svc.Store, h.svc.Layout)
	restarted.Units, restarted.Logs, restarted.XrayAPI, restarted.Now = h.units, h.units, h.api, h.svc.Now
	t.Cleanup(func() { restarted.StopAccess(context.Background()) })
	restarted.AccessTick(ctx)
	require.Zero(t, h.units.Following())
	h.visit(email, "tcp:www.google.com:443")

	h.setAccessStats(t, true)
	restarted.AccessTick(ctx)
	h.waitFollowing(t)
	h.visit(email, "tcp:example.org:443")
	waitAccess(t, restarted, alice, h.now, map[string]int64{"google.com": 1, "example.org": 1})
}

// TestAccessStatsCountsBlockedConnections 验证被域名黑名单送进黑洞的连接单独计数，不混进放行次数。
func TestAccessStatsCountsBlockedConnections(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	alice := h.alice(t).ID
	email := fmt.Sprintf("alice.u%d", alice)
	t.Cleanup(func() { h.svc.StopAccess(context.Background()) })
	h.setAccessStats(t, true)
	h.svc.AccessTick(ctx)
	h.waitFollowing(t)

	h.visit(email, "tcp:www.google.com:443")
	for range 2 {
		h.units.AppendLog(units.LogEntry{
			Unit: units.Name(units.Xray, "usa1"), Time: h.now,
			Message: "from 1.2.3.4:5678 accepted tcp:ads.google.com:443 [vmess -> block-usa1] email: " + email,
		})
	}
	h.visit(email, "tcp:example.org:443")
	waitAccess(t, h.svc, alice, h.now, map[string]int64{"google.com": 1, "example.org": 1})
	window := app.AccessQuery{From: h.now.Add(-time.Hour), To: h.now.Add(time.Hour)}
	require.Eventually(t, func() bool {
		h.svc.AccessTick(ctx)
		result, err := h.svc.AccessUserDomains(ctx, alice, window)
		return err == nil && result.Blocked == 2
	}, 3*time.Second, 20*time.Millisecond)

	top, err := h.svc.AccessTopDomains(ctx, window)
	require.NoError(t, err)
	require.Equal(t, int64(2), top.Total)
	require.Equal(t, int64(2), top.Blocked)
	require.Equal(t, "google.com", top.Domains[0].Domain, "blocked connections count toward the ranking")
	require.Equal(t, int64(1), top.Domains[0].Count)
	require.Equal(t, int64(2), top.Domains[0].Blocked)
	require.Contains(t, string(app.AccessDomainsCSV(top, false)), "google.com,1,2,1\n")

	users, err := h.svc.AccessDomainUsersData(ctx, "google.com", window)
	require.NoError(t, err)
	require.Equal(t, int64(2), users.Users[0].Blocked)
}

// TestSetDomainBlockedUpdatesListsAndPendingChanges 验证一键拉黑写入全局或用户名单、进入待应用变更，并在访问排行中标出。
func TestSetDomainBlockedUpdatesListsAndPendingChanges(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	alice := h.alice(t).ID
	email := fmt.Sprintf("alice.u%d", alice)
	t.Cleanup(func() { h.svc.StopAccess(context.Background()) })
	h.setAccessStats(t, true)
	h.svc.AccessTick(ctx)
	h.waitFollowing(t)
	h.visit(email, "tcp:www.google.com:443")
	h.visit(email, "tcp:example.org:443")
	waitAccess(t, h.svc, alice, h.now, map[string]int64{"google.com": 1, "example.org": 1})
	window := app.AccessQuery{From: h.now.Add(-time.Hour), To: h.now.Add(time.Hour)}

	// 全局拉黑：输入子域名时按主域名写入，配置出现待应用的 xray 变更。
	result, err := h.svc.SetDomainBlocked(ctx, h.actor, "WWW.Google.com", 0, true)
	require.NoError(t, err)
	require.Equal(t, app.BlockDomainResult{Domain: "google.com", Entry: "google.com", Changed: true, Listed: app.BlockListedGlobal}, *result)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"google.com"}, doc.Settings.Traffic.BlockedDomains)
	pending, err := h.svc.Pending(ctx)
	require.NoError(t, err)
	diffs := map[string]string{}
	for _, change := range pending.Changes {
		diffs[change.Stack+"/"+change.Component] += change.Diff
	}
	require.Contains(t, diffs["usa1/xray"], "domain:google.com")
	require.Contains(t, diffs["usa1/xray"], "block-usa1")
	require.Contains(t, diffs["usa1/xray"], "routeOnly")
	require.NotContains(t, diffs, "usa1/mihomo", "the blocklist never touches mihomo")

	result, err = h.svc.SetDomainBlocked(ctx, h.actor, "google.com", 0, true)
	require.NoError(t, err)
	require.False(t, result.Changed, "already listed")

	// 用户拉黑：只写入该用户的名单；全局名单的状态优先显示。
	result, err = h.svc.SetDomainBlocked(ctx, h.actor, "example.org", alice, true)
	require.NoError(t, err)
	require.Equal(t, app.BlockListedUser, result.Listed)
	mine, err := h.svc.AccessUserDomains(ctx, alice, window)
	require.NoError(t, err)
	listed := map[string]string{}
	for _, row := range mine.Domains {
		listed[row.Domain] = row.Listed
	}
	require.Equal(t, map[string]string{"google.com": app.BlockListedGlobal, "example.org": app.BlockListedUser}, listed)
	top, err := h.svc.AccessTopDomains(ctx, window)
	require.NoError(t, err)
	for _, row := range top.Domains {
		if row.Domain == "example.org" {
			require.Empty(t, row.Listed, "the global view ignores per-user lists")
		}
	}

	// 移出：只删除相同条目；由上级域名条目拦截时报错。
	result, err = h.svc.SetDomainBlocked(ctx, h.actor, "example.org", alice, false)
	require.NoError(t, err)
	require.True(t, result.Changed)
	require.Empty(t, result.Listed)
	settings := *doc.Settings
	settings.Traffic.BlockedDomains = []string{"org"}
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
	_, err = h.svc.SetDomainBlocked(ctx, h.actor, "example.org", 0, false)
	var fieldErr *app.FieldError
	require.ErrorAs(t, err, &fieldErr)
	for _, bad := range []string{"1.2.3.4", "full:foo.com", "domain:foo.com", "a:b.com", "例子.中国"} {
		_, err = h.svc.SetDomainBlocked(ctx, h.actor, bad, 0, true)
		require.ErrorAs(t, err, &fieldErr, bad)
		require.Equal(t, "domain", fieldErr.Field, bad)
	}
	_, err = h.svc.SetDomainBlocked(ctx, h.actor, "example.org", 9999, true)
	require.ErrorIs(t, err, app.ErrNotFound)
}
