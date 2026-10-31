package app_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/backup"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/paths"
	"proxystack-web/internal/store"
	"proxystack-web/internal/units"
	"proxystack-web/internal/xrayapi"
)

func jobNamed(t *testing.T, h *trafficHarness, kind string) app.JobView {
	t.Helper()
	jobs, err := h.svc.ListJobs(context.Background())
	require.NoError(t, err)
	for _, job := range jobs {
		if job.Kind == kind {
			return job
		}
	}
	t.Fatalf("job %s not found", kind)
	return app.JobView{}
}

// TestSchedulerRunsDueJobs 验证到期任务被执行并留下记录，错过的不补跑，停用的任务不执行。
func TestSchedulerRunsDueJobs(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	job := jobNamed(t, h, app.JobTrafficPrune)
	require.Equal(t, "0 3 * * *", job.Schedule)
	require.Len(t, job.NextRuns, 5)

	h.now = time.Date(2026, 11, 11, 2, 59, 50, 0, h.now.Location())
	h.svc.SchedulerTick(ctx)
	h.svc.WaitJobs()
	require.Nil(t, jobNamed(t, h, app.JobTrafficPrune).LastRun, "nothing is due yet")

	h.now = time.Date(2026, 11, 11, 3, 0, 5, 0, h.now.Location())
	h.svc.SchedulerTick(ctx)
	h.svc.WaitJobs()
	last := jobNamed(t, h, app.JobTrafficPrune).LastRun
	require.NotNil(t, last)
	require.Equal(t, app.RunSucceeded, last.Status)
	require.Equal(t, "schedule", last.Trigger)

	// 下次到期时间已推进，同一分钟内再检查不重复执行。
	h.svc.SchedulerTick(ctx)
	h.svc.WaitJobs()
	runs, err := h.svc.ListJobRuns(ctx, job.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)

	require.NoError(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: false, Timeout: job.Timeout}))
	h.svc.SchedulerTick(ctx)
	h.now = h.now.Add(24 * time.Hour)
	h.svc.SchedulerTick(ctx)
	h.svc.WaitJobs()
	runs, err = h.svc.ListJobRuns(ctx, job.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1, "a disabled job does not run")
}

// TestJobScheduleValidation 验证非法的 cron 表达式被拒绝，备份任务的保留份数必须为正。
func TestJobScheduleValidation(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	job := jobNamed(t, h, app.JobBackup)
	require.Error(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: "not cron", Enabled: true, Timeout: 60, Params: job.Params}))
	require.Error(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: true, Timeout: 60, Params: app.JobParams{}}))
	times, err := h.svc.JobSchedulePreview(ctx, "30 2 * * *", 5)
	require.NoError(t, err)
	require.Len(t, times, 5)
	require.Equal(t, 2, times[0].Hour(), "previews use the configured time zone")
}

// TestBackupJobKeepsRecentSnapshots 验证备份任务生成快照并只保留设定份数，手工文件不受影响。
func TestBackupJobKeepsRecentSnapshots(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	job := jobNamed(t, h, app.JobBackup)
	require.NoError(t, h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: true, Timeout: 60, Params: app.JobParams{Keep: 2}}))
	manual := filepath.Join(h.svc.Layout.BackupsDir(), "pre-restore-1.db")
	require.NoError(t, os.WriteFile(manual, []byte("x"), 0o640))
	for range 3 {
		require.NoError(t, h.svc.RunJobNow(ctx, h.actor, job.ID))
		h.svc.WaitJobs()
		require.Equal(t, app.RunSucceeded, jobNamed(t, h, app.JobBackup).LastRun.Status, jobNamed(t, h, app.JobBackup).LastRun.Log)
		h.now = h.now.Add(time.Minute)
	}
	files, err := h.svc.ListBackups()
	require.NoError(t, err)
	auto := 0
	for _, file := range files {
		if strings.HasPrefix(file.Name, "auto-") {
			auto++
			require.Equal(t, "snapshot", file.Kind)
		}
	}
	require.Equal(t, 2, auto)
	require.FileExists(t, manual)
}

// installFakeDeps 放置假的依赖文件，让实例认为依赖已安装。
func installFakeDeps(t *testing.T, h *trafficHarness) {
	t.Helper()
	for _, component := range []string{"xray", "mihomo", "geo"} {
		for _, path := range h.svc.DepFiles(component) {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
			require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho version 1.0\n"), 0o750))
		}
	}
}

// TestMigrationPackageRestoresAnotherInstance 走完整机迁移：源实例导出，目标实例恢复后配置、token、流量连续，
// 管理员和面板设置按选项处理，依赖缺失时等待，补齐后重新生成配置并拉起 stack。
func TestMigrationPackageRestoresAnotherInstance(t *testing.T) {
	ctx := context.Background()
	source := newTrafficHarness(t)
	installFakeDeps(t, source)
	source.use(t, 1000, 2000)
	source.update(t, func(u *configdoc.User) { u.DailyLimitBytes = 100 })
	require.Equal(t, []string{app.PauseDaily}, source.pauses(t), "automatic pauses travel with the package")
	sourceDoc, err := source.svc.LoadConfig(ctx)
	require.NoError(t, err)
	sourceToken := sourceDoc.Users[0].SubToken

	id, err := source.svc.ExportPackage(ctx, source.actor, app.ExportRequest{Passphrase: "correct horse", IncludeTLS: true})
	require.NoError(t, err)
	task := source.waitTask(t, id)
	require.Equal(t, app.TaskSucceeded, task.Status, task.Error)
	files, err := source.svc.ListBackups()
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "package", files[0].Kind)
	data, err := os.ReadFile(filepath.Join(source.svc.Layout.BackupsDir(), files[0].Name))
	require.NoError(t, err)

	// 目标实例：另一套配置和管理员，已有一个运行中的 stack。
	target := newTrafficHarness(t)
	target.update(t, func(u *configdoc.User) { u.Note = "target" })
	_, err = target.svc.StageRestore(ctx, bytes.NewReader(data), "wrong passphrase")
	require.Error(t, err)
	preview, err := target.svc.StageRestore(ctx, bytes.NewReader(data), "correct horse")
	require.NoError(t, err)
	require.EqualValues(t, 1, preview.Counts["users"])
	require.Greater(t, preview.Counts["traffic"], int64(0))
	require.False(t, preview.TrafficFrom.IsZero())

	targetPanel, err := store.GetPanelSettings(ctx, target.svc.Store.Q)
	require.NoError(t, err)
	restarted := false
	target.svc.RequestRestart = func() { restarted = true }
	require.NoError(t, target.svc.ExecuteRestore(ctx, target.actor, app.RestoreOptions{ExternalHost: "203.0.113.9", KeepPanel: true, KeepAdmins: true, StartProxies: true}))
	require.True(t, restarted)
	backups, err := target.svc.ListBackups()
	require.NoError(t, err)
	require.Contains(t, backups[0].Name, "pre-restore-", "the old database is backed up first")

	// 模拟重启：替换数据库后用新连接打开。
	layout := target.svc.Layout
	require.NoError(t, target.svc.Store.Close())
	replaced, err := app.ApplyPendingRestore(layout)
	require.NoError(t, err)
	require.True(t, replaced)
	replaced, err = app.ApplyPendingRestore(layout)
	require.NoError(t, err)
	require.False(t, replaced, "applying twice is a no-op")
	st, err := store.Open(layout.DBPath())
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	restored := app.New(st, layout)
	fake := units.NewFake()
	api := &xrayapi.Fake{}
	restored.Units, restored.Logs, restored.XrayAPI, restored.PortChecker, restored.SettleDelay, restored.HealthWait = fake, fake, api, nil, time.Millisecond, time.Millisecond
	restored.Now = source.svc.Now

	doc, err := restored.LoadConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, sourceToken, doc.Users[0].SubToken)
	require.Empty(t, doc.Users[0].Note, "user data comes from the package")
	require.Equal(t, "203.0.113.9", doc.Settings.Basic.ExternalHost)
	panel, err := store.GetPanelSettings(ctx, st.Q)
	require.NoError(t, err)
	require.Equal(t, targetPanel.Path, panel.Path, "panel settings are kept")
	usage, err := restored.UserUsageDetail(ctx, doc.Users[0].ID)
	require.NoError(t, err)
	require.EqualValues(t, 3000, usage.Today.Total(), "traffic history continues")
	entries, err := restored.ListAudit(ctx, app.ActionBackupRestore, target.actor.Name, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries.Items, 1, "who restored is recorded in the restored database")
	require.Len(t, usage.AutoPauses, 1)

	// 依赖缺失：配置不生成，stack 不启动。
	ctxBackground, cancel := context.WithCancel(ctx)
	require.NoError(t, restored.StartBackground(ctxBackground))
	t.Cleanup(func() { cancel(); restored.WaitBackground() })
	status, err := restored.RestoreStatusData(ctx)
	require.NoError(t, err)
	require.True(t, status.Pending)
	require.Contains(t, status.Missing, "xray")
	_, err = os.Stat(layout.GeneratedXray("usa1"))
	require.True(t, os.IsNotExist(err) || status.Pending)

	// 依赖补齐后下一次对账完成收尾。
	for _, component := range []string{"xray", "mihomo", "geo"} {
		for _, path := range restored.DepFiles(component) {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
			require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho version 1.0\n"), 0o750))
		}
	}
	jobs, err := restored.ListJobs(ctx)
	require.NoError(t, err)
	for _, job := range jobs {
		if job.Kind == app.JobReconcile {
			require.NoError(t, restored.RunJobNow(ctx, app.SystemActor, job.ID))
		}
	}
	restored.WaitJobs()
	status, err = restored.RestoreStatusData(ctx)
	require.NoError(t, err)
	require.False(t, status.Pending)
	written, err := os.ReadFile(layout.GeneratedXray("usa1"))
	require.NoError(t, err)
	require.Contains(t, string(written), "psw-placeholder", "the automatic pause still applies after restoring")
	require.Contains(t, strings.Join(fake.Calls, "\n"), "start psw-xray@usa1.service")
}

// TestMigrationPackageFreshInstall 验证全新目录恢复：管理员、面板路径来自迁移包，不加密的包不需要口令。
func TestMigrationPackageFreshInstall(t *testing.T) {
	ctx := context.Background()
	source := newTrafficHarness(t)
	sourcePanel, err := store.GetPanelSettings(ctx, source.svc.Store.Q)
	require.NoError(t, err)
	dest := filepath.Join(t.TempDir(), "package"+backup.Extension)
	require.NoError(t, source.svc.WritePackage(ctx, dest, app.ExportRequest{Plain: true}, func(string, ...any) {}))
	require.Error(t, source.svc.WritePackage(ctx, dest+"2", app.ExportRequest{Passphrase: "short"}, func(string, ...any) {}))

	layout := paths.New(t.TempDir())
	for _, dir := range layout.Dirs() {
		require.NoError(t, os.MkdirAll(dir, 0o750))
	}
	file, err := os.Open(dest)
	require.NoError(t, err)
	defer file.Close()
	manifest, err := app.RestoreFresh(ctx, layout, file, "", app.RestoreOptions{StartProxies: false})
	require.NoError(t, err)
	require.NotEmpty(t, manifest.Files)

	st, err := store.Open(layout.DBPath())
	require.NoError(t, err)
	defer st.Close()
	panel, err := store.GetPanelSettings(ctx, st.Q)
	require.NoError(t, err)
	require.Equal(t, sourcePanel.Path, panel.Path)
	count, err := st.Q.CountAdmins(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	doc, err := app.New(st, layout).LoadConfig(ctx)
	require.NoError(t, err)
	for _, stack := range doc.Stacks {
		require.False(t, stack.DesiredRunning, "--no-start keeps stacks stopped")
	}
}

// TestRestoreRejectsNewerSchema 验证迁移包的数据库版本高于当前程序时被拒绝。
func TestRestoreRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	dest := filepath.Join(t.TempDir(), "package"+backup.Extension)
	require.NoError(t, h.svc.WritePackage(ctx, dest, app.ExportRequest{Plain: true}, func(string, ...any) {}))
	dir := t.TempDir()
	file, err := os.Open(dest)
	require.NoError(t, err)
	defer file.Close()
	manifest, err := backup.Read(file, "", dir)
	require.NoError(t, err)
	database := filepath.Join(dir, backup.DatabaseName)
	st, err := store.Open(database)
	require.NoError(t, err)
	_, err = st.DB.Exec("INSERT INTO goose_db_version (version_id, is_applied) VALUES (9999, 1)")
	require.NoError(t, err)
	require.NoError(t, st.Close())
	var out bytes.Buffer
	require.NoError(t, backup.Write(&out, *manifest, []backup.File{{Name: backup.DatabaseName, Path: database}}, ""))
	_, err = h.svc.StageRestore(ctx, &out, "")
	require.ErrorContains(t, err, "upgrade proxystack-web first")
}

// TestBackupNamesCannotEscapeDirectory 验证备份文件接口只接受备份目录下的文件名。
func TestBackupNamesCannotEscapeDirectory(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	for _, name := range []string{"../data/proxystack.db", "..", "a/b.db", "missing.db", "cert.pem"} {
		_, _, err := h.svc.OpenBackup(ctx, h.actor, name)
		require.ErrorIs(t, err, app.ErrNotFound, name)
		require.ErrorIs(t, h.svc.DeleteBackup(ctx, h.actor, name), app.ErrNotFound, name)
	}
}

// TestHealthChecksReportProblems 验证健康检查发现依赖缺失和待应用变更，单项问题不影响其他项。
func TestHealthChecksReportProblems(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	checks := h.svc.HealthChecks(ctx)
	byTitle := map[string]app.HealthCheck{}
	for _, check := range checks {
		byTitle[check.Title] = check
	}
	require.Equal(t, app.HealthFail, byTitle["xray"].Status, "xray is used but not installed")
	require.Equal(t, app.HealthWarn, byTitle["geo"].Status, "mihomo runs without geo data")
	require.Equal(t, app.HealthOK, byTitle["待应用变更"].Status)
	require.Equal(t, app.HealthWarn, byTitle["最近备份"].Status)
	require.Equal(t, app.HealthOK, byTitle["HandlerService：usa1"].Status)
	require.Contains(t, byTitle["HandlerService：usa1"].Detail, "已开启")

	installFakeDeps(t, h)
	h.update(t, func(u *configdoc.User) { u.UUID = "22222222-2222-4222-8222-222222222222" })
	checks = h.svc.HealthChecks(ctx)
	for _, check := range checks {
		byTitle[check.Title] = check
	}
	require.Equal(t, app.HealthOK, byTitle["xray"].Status)
	require.Equal(t, app.HealthWarn, byTitle["待应用变更"].Status)
}

// TestAuditFilters 验证审计日志按动作前缀和操作者筛选。
func TestAuditFilters(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.update(t, func(u *configdoc.User) { u.Note = "x" })
	all, err := h.svc.ListAudit(ctx, "", "", 500, 0)
	require.NoError(t, err)
	require.Greater(t, all.Total, int64(1))
	page, err := h.svc.ListAudit(ctx, "config.user", "", 500, 0)
	require.NoError(t, err)
	require.NotEmpty(t, page.Items)
	for _, item := range page.Items {
		require.True(t, strings.HasPrefix(item.Action, "config.user"))
	}
	none, err := h.svc.ListAudit(ctx, "", "nobody", 500, 0)
	require.NoError(t, err)
	require.Zero(t, none.Total)
	require.Empty(t, none.Items)
}

// TestRestoreDoesNotWaitForDepsSourceLacked 验证源实例上没有的组件不阻塞恢复收尾。
func TestRestoreDoesNotWaitForDepsSourceLacked(t *testing.T) {
	ctx := context.Background()
	source := newTrafficHarness(t)
	for _, component := range []string{"xray", "mihomo"} {
		for _, path := range source.svc.DepFiles(component) {
			require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho version 1.0\n"), 0o750))
		}
	}
	dest := filepath.Join(t.TempDir(), "package"+backup.Extension)
	require.NoError(t, source.svc.WritePackage(ctx, dest, app.ExportRequest{Plain: true}, func(string, ...any) {}))

	target := newTrafficHarness(t)
	file, err := os.Open(dest)
	require.NoError(t, err)
	defer file.Close()
	_, err = target.svc.StageRestore(ctx, file, "")
	require.NoError(t, err)
	require.NoError(t, target.svc.StageRestoreForNextStart(ctx, app.RestoreOptions{StartProxies: true}))
	layout := target.svc.Layout
	require.NoError(t, target.svc.Store.Close())
	_, err = app.ApplyPendingRestore(layout)
	require.NoError(t, err)
	st, err := store.Open(layout.DBPath())
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	restored := app.New(st, layout)
	status, err := restored.RestoreStatusData(ctx)
	require.NoError(t, err)
	require.True(t, status.Pending)
	require.ElementsMatch(t, []string{"xray", "mihomo"}, status.Missing, "geo was not installed on the source")
}
