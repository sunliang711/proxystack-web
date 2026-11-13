package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/units"
)

// enableHealthMail 开启邮件通知并只勾选健康事件。
func enableHealthMail(t *testing.T, h *trafficHarness) {
	t.Helper()
	ctx := context.Background()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Notify.Enabled = true
	settings.Notify.Host, settings.Notify.Port = "smtp.example.com", 587
	settings.Notify.From, settings.Notify.To = "panel@example.com", []string{"admin@example.com"}
	settings.Notify.Events = []string{configdoc.EventHealthAlert, configdoc.EventHealthRecovered}
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
}

// writeBackup 写入一个修改时间为 at 的备份文件。
func writeBackup(t *testing.T, h *trafficHarness, at time.Time) {
	t.Helper()
	dir := h.svc.Layout.BackupsDir()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	path := filepath.Join(dir, "proxystack-20261101-000000.db")
	require.NoError(t, os.WriteFile(path, []byte("backup"), 0o640))
	require.NoError(t, os.Chtimes(path, at, at))
}

// backupNotifications 返回与「最近备份」相关的健康通知，按时间从早到晚。
func backupNotifications(t *testing.T, svc *app.Service) []app.Notification {
	t.Helper()
	items, err := svc.ListNotifications(context.Background(), 100)
	require.NoError(t, err)
	out := []app.Notification{}
	for index := len(items) - 1; index >= 0; index-- {
		item := items[index]
		if strings.HasPrefix(item.Event, "health_") && strings.Contains(item.Subject, "最近备份") {
			out = append(out, item)
		}
	}
	return out
}

// TestHealthAlertRaisesAndRecovers 验证检查项连续两次异常才告警、持续期间不重复、恢复后发送恢复通知，
// 并且已告警状态在重启后保留。
func TestHealthAlertRaisesAndRecovers(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	enableHealthMail(t, h)

	// 从未备份只提示不告警。
	h.svc.CheckHealthAlerts(ctx)
	h.svc.CheckHealthAlerts(ctx)
	require.Empty(t, backupNotifications(t, h.svc))

	writeBackup(t, h, h.now.Add(-4*24*time.Hour))
	h.svc.CheckHealthAlerts(ctx)
	require.Empty(t, backupNotifications(t, h.svc), "a single failed round does not alert")
	h.svc.CheckHealthAlerts(ctx)
	items := backupNotifications(t, h.svc)
	require.Len(t, items, 1)
	require.Equal(t, configdoc.EventHealthAlert, items[0].Event)
	require.Equal(t, "系统健康异常", items[0].Label)
	require.Equal(t, app.NotifyPending, items[0].Status)

	h.svc.DeliverNotifications(ctx)
	h.mu.Lock()
	require.Contains(t, h.mails, items[0].Subject)
	h.mu.Unlock()

	// 问题持续期间不重复告警。
	for range 3 {
		h.svc.CheckHealthAlerts(ctx)
	}
	require.Len(t, backupNotifications(t, h.svc), 1)

	// 重启后从数据库读回已告警状态：恢复只需连续两次正常，且不会重新告警。
	restarted := app.New(h.svc.Store, h.svc.Layout)
	restarted.Units, restarted.Logs, restarted.XrayAPI, restarted.Now = h.units, h.units, h.api, h.svc.Now
	restarted.CheckHealthAlerts(ctx)
	require.Len(t, backupNotifications(t, restarted), 1)

	writeBackup(t, h, h.now.Add(-time.Hour))
	restarted.CheckHealthAlerts(ctx)
	require.Len(t, backupNotifications(t, restarted), 1, "a single passing round does not recover")
	restarted.CheckHealthAlerts(ctx)
	items = backupNotifications(t, restarted)
	require.Len(t, items, 2)
	require.Equal(t, configdoc.EventHealthRecovered, items[1].Event)
	require.Equal(t, "系统健康恢复", items[1].Label)

	// 异常与正常交替出现时计数清零，不发送通知。
	for _, age := range []time.Duration{4 * 24 * time.Hour, time.Hour, 4 * 24 * time.Hour, time.Hour} {
		writeBackup(t, h, h.now.Add(-age))
		restarted.CheckHealthAlerts(ctx)
	}
	require.Len(t, backupNotifications(t, restarted), 2)
}

// TestHealthAlertSkippedWhenUnsubscribed 验证未勾选健康事件时只记录、不发送。
func TestHealthAlertSkippedWhenUnsubscribed(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	writeBackup(t, h, h.now.Add(-4*24*time.Hour))
	h.svc.CheckHealthAlerts(ctx)
	h.svc.CheckHealthAlerts(ctx)
	items := backupNotifications(t, h.svc)
	require.Len(t, items, 1)
	require.Equal(t, app.NotifySkipped, items[0].Status)
}

// TestHealthCheckDatabaseSizeThreshold 验证数据库大小超过阈值时给出警告。
func TestHealthCheckDatabaseSizeThreshold(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	find := func() app.HealthCheck {
		for _, check := range h.svc.HealthChecks(ctx) {
			if check.Title == "数据库大小" {
				return check
			}
		}
		t.Fatal("missing database size check")
		return app.HealthCheck{}
	}
	require.Equal(t, app.HealthOK, find().Status)

	// 阈值设为最小值 1 MB，并写入 2 MB 数据让数据库超过阈值。
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Notify.DBWarnMB = 1
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
	_, err = h.svc.Store.DB.ExecContext(ctx, "CREATE TABLE padding (data BLOB); INSERT INTO padding VALUES (zeroblob(2097152))")
	require.NoError(t, err)
	check := find()
	require.Equal(t, app.HealthWarn, check.Status)
	require.Contains(t, check.Detail, "超过告警阈值")
}

// healthBodies 返回健康通知的正文，按时间从早到晚。
func healthBodies(t *testing.T, h *trafficHarness) []string {
	t.Helper()
	rows, err := h.svc.Store.Q.ListNotifications(context.Background(), 100)
	require.NoError(t, err)
	out := []string{}
	for index := len(rows) - 1; index >= 0; index-- {
		if strings.HasPrefix(rows[index].Event, "health_") {
			out = append(out, rows[index].Event+"\n"+rows[index].Body)
		}
	}
	return out
}

// TestHealthAlertPerUnitFinding 验证 unit 状态的每处不一致分别告警：已有告警时新出现的不一致仍会告警，恢复只针对已消失的那一处。
func TestHealthAlertPerUnitFinding(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	enableHealthMail(t, h)
	xray, mihomo := units.Name(units.Xray, "usa1"), units.Name(units.Mihomo, "usa1")
	rounds := func() {
		h.svc.CheckHealthAlerts(ctx)
		h.svc.CheckHealthAlerts(ctx)
	}

	require.NoError(t, h.units.Stop(ctx, xray))
	rounds()
	bodies := healthBodies(t, h)
	require.Len(t, bodies, 1)
	require.Contains(t, bodies[0], configdoc.EventHealthAlert)
	require.Contains(t, bodies[0], xray+" 应运行但未运行")

	require.NoError(t, h.units.Stop(ctx, mihomo))
	rounds()
	bodies = healthBodies(t, h)
	require.Len(t, bodies, 2, "a new finding alerts although the unit check is already alarmed")
	newPart, ongoingPart, ok := strings.Cut(bodies[1], "仍未恢复的问题：")
	require.True(t, ok)
	require.Contains(t, newPart, mihomo+" 应运行但未运行")
	require.Contains(t, ongoingPart, xray+" 应运行但未运行")

	require.NoError(t, h.units.Start(ctx, xray))
	rounds()
	bodies = healthBodies(t, h)
	require.Len(t, bodies, 3)
	require.Contains(t, bodies[2], configdoc.EventHealthRecovered)
	recoveredPart, ongoingPart, ok := strings.Cut(bodies[2], "仍未恢复的问题：")
	require.True(t, ok)
	require.Contains(t, recoveredPart, xray+" 应运行但未运行（异常开始于")
	require.Contains(t, recoveredPart, "当前：该问题已消失")
	require.Contains(t, ongoingPart, mihomo)
	require.NotContains(t, ongoingPart, xray)
}

// TestHealthAlertDiskEscalationAndFreeze 验证磁盘告警由警告加重为失败时再次告警，方向交替时重新计数，查询失败时保持告警状态。
func TestHealthAlertDiskEscalationAndFreeze(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	enableHealthMail(t, h)
	var free uint64
	var freeErr error
	h.svc.DiskFree = func(string) (uint64, error) { return free, freeErr }
	diskBodies := func() []string {
		out := []string{}
		for _, body := range healthBodies(t, h) {
			if strings.Contains(body, "存储 / 磁盘剩余空间") && !strings.Contains(strings.SplitN(body, "仍未恢复的问题：", 2)[0], "unit") {
				out = append(out, strings.SplitN(body, "仍未恢复的问题：", 2)[0])
			}
		}
		return out
	}
	round := func(bytes uint64, err error) {
		free, freeErr = bytes, err
		h.svc.CheckHealthAlerts(ctx)
	}
	const gb = 1 << 30

	round(4*gb, nil)
	round(4*gb, nil)
	require.Len(t, diskBodies(), 1)
	require.Contains(t, diskBodies()[0], configdoc.EventHealthAlert)

	// 加重一次、正常一次：方向改变，各自重新计数，不发送通知。
	round(gb/2, nil)
	round(6*gb, nil)
	round(gb/2, nil)
	require.Len(t, diskBodies(), 1)
	round(gb/2, nil)
	bodies := diskBodies()
	require.Len(t, bodies, 2)
	require.Contains(t, bodies[1], "由警告加重为失败")

	// 加重后回到警告不降级，也不重复告警。
	round(4*gb, nil)
	round(4*gb, nil)
	require.Len(t, diskBodies(), 2)

	// 查询失败时结果未知，保持告警，不发送恢复通知。
	for range 3 {
		round(0, errors.New("statfs failed"))
	}
	require.Len(t, diskBodies(), 2)

	round(6*gb, nil)
	round(6*gb, nil)
	bodies = diskBodies()
	require.Len(t, bodies, 3)
	require.Contains(t, bodies[2], configdoc.EventHealthRecovered)
	require.Contains(t, bodies[2], "当前：6.00 GB")
}
