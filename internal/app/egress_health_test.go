package app_test

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/deps"
	"proxystack-web/internal/units"
)

func newEgressHarness(t *testing.T) (*trafficHarness, configdoc.Stack) {
	t.Helper()
	h := newTrafficHarness(t)
	ctx := context.Background()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Notify.Enabled = true
	settings.Notify.Host, settings.Notify.Port = "smtp.example.com", 587
	settings.Notify.From, settings.Notify.To = "panel@example.com", []string{"admin@example.com"}
	settings.Notify.Events = append(settings.Notify.Events, configdoc.EventEgressDown, configdoc.EventEgressRecovered)
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
	job := jobNamed(t, h, app.JobEgressCheck)
	job.Params.RetryCount = 0
	require.NoError(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: job.Enabled, Timeout: job.Timeout, Params: job.Params}))
	for _, stack := range doc.Stacks {
		if stack.Name != "usa1" {
			require.NoError(t, h.units.Stop(ctx, units.Name(units.Mihomo, stack.Name)))
		}
	}
	return h, doc.Stacks[doc.StackByName("usa1")]
}

// checkEgress 执行一轮出口检测并返回执行记录；出口全部失败时执行记为失败，其余情况记为成功。
func checkEgress(t *testing.T, h *trafficHarness) *app.JobRun {
	t.Helper()
	job := jobNamed(t, h, app.JobEgressCheck)
	require.NoError(t, h.svc.RunJobNow(context.Background(), h.actor, job.ID))
	h.svc.WaitJobs()
	job = jobNamed(t, h, app.JobEgressCheck)
	require.Contains(t, []string{app.RunSucceeded, app.RunFailed}, job.LastRun.Status, job.LastRun.Log)
	h.now = h.now.Add(time.Minute)
	return job.LastRun
}

func egressStatus(t *testing.T, h *trafficHarness, id int64) app.EgressHealth {
	t.Helper()
	view, err := h.svc.StackEgressHealth(context.Background())
	require.NoError(t, err)
	for _, row := range view.Stacks {
		if row.StackID == id {
			return row
		}
	}
	t.Fatalf("missing egress health for stack %d", id)
	return app.EgressHealth{}
}

// TestEgressHealthLifecycle 验证两个地址族只需一个可用、单轮失败告警、持久化去重及每次故障独立通知。
func TestEgressHealthLifecycle(t *testing.T) {
	h, stack := newEgressHarness(t)
	ctx := context.Background()
	results := []app.EgressResult{{Family: "ipv4", EgressInfo: app.EgressInfo{IP: "203.0.113.1"}}}
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) {
		return results, nil
	}
	run := checkEgress(t, h)
	require.Equal(t, app.RunSucceeded, run.Status, run.Log)
	require.Equal(t, "healthy", egressStatus(t, h, stack.ID).Status)
	notifications, err := h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, notifications, "initial successful probe is silent")
	results = nil
	// 重试耗尽仍失败时，告警的同时把本次执行记为失败。
	run = checkEgress(t, h)
	require.Equal(t, app.RunFailed, run.Status, run.Log)
	require.Contains(t, run.Log, "Stack usa1: 出口检测失败")
	require.Equal(t, "down", egressStatus(t, h, stack.ID).Status)
	require.EqualValues(t, 1, egressStatus(t, h, stack.ID).Failures)
	h.svc.DeliverNotifications(ctx)
	require.Len(t, h.mails, 1)
	require.Contains(t, h.mails[0], "Stack 出口异常：usa1")

	// 新 Service 只复用数据库和运行时依赖，不复用任何内存去重状态。
	previous := h.svc
	h.svc = app.New(previous.Store, previous.Layout)
	h.svc.Now, h.svc.Units, h.svc.ProbeEgress, h.svc.SendMail = previous.Now, h.units, previous.ProbeEgress, previous.SendMail
	checkEgress(t, h)
	h.svc.DeliverNotifications(ctx)
	require.Len(t, h.mails, 1)
	results = []app.EgressResult{{Family: "ipv6", EgressInfo: app.EgressInfo{IP: "2001:db8::1"}}}
	checkEgress(t, h)
	h.svc.DeliverNotifications(ctx)
	require.Equal(t, "healthy", egressStatus(t, h, stack.ID).Status)
	require.Len(t, h.mails, 2)
	require.Contains(t, h.mails[1], "Stack 出口恢复：usa1")
	checkEgress(t, h)
	h.svc.DeliverNotifications(ctx)
	require.Len(t, h.mails, 2)
	results = nil
	checkEgress(t, h)
	checkEgress(t, h)
	h.svc.DeliverNotifications(ctx)
	require.Len(t, h.mails, 3)
	notifications, err = h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Len(t, notifications, 3)
	for _, row := range notifications {
		require.Equal(t, stack.ID, row.StackID)
		require.Equal(t, "usa1", row.StackName)
		require.Zero(t, row.UserID)
	}
}

// TestEgressFailureNotificationFormat 验证故障邮件按地址族和数据源分行展示错误与详情链接。
func TestEgressFailureNotificationFormat(t *testing.T) {
	h, stack := newEgressHarness(t)
	ctx := context.Background()
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) {
		return []app.EgressResult{
			{Family: "ipv4", Sources: []app.EgressSource{
				{URL: "https://ipinfo.io/json", Error: `Get "https://ipinfo.io/json": context deadline exceeded`},
				{URL: "https://myip.ipip.net", Error: `Get "https://myip.ipip.net": context deadline exceeded`},
			}},
			{Family: "ipv6", Sources: []app.EgressSource{
				{URL: "https://ifconfig.me/all.json", Error: `Get "https://ifconfig.me/all.json": context deadline exceeded`},
				{URL: "https://ifconfig.co/json", Error: `Get "https://ifconfig.co/json": context deadline exceeded`},
				{URL: "https://api64.ipify.org?format=json", Error: `Get "https://api64.ipify.org?format=json": context deadline exceeded`},
			}},
		}, nil
	}
	checkEgress(t, h)
	rows, err := h.svc.Store.Q.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	info, err := h.svc.Info(ctx)
	require.NoError(t, err)
	detailURL := strings.TrimRight(info.PanelURL, "/") + "/stacks/" + strconv.FormatInt(stack.ID, 10)
	require.Contains(t, rows[0].Body, "本轮查询失败次数：1\n\n"+
		"IPv4：\n"+
		"- https://ipinfo.io/json：context deadline exceeded\n"+
		"- https://myip.ipip.net：context deadline exceeded\n\n"+
		"IPv6：\n"+
		"- https://ifconfig.me/all.json：context deadline exceeded\n"+
		"- https://ifconfig.co/json：context deadline exceeded\n"+
		"- https://api64.ipify.org?format=json：context deadline exceeded\n\n"+
		"Stack 详情：\n"+detailURL+"\n")
}

// TestEgressRecoverySupersedesUnsentAlarm 验证发送失败后恢复，不会在恢复邮件后重发旧故障。
func TestEgressRecoverySupersedesUnsentAlarm(t *testing.T) {
	h, _ := newEgressHarness(t)
	ctx := context.Background()
	results := []app.EgressResult(nil)
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) {
		return results, nil
	}
	checkEgress(t, h)
	checkEgress(t, h)
	h.fail = errors.New("SMTP unavailable")
	h.svc.DeliverNotifications(ctx)
	rows, err := h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	alarmID := rows[0].ID
	require.Equal(t, app.NotifyPending, rows[0].Status)
	results = []app.EgressResult{{Family: "ipv4", EgressInfo: app.EgressInfo{IP: "203.0.113.2"}}}
	checkEgress(t, h)
	h.fail = nil
	h.svc.DeliverNotifications(ctx)
	require.Len(t, h.mails, 1)
	require.Contains(t, h.mails[0], "出口恢复")
	require.Error(t, h.svc.ResendNotification(ctx, alarmID))
	rows, err = h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, app.NotifySkipped, rows[1].Status)
	h.now = h.now.Add(time.Hour)
	h.svc.DeliverNotifications(ctx)
	require.Len(t, h.mails, 1)
}

// TestEgressUsesAppliedSettings 验证后台使用已应用的监听和认证，保存未应用的新端口不影响检测。
func TestEgressUsesAppliedSettings(t *testing.T) {
	h, stack := newEgressHarness(t)
	ctx := context.Background()
	original := net.JoinHostPort(stack.Mihomo.Socks.Listen, strconv.Itoa(stack.Mihomo.Socks.Port))
	stack.Mihomo.Socks.Port = 7099
	_, err := h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	var actual deps.ProxyConfig
	h.svc.ProbeEgress = func(_ context.Context, proxy deps.ProxyConfig, _ time.Duration) ([]app.EgressResult, error) {
		actual = proxy
		return []app.EgressResult{{Family: "ipv4", EgressInfo: app.EgressInfo{IP: "203.0.113.3"}}}, nil
	}
	checkEgress(t, h)
	require.Equal(t, original, actual.Address)
	require.True(t, actual.RemoteDNS)
	require.Equal(t, "healthy", egressStatus(t, h, stack.ID).Status)
}

// TestEgressStoppedAndUnsupported 验证主动停止不报警，已告警故障在暂停后继续保留且不重复通知。
func TestEgressStoppedAndUnsupported(t *testing.T) {
	h, stack := newEgressHarness(t)
	ctx := context.Background()
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) { return nil, nil }
	checkEgress(t, h)
	require.NoError(t, h.units.Stop(ctx, units.Name(units.Mihomo, stack.Name)))
	checkEgress(t, h)
	require.Equal(t, "stopped", egressStatus(t, h, stack.ID).Status)
	require.Zero(t, egressStatus(t, h, stack.ID).Failures)
	require.NoError(t, h.units.Start(ctx, units.Name(units.Mihomo, stack.Name)))
	checkEgress(t, h)
	require.Equal(t, "down", egressStatus(t, h, stack.ID).Status)
	rows, err := h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1, "stopping and restarting must not generate duplicate alarms or recovery")
	stack.Mihomo.Enabled = false
	stack.Xray.Outbound = configdoc.Outbound{Type: "direct"}
	_, err = h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	id, err := h.svc.Apply(ctx, h.actor, []string{stack.Name})
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
	checkEgress(t, h)
	require.Equal(t, "unsupported", egressStatus(t, h, stack.ID).Status)
}

// TestEgressDiscardsStoppedProbe 验证检测过程中停止 Stack 的过期失败结果被丢弃。
func TestEgressDiscardsStoppedProbe(t *testing.T) {
	h, stack := newEgressHarness(t)
	h.svc.ProbeEgress = func(ctx context.Context, _ deps.ProxyConfig, _ time.Duration) ([]app.EgressResult, error) {
		require.NoError(t, h.units.Stop(ctx, units.Name(units.Mihomo, stack.Name)))
		return nil, nil
	}
	checkEgress(t, h)
	row, err := h.svc.Store.Q.GetStackEgressHealth(context.Background(), stack.ID)
	require.Error(t, err)
	require.Zero(t, row.Failures)
}

// TestEgressJobOptions 验证参数边界、关闭检测的展示，以及状态接口不触发网络探测。
func TestEgressJobOptions(t *testing.T) {
	h, stack := newEgressHarness(t)
	ctx := context.Background()
	job := jobNamed(t, h, app.JobEgressCheck)
	require.Equal(t, "*/2 * * * *", job.Schedule)
	require.Equal(t, 0, job.Params.RetryCount)
	for _, params := range []app.JobParams{{RetryCount: -1, ProbeTimeout: 12}, {RetryCount: 3, ProbeTimeout: 0}, {RetryCount: 11, ProbeTimeout: 12}, {RetryCount: 3, ProbeTimeout: 61}} {
		require.Error(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: true, Timeout: 120, Params: params}))
	}
	var probes atomic.Int64
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) {
		probes.Add(1)
		return nil, nil
	}
	for range 3 {
		_ = egressStatus(t, h, stack.ID)
	}
	require.Zero(t, probes.Load())
	require.NoError(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: false, Timeout: job.Timeout, Params: job.Params}))
	require.Equal(t, "paused", egressStatus(t, h, stack.ID).Status)
}

// TestEgressDisabledMailStillRecords 验证未订阅事件时保留事件记录，不发送邮件。
func TestEgressDisabledMailStillRecords(t *testing.T) {
	h, _ := newEgressHarness(t)
	ctx := context.Background()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Notify.Events = slices.DeleteFunc(settings.Notify.Events, func(event string) bool {
		return event == configdoc.EventEgressDown || event == configdoc.EventEgressRecovered
	})
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) { return nil, nil }
	checkEgress(t, h)
	checkEgress(t, h)
	h.svc.DeliverNotifications(ctx)
	require.Empty(t, h.mails)
	rows, err := h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, app.NotifySkipped, rows[0].Status)
}

// TestEgressJobTimeoutDoesNotCountFailure 验证任务整体超时属于采集取消，不误计为出口失败。
func TestEgressJobTimeoutDoesNotCountFailure(t *testing.T) {
	h, stack := newEgressHarness(t)
	ctx := context.Background()
	job := jobNamed(t, h, app.JobEgressCheck)
	require.NoError(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: true, Timeout: 1, Params: job.Params}))
	h.svc.ProbeEgress = func(ctx context.Context, _ deps.ProxyConfig, _ time.Duration) ([]app.EgressResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	require.NoError(t, h.svc.RunJobNow(ctx, h.actor, job.ID))
	h.svc.WaitJobs()
	require.Equal(t, app.RunTimeout, jobNamed(t, h, app.JobEgressCheck).LastRun.Status)
	require.Equal(t, "unknown", egressStatus(t, h, stack.ID).Status)
	rows, err := h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, rows)
}

// TestEgressConcurrencyLimit 验证多个运行中 Stack 并发检测，并限制最多四个网络探测。
func TestEgressConcurrencyLimit(t *testing.T) {
	h, _ := newEgressHarness(t)
	ctx := context.Background()
	names := []string{"check1", "check2", "check3", "check4", "check5"}
	for _, name := range names {
		_, err := h.svc.CreateStack(ctx, h.actor, configdoc.NewStackOptions{Template: "pair", Name: name})
		require.NoError(t, err)
	}
	id, err := h.svc.Apply(ctx, h.actor, names)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
	var active, peak, total atomic.Int64
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		total.Add(1)
		time.Sleep(20 * time.Millisecond)
		return []app.EgressResult{{Family: "ipv4", EgressInfo: app.EgressInfo{IP: "203.0.113.4"}}}, nil
	}
	checkEgress(t, h)
	require.EqualValues(t, 6, total.Load())
	require.GreaterOrEqual(t, peak.Load(), int64(2))
	require.LessOrEqual(t, peak.Load(), int64(4))
}

// TestEgressDeletedStackDoesNotNotify 验证检测期间删除 Stack 后不写入过期结果或发送邮件。
func TestEgressDeletedStackDoesNotNotify(t *testing.T) {
	h, _ := newEgressHarness(t)
	ctx := context.Background()
	require.NoError(t, h.units.Stop(ctx, units.Name(units.Mihomo, "usa1")))
	stack, err := h.svc.CreateStack(ctx, h.actor, configdoc.NewStackOptions{Template: "pair", Name: "temporary"})
	require.NoError(t, err)
	id, err := h.svc.Apply(ctx, h.actor, []string{stack.Name})
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
	h.svc.ProbeEgress = func(ctx context.Context, _ deps.ProxyConfig, _ time.Duration) ([]app.EgressResult, error) {
		require.NoError(t, h.svc.DeleteStack(ctx, h.actor, stack.ID))
		return nil, nil
	}
	checkEgress(t, h)
	_, err = h.svc.Store.Q.GetStackEgressHealth(ctx, stack.ID)
	require.Error(t, err)
	rows, err := h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, rows)
}

// strictEgressUnits 模拟 systemd 对未加载或异常 unit 的查询失败。
type strictEgressUnits struct {
	units.Manager
	known    map[string]bool
	broken   string
	rejected atomic.Int64
}

func (m *strictEgressUnits) States(ctx context.Context, names []string) (map[string]units.State, error) {
	for _, name := range names {
		if !m.known[name] || name == m.broken {
			m.rejected.Add(1)
			return nil, errors.New("unit properties unavailable")
		}
	}
	return m.Manager.States(ctx, names)
}

// TestEgressUnitQueryIsolation 验证未应用 Stack 不查询 unit，单个查询异常也不影响其它出口。
func TestEgressUnitQueryIsolation(t *testing.T) {
	h, healthy := newEgressHarness(t)
	ctx := context.Background()
	broken, err := h.svc.CreateStack(ctx, h.actor, configdoc.NewStackOptions{Template: "pair", Name: "broken"})
	require.NoError(t, err)
	id, err := h.svc.Apply(ctx, h.actor, []string{broken.Name})
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	manager := &strictEgressUnits{Manager: h.units, known: map[string]bool{}}
	for _, stack := range doc.Stacks {
		manager.known[units.Name(units.Mihomo, stack.Name)] = true
	}
	pending, err := h.svc.CreateStack(ctx, h.actor, configdoc.NewStackOptions{Template: "pair", Name: "pending"})
	require.NoError(t, err)
	h.svc.Units = manager
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) {
		return []app.EgressResult{{Family: "ipv4", EgressInfo: app.EgressInfo{IP: "203.0.113.9"}}}, nil
	}
	checkEgress(t, h)
	require.Zero(t, manager.rejected.Load(), "unapplied units must not be queried")
	require.Equal(t, "unsupported", egressStatus(t, h, pending.ID).Status)
	before, err := h.svc.Store.Q.GetStackEgressHealth(ctx, broken.ID)
	require.NoError(t, err)
	manager.broken = units.Name(units.Mihomo, broken.Name)
	job := jobNamed(t, h, app.JobEgressCheck)
	require.NoError(t, h.svc.RunJobNow(ctx, h.actor, job.ID))
	h.svc.WaitJobs()
	require.Equal(t, app.RunFailed, jobNamed(t, h, app.JobEgressCheck).LastRun.Status)
	require.Equal(t, "healthy", egressStatus(t, h, healthy.ID).Status)
	require.Equal(t, "unavailable", egressStatus(t, h, broken.ID).Status)
	after, err := h.svc.Store.Q.GetStackEgressHealth(ctx, broken.ID)
	require.NoError(t, err)
	require.Equal(t, before, after, "unit query errors must not count as egress failures or pauses")
	rows, err := h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, rows)
}

// TestEgressDefaultRetrySettings 验证新实例的调度和参数默认值。
func TestEgressDefaultRetrySettings(t *testing.T) {
	h := newTrafficHarness(t)
	job := jobNamed(t, h, app.JobEgressCheck)
	require.Equal(t, "*/2 * * * *", job.Schedule)
	require.Equal(t, 3, job.Params.RetryCount)
	require.Equal(t, 12, job.Params.ProbeTimeout)
}

// TestEgressRetryPolicy 验证首次查询加额外重试的次数、间隔和任一地址族成功即停止。
func TestEgressRetryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		retries   int
		successAt int
		family    string
		ip        string
		queries   int
	}{
		{name: "first-success", retries: 3, successAt: 1, family: "ipv4", ip: "203.0.113.10", queries: 1},
		{name: "retry-ipv6-success", retries: 3, successAt: 2, family: "ipv6", ip: "2001:db8::10", queries: 2},
		{name: "last-retry-success", retries: 3, successAt: 4, family: "ipv4", ip: "203.0.113.10", queries: 4},
		{name: "three-retries-exhausted", retries: 3, queries: 4},
		{name: "no-retries", retries: 0, queries: 1},
		{name: "custom-retries", retries: 1, queries: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, stack := newEgressHarness(t)
			ctx := context.Background()
			job := jobNamed(t, h, app.JobEgressCheck)
			job.Params.RetryCount = tc.retries
			require.NoError(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: job.Enabled, Timeout: job.Timeout, Params: job.Params}))
			require.Equal(t, tc.retries, jobNamed(t, h, app.JobEgressCheck).Params.RetryCount, "zero must survive JSON serialization and default merging")
			var queryTimes []time.Time
			h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) {
				queryTimes = append(queryTimes, time.Now())
				if len(queryTimes) == tc.successAt {
					return []app.EgressResult{{Family: tc.family, EgressInfo: app.EgressInfo{IP: tc.ip}}}, nil
				}
				return []app.EgressResult{{Family: "ipv4", EgressInfo: app.EgressInfo{IP: "not-an-ip"}}}, errors.New("probe failed")
			}
			checkEgress(t, h)
			require.Len(t, queryTimes, tc.queries)
			for i := 1; i < len(queryTimes); i++ {
				require.GreaterOrEqual(t, queryTimes[i].Sub(queryTimes[i-1]), time.Second)
			}
			h.svc.DeliverNotifications(ctx)
			status := egressStatus(t, h, stack.ID)
			if tc.successAt > 0 {
				require.Equal(t, "healthy", status.Status)
				require.Zero(t, status.Failures)
				require.Empty(t, h.mails, "transient failures must not generate an alarm or recovery")
			} else {
				require.Equal(t, "down", status.Status)
				require.EqualValues(t, tc.queries, status.Failures)
				require.Len(t, h.mails, 1)
			}
		})
	}
}

// TestEgressRetryWaitCancellation 验证重试等待可被任务超时取消，不生成不完整轮次的故障。
func TestEgressRetryWaitCancellation(t *testing.T) {
	h, stack := newEgressHarness(t)
	ctx := context.Background()
	job := jobNamed(t, h, app.JobEgressCheck)
	job.Params.RetryCount = 3
	require.NoError(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: true, Timeout: 1, Params: job.Params}))
	var calls atomic.Int64
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond)
		return nil, errors.New("probe failed")
	}
	require.NoError(t, h.svc.RunJobNow(ctx, h.actor, job.ID))
	h.svc.WaitJobs()
	require.Equal(t, app.RunTimeout, jobNamed(t, h, app.JobEgressCheck).LastRun.Status)
	require.EqualValues(t, 1, calls.Load())
	require.Equal(t, "unknown", egressStatus(t, h, stack.ID).Status)
	rows, err := h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, rows)
}

// TestEgressRecoveryDuringRetry 验证已确认故障后在重试中恢复，只发送一次恢复通知。
func TestEgressRecoveryDuringRetry(t *testing.T) {
	h, stack := newEgressHarness(t)
	ctx := context.Background()
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) { return nil, nil }
	checkEgress(t, h)
	h.svc.DeliverNotifications(ctx)
	require.Len(t, h.mails, 1)
	job := jobNamed(t, h, app.JobEgressCheck)
	job.Params.RetryCount = 3
	require.NoError(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: job.Enabled, Timeout: job.Timeout, Params: job.Params}))
	var calls atomic.Int64
	h.svc.ProbeEgress = func(context.Context, deps.ProxyConfig, time.Duration) ([]app.EgressResult, error) {
		if calls.Add(1) == 2 {
			return []app.EgressResult{{Family: "ipv6", EgressInfo: app.EgressInfo{IP: "2001:db8::11"}}}, nil
		}
		return nil, nil
	}
	checkEgress(t, h)
	h.svc.DeliverNotifications(ctx)
	require.EqualValues(t, 2, calls.Load())
	require.Equal(t, "healthy", egressStatus(t, h, stack.ID).Status)
	require.Len(t, h.mails, 2)
	require.Contains(t, h.mails[1], "出口恢复")
}
