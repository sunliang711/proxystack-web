package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
)

// setPauseWindows 修改全局停服时段。
func (h *trafficHarness) setPauseWindows(t *testing.T, windows ...string) {
	t.Helper()
	ctx := context.Background()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Traffic.PauseWindows = windows
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
}

// TestPauseWindowPausesAndResumes 验证全局停服时段到点暂停、结束后恢复，不改配置也不发通知，审计汇总为一条。
func TestPauseWindowPausesAndResumes(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	// 时钟从 12:00:30 开始。
	h.setPauseWindows(t, "12:30-13:00")
	require.Empty(t, h.pauses(t))
	before := h.revisions(t)

	h.now = h.now.Add(30 * time.Minute) // 12:30:30
	h.tick(t, time.Second)
	require.Equal(t, []string{app.PauseSchedule}, h.pauses(t))
	require.Contains(t, h.calls(), "rmu "+usa1API+" vmess:24101:vmess alice.u1")
	require.True(t, h.alice(t).Enabled)
	require.Equal(t, before, h.revisions(t), "a schedule pause adds no config revision")
	usage, err := h.svc.UserUsageDetail(ctx, h.alice(t).ID)
	require.NoError(t, err)
	require.Equal(t, 13, usage.AutoPauses[0].ResumeAt.In(h.now.Location()).Hour())
	notifications, err := h.svc.ListNotifications(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, notifications, "a schedule pause sends no notification")

	h.now = h.now.Add(30 * time.Minute) // 13:00:31
	h.tick(t, time.Second)
	require.Empty(t, h.pauses(t))
	require.NotContains(t, h.generated(t), "psw-placeholder")

	page, err := h.svc.ListAudit(ctx, app.ActionUserPause, app.SystemActor.Name, 10, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.Equal(t, "解除 1 位用户的停服时段暂停：alice", page.Items[0].Summary)
	require.Equal(t, "进入停服时段，自动暂停 1 位用户：alice", page.Items[1].Summary)
}

// TestPauseWindowUserOverride 验证用户可以不受全局时段限制，或使用自己的时段。
func TestPauseWindowUserOverride(t *testing.T) {
	h := newTrafficHarness(t)
	h.setPauseWindows(t, "12:00-13:00")
	h.tick(t, time.Second)
	require.Equal(t, []string{app.PauseSchedule}, h.pauses(t))

	h.update(t, func(u *configdoc.User) { u.PauseWindowMode = configdoc.PauseWindowOff })
	require.Empty(t, h.pauses(t), "saving the user re-evaluates at once")

	h.update(t, func(u *configdoc.User) {
		u.PauseWindowMode = configdoc.PauseWindowCustom
		u.PauseWindows = []string{"14:00-15:00"}
	})
	require.Empty(t, h.pauses(t))
	h.now = h.now.Add(2 * time.Hour) // 14:00:30
	h.tick(t, time.Second)
	require.Equal(t, []string{app.PauseSchedule}, h.pauses(t))

	h.setPauseWindows(t)
	require.Equal(t, []string{app.PauseSchedule}, h.pauses(t), "custom windows do not follow the global setting")
}

// TestPauseWindowRelease 验证放行本次停服时段到这段停服结束为止有效，下一次时段重新暂停。
func TestPauseWindowRelease(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.setPauseWindows(t, "12:00-13:00")
	h.tick(t, time.Second)
	require.Equal(t, []string{app.PauseSchedule}, h.pauses(t))

	require.NoError(t, h.svc.ReleaseAutoPause(ctx, h.actor, h.alice(t).ID, app.PauseSchedule))
	require.Empty(t, h.pauses(t))
	usage, err := h.svc.UserUsageDetail(ctx, h.alice(t).ID)
	require.NoError(t, err)
	require.Equal(t, 13, usage.WindowExemptUntil.In(h.now.Location()).Hour())
	h.tick(t, 10*time.Minute)
	require.Empty(t, h.pauses(t), "the release lasts for the rest of the window")

	// 不在时段内时不能放行。
	h.now = h.now.Add(time.Hour) // 13:10
	h.tick(t, time.Second)
	err = h.svc.ReleaseAutoPause(ctx, h.actor, h.alice(t).ID, app.PauseSchedule)
	var field *app.FieldError
	require.ErrorAs(t, err, &field)

	// 次日同一时段重新暂停。
	h.now = h.now.Add(23 * time.Hour) // 次日 12:10
	h.tick(t, time.Second)
	require.Equal(t, []string{app.PauseSchedule}, h.pauses(t))
	usage, err = h.svc.UserUsageDetail(ctx, h.alice(t).ID)
	require.NoError(t, err)
	require.True(t, usage.WindowExemptUntil.IsZero())
}

// TestPauseWindowValidation 验证时段格式校验和规范化。
func TestPauseWindowValidation(t *testing.T) {
	h := newTrafficHarness(t)
	user := h.alice(t)
	user.PauseWindowMode = configdoc.PauseWindowCustom
	user.PauseWindows = []string{}
	_, err := h.svc.UpdateUser(context.Background(), h.actor, user.ID, user)
	var validation *app.ValidationError
	require.ErrorAs(t, err, &validation)
	require.Equal(t, "users[0].pause_windows", validation.Issues[0].Path)

	user.PauseWindows = []string{"08:00-08:00"}
	_, err = h.svc.UpdateUser(context.Background(), h.actor, user.ID, user)
	require.ErrorAs(t, err, &validation)

	user.PauseWindows = []string{"2:00-8:00"}
	_, err = h.svc.UpdateUser(context.Background(), h.actor, user.ID, user)
	require.NoError(t, err)
	require.Equal(t, []string{"02:00-08:00"}, h.alice(t).PauseWindows)
}

// TestPauseWindowSkipsDisabledAndUnaffectedStacks 验证停用的用户不进入停服暂停，
// 开启断开连接时只重启确实引用了被暂停用户的 stack。
func TestPauseWindowSkipsDisabledAndUnaffectedStacks(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Traffic.DisconnectOnPause = true
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
	// bob 不在任何 stack 上：他进入停服时段不应重启 usa1。
	_, err = h.svc.CreateUser(ctx, h.actor, configdoc.User{Name: "bob", Enabled: true, ResetMode: "none", Profiles: []configdoc.Profile{}})
	require.NoError(t, err)
	h.update(t, func(u *configdoc.User) { u.PauseWindowMode = configdoc.PauseWindowOff })
	calls := len(h.units.Calls)
	h.setPauseWindows(t, "12:00-13:00")
	require.Empty(t, h.pauses(t))
	require.NotContains(t, strings.Join(h.units.Calls[calls:], "\n"), "restart psw-xray@usa1.service")

	// alice 在 usa1 上：进入时段时重启。
	h.update(t, func(u *configdoc.User) { u.PauseWindowMode = configdoc.PauseWindowInherit })
	require.Equal(t, []string{app.PauseSchedule}, h.pauses(t))
	require.Contains(t, strings.Join(h.units.Calls[calls:], "\n"), "restart psw-xray@usa1.service")

	// 停用的用户不进入停服暂停。
	_, err = h.svc.SetUserPause(ctx, h.actor, h.alice(t).ID, false, nil)
	require.NoError(t, err)
	require.Empty(t, h.pauses(t))
}

// TestPauseWindowsClearedOutsideCustom 验证非自定义模式不保留时段，隐藏的无效值不会挡住保存。
func TestPauseWindowsClearedOutsideCustom(t *testing.T) {
	h := newTrafficHarness(t)
	h.update(t, func(u *configdoc.User) {
		u.PauseWindowMode = configdoc.PauseWindowInherit
		u.PauseWindows = []string{"08:00-08:00"}
	})
	require.Empty(t, h.alice(t).PauseWindows)
}

// TestPauseWindowStatus 验证停服状态：未到时段时给出下一次开始时刻，时段内给出恢复时刻和暂停人数，并统计例外用户。
func TestPauseWindowStatus(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	status, err := h.svc.PauseWindowStatus(ctx)
	require.NoError(t, err)
	require.Empty(t, status.Windows)
	require.False(t, status.Active)
	require.True(t, status.NextStart.IsZero())

	// 时钟 12:00:30：13:00 开始的时段还没到。
	h.setPauseWindows(t, "13:00-14:00", "02:00-03:00")
	status, err = h.svc.PauseWindowStatus(ctx)
	require.NoError(t, err)
	require.False(t, status.Active)
	require.Equal(t, 13, status.NextStart.In(h.now.Location()).Hour())
	require.Zero(t, status.Paused)

	h.now = h.now.Add(time.Hour)
	h.tick(t, time.Second)
	status, err = h.svc.PauseWindowStatus(ctx)
	require.NoError(t, err)
	require.True(t, status.Active)
	require.Equal(t, 14, status.ResumeAt.In(h.now.Location()).Hour())
	require.Equal(t, 1, status.Paused)

	h.update(t, func(u *configdoc.User) { u.PauseWindowMode = configdoc.PauseWindowOff })
	status, err = h.svc.PauseWindowStatus(ctx)
	require.NoError(t, err)
	require.Zero(t, status.Paused)
	require.Equal(t, 1, status.Exempt)
	require.Zero(t, status.Custom)
}
