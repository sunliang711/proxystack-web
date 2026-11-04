package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/units"
)

func updateStackLimit(t *testing.T, h *trafficHarness, name string, limit int64) {
	t.Helper()
	doc, err := h.svc.LoadConfig(context.Background())
	require.NoError(t, err)
	stack := doc.Stacks[doc.StackByName(name)]
	stack.DailyLimitBytes = limit
	_, err = h.svc.UpdateStack(context.Background(), h.actor, stack.ID, stack)
	require.NoError(t, err)
}

func TestStackDailyLimitStopsNotifiesAndRejectsStart(t *testing.T) {
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
	updateStackLimit(t, h, "usa1", 500)

	h.use(t, 300, 300)
	calls := strings.Join(h.units.Calls, "\n")
	require.Contains(t, calls, "stop "+units.Name(units.Xray, "usa1"))
	require.Contains(t, calls, "stop "+units.Name(units.Mihomo, "usa1"))
	status, err := h.svc.RuntimeStatus(ctx)
	require.NoError(t, err)
	var usa1 app.StackRuntime
	for _, item := range status {
		if item.Name == "usa1" {
			usa1 = item
		}
	}
	require.True(t, usa1.AutoStopped)

	h.svc.DeliverNotifications(ctx)
	items, err := h.svc.ListNotifications(ctx, 10)
	require.NoError(t, err)
	var found bool
	for _, item := range items {
		if item.StackName == "usa1" && item.Event == configdoc.EventDailyLimit {
			found = true
			require.Equal(t, app.NotifySent, item.Status)
		}
	}
	require.True(t, found)

	err = h.svc.UnitAction(ctx, h.actor, "usa1", "all", "start")
	require.Error(t, err)
	require.Contains(t, err.Error(), "daily traffic limit reached")
	err = h.svc.UnitAction(ctx, h.actor, "usa1", "all", "restart")
	require.Error(t, err)
	require.Contains(t, err.Error(), "daily traffic limit reached")
}

func TestStackDailyLimitRaiseAllowsManualStartAndRecoversNextDay(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	updateStackLimit(t, h, "usa1", 500)
	h.use(t, 300, 300)

	updateStackLimit(t, h, "usa1", 1000)
	require.NoError(t, h.svc.UnitAction(ctx, h.actor, "usa1", "all", "start"))
	status, err := h.svc.RuntimeStatus(ctx)
	require.NoError(t, err)
	for _, item := range status {
		if item.Name == "usa1" {
			require.False(t, item.AutoStopped)
		}
	}

	h2 := newTrafficHarness(t)
	updateStackLimit(t, h2, "usa1", 500)
	h2.use(t, 300, 300)
	calls := len(h2.units.Calls)
	h2.now = h2.now.Add(24 * time.Hour)
	h2.svc.TrafficTick(ctx)
	recoveredCalls := strings.Join(h2.units.Calls[calls:], "\n")
	require.Contains(t, recoveredCalls, "start "+units.Name(units.Xray, "usa1"))
	require.Contains(t, recoveredCalls, "start "+units.Name(units.Mihomo, "usa1"))
	mihomoStart := strings.Index(recoveredCalls, "start "+units.Name(units.Mihomo, "usa1"))
	xrayStart := strings.Index(recoveredCalls, "start "+units.Name(units.Xray, "usa1"))
	require.Less(t, mihomoStart, xrayStart)
	status, err = h2.svc.RuntimeStatus(ctx)
	require.NoError(t, err)
	for _, item := range status {
		if item.Name == "usa1" {
			require.False(t, item.AutoStopped)
		}
	}
}

func TestStackDailyLimitDoesNotRecoverManuallyStoppedStack(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.use(t, 300, 300)
	require.NoError(t, h.svc.UnitAction(ctx, h.actor, "usa1", "all", "stop"))
	updateStackLimit(t, h, "usa1", 500)
	h.svc.TrafficTick(ctx)

	calls := len(h.units.Calls)
	h.now = h.now.Add(24 * time.Hour)
	h.svc.TrafficTick(ctx)
	recoveredCalls := strings.Join(h.units.Calls[calls:], "\n")
	require.NotContains(t, recoveredCalls, "start "+units.Name(units.Xray, "usa1"))
	require.NotContains(t, recoveredCalls, "start "+units.Name(units.Mihomo, "usa1"))
}

func TestStackDailyLimitRecoveryRetriesAfterStartFailure(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	updateStackLimit(t, h, "usa1", 500)
	h.use(t, 300, 300)
	h.units.FailStart[units.Name(units.Xray, "usa1")] = true
	h.now = h.now.Add(24 * time.Hour)
	h.svc.TrafficTick(ctx)

	status, err := h.svc.RuntimeStatus(ctx)
	require.NoError(t, err)
	for _, item := range status {
		if item.Name == "usa1" {
			require.True(t, item.AutoStopped)
		}
	}
	h.units.FailStart[units.Name(units.Xray, "usa1")] = false
	h.svc.TrafficTick(ctx)
	status, err = h.svc.RuntimeStatus(ctx)
	require.NoError(t, err)
	for _, item := range status {
		if item.Name == "usa1" {
			require.False(t, item.AutoStopped)
		}
	}
}

func TestStackDailyLimitManualStopDuringAutoStopIsNotRecovered(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	updateStackLimit(t, h, "usa1", 500)
	h.use(t, 300, 300)
	require.NoError(t, h.svc.UnitAction(ctx, h.actor, "usa1", "xray", "stop"))
	calls := len(h.units.Calls)
	h.now = h.now.Add(24 * time.Hour)
	h.svc.TrafficTick(ctx)
	recoveredCalls := strings.Join(h.units.Calls[calls:], "\n")
	require.NotContains(t, recoveredCalls, "start "+units.Name(units.Xray, "usa1"))
	require.Contains(t, recoveredCalls, "start "+units.Name(units.Mihomo, "usa1"))
}
