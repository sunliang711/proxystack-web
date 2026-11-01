package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/deps"
	"proxystack-web/internal/store/dbq"
)

// 定时任务类型。
const (
	JobTrafficPrune = "traffic_prune"
	JobBackup       = "db_backup"
	JobGeoUpdate    = "geo_update"
	JobDepsCheck    = "deps_check"
	JobDepsUpgrade  = "deps_upgrade"
	JobRemoteSync   = "remote_sync"
	JobReconcile    = "reconcile"
	JobEgressCheck  = "egress_check"
)

// 执行状态。
const (
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunSkipped   = "skipped"
	RunTimeout   = "timeout"
)

// 定时任务的审计动作。
const (
	ActionJobUpdate = "job.update"
	ActionJobRun    = "job.run"
)

const (
	// schedulerInterval 是检查到期任务的间隔。cron 的最小粒度是一分钟。
	schedulerInterval = 5 * time.Second
	// jobRunsKept 是每个任务保留的执行记录数。
	jobRunsKept = 200
	// jobLogLimit 是单次执行保留的日志长度。
	jobLogLimit = 16 << 10
)

// jobType 是任务注册表的一项。任务类型在代码中定义，每种类型一个实例，内置且不可删除。
type jobType struct {
	Kind        string
	Name        string
	Description string
	Schedule    string
	Timeout     int
	// Params 是参数默认值。
	Params JobParams
	Run    func(ctx context.Context, s *Service, params JobParams, logf Logf) error
}

// JobParams 是备份保留和出口检测任务的参数。
type JobParams struct {
	Keep int `json:"keep,omitempty" minimum:"0" doc:"数据库备份保留份数"`
	FailureThreshold int `json:"failure_threshold,omitempty" minimum:"0" maximum:"100" doc:"出口连续失败告警阈值"`
	ProbeTimeout int `json:"probe_timeout,omitempty" minimum:"0" maximum:"60" doc:"每个 Stack 出口检测总超时（秒）"`
}

// jobTypes 是任务注册表，顺序决定列表页的显示顺序。
var jobTypes = []jobType{
	{Kind: JobBackup, Name: "数据库备份", Description: "生成数据库快照到备份目录，超出保留份数的旧快照被删除",
		Schedule: "0 4 * * *", Timeout: 600, Params: JobParams{Keep: 7}, Run: runBackupJob},
	{Kind: JobTrafficPrune, Name: "流量数据清理", Description: "按保留策略删除过期的流量明细和通知记录",
		Schedule: "0 3 * * *", Timeout: 600, Run: func(ctx context.Context, s *Service, _ JobParams, logf Logf) error {
			s.PruneNow(ctx)
			logf("清理完成")
			return nil
		}},
	{Kind: JobGeoUpdate, Name: "geo 数据更新", Description: "下载并校验 geo 数据，成功后重启使用 geo 的 mihomo",
		Schedule: "0 5 * * 1", Timeout: 900, Run: func(ctx context.Context, s *Service, _ JobParams, logf Logf) error {
			return s.installAndWait(ctx, deps.Geo, logf)
		}},
	{Kind: JobDepsCheck, Name: "依赖版本检查", Description: "只检查不安装，有新版本时在「依赖」页提示",
		Schedule: "0 6 * * *", Timeout: 120, Run: func(ctx context.Context, s *Service, _ JobParams, logf Logf) error {
			if err := s.CheckDepUpdates(ctx); err != nil {
				return err
			}
			logf("检查完成")
			return nil
		}},
	{Kind: JobDepsUpgrade, Name: "依赖自动升级", Description: "在维护时间窗内升级系统设置中开启了自动升级的 xray、mihomo",
		Schedule: "*/30 * * * *", Timeout: 900, Run: runDepsUpgradeJob},
	{Kind: JobRemoteSync, Name: "远端节点源同步", Description: "拉取远端实例的节点",
		Schedule: "*/30 * * * *", Timeout: 120, Run: func(ctx context.Context, s *Service, _ JobParams, logf Logf) error {
			if err := s.SyncRemoteSources(ctx, 0); err != nil {
				return err
			}
			logf("同步完成")
			return nil
		}},
	{Kind: JobReconcile, Name: "对账", Description: "核对 unit 状态、生成物与期望状态，只报告不改动",
		Schedule: "*/5 * * * *", Timeout: 60, Run: func(ctx context.Context, s *Service, _ JobParams, logf Logf) error {
			s.resumeAfterRestore(ctx)
			// 运行期间只报告，不反复拉起崩溃的 unit（由 systemd 的 Restart=on-failure 负责）。
			findings, err := s.Reconcile(ctx, false)
			if err != nil {
				return err
			}
			for _, finding := range findings {
				logf("%s", finding)
			}
			if len(findings) == 0 {
				logf("状态一致")
			}
			return nil
		}},
	{Kind: JobEgressCheck, Name: "Stack 出口检测", Description: "检测运行中 Stack 的 IPv4/IPv6 出口，连续失败告警、恢复后通知；仅支持已应用的 Mihomo SOCKS 监听",
		Schedule: "* * * * *", Timeout: 120, Params: JobParams{FailureThreshold: 2, ProbeTimeout: 12}, Run: runEgressCheckJob},
}

func jobTypeOf(kind string) *jobType {
	for index := range jobTypes {
		if jobTypes[index].Kind == kind {
			return &jobTypes[index]
		}
	}
	return nil
}

// schedulerState 是调度器的内存状态：各任务的下次执行时间和正在执行的任务。
type schedulerState struct {
	mu      sync.Mutex
	next    map[string]time.Time
	running map[string]bool
	logs    map[int64]string
	wg      sync.WaitGroup
}

// cronParser 解析五段式 cron 表达式。
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// JobSchedulePreview 校验 cron 表达式并返回未来的执行时间，按设置时区计算。
func (s *Service) JobSchedulePreview(ctx context.Context, schedule string, count int) ([]time.Time, error) {
	parsed, err := cronParser.Parse(schedule)
	if err != nil {
		return nil, &FieldError{Field: "schedule", Message: "invalid cron expression: " + err.Error()}
	}
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, count)
	at := s.Now().In(location(settings))
	for range count {
		if at = parsed.Next(at); at.IsZero() {
			break
		}
		out = append(out, at)
	}
	return out, nil
}

// seedJobs 为每种任务类型写入默认实例，已有的不覆盖。
func (s *Service) seedJobs(ctx context.Context) error {
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		for _, kind := range jobTypes {
			params, err := json.Marshal(kind.Params)
			if err != nil {
				return err
			}
			if err := q.InsertJob(ctx, dbq.InsertJobParams{
				Kind: kind.Kind, Schedule: kind.Schedule, Enabled: 1, Timeout: int64(kind.Timeout), Params: string(params),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// SchedulerTick 检查到期的任务并在后台执行。服务停机期间错过的执行不补跑。
func (s *Service) SchedulerTick(ctx context.Context) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		slog.Warn("scheduler", "err", err)
		return
	}
	var jobs []dbq.Job
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		jobs, err = q.ListJobs(ctx)
		return err
	})
	if err != nil {
		slog.Warn("scheduler", "err", err)
		return
	}
	now := s.Now().In(location(settings))
	for _, job := range jobs {
		schedule, err := cronParser.Parse(job.Schedule)
		if err != nil {
			slog.Warn("scheduler", "job", job.Kind, "err", err)
			continue
		}
		s.scheduler.mu.Lock()
		if s.scheduler.next == nil {
			s.scheduler.next = map[string]time.Time{}
		}
		next, known := s.scheduler.next[job.Kind]
		due := known && !now.Before(next)
		if !known || due {
			s.scheduler.next[job.Kind] = schedule.Next(now)
		}
		s.scheduler.mu.Unlock()
		if due && job.Enabled != 0 {
			s.startJob(ctx, job, "schedule")
		}
	}
}

// startJob 在后台执行任务。同一任务不并发：上一次未结束时跳过本次并记录。
func (s *Service) startJob(ctx context.Context, job dbq.Job, trigger string) {
	s.scheduler.mu.Lock()
	if s.scheduler.running == nil {
		s.scheduler.running = map[string]bool{}
	}
	if s.scheduler.running[job.Kind] {
		s.scheduler.mu.Unlock()
		s.recordRun(ctx, job.ID, RunSkipped, trigger, "上一次执行尚未结束，跳过本次")
		return
	}
	s.scheduler.running[job.Kind] = true
	s.scheduler.wg.Add(1)
	s.scheduler.mu.Unlock()
	go func() {
		defer s.scheduler.wg.Done()
		defer func() {
			s.scheduler.mu.Lock()
			delete(s.scheduler.running, job.Kind)
			s.scheduler.mu.Unlock()
		}()
		s.executeJob(ctx, job, trigger)
	}()
}

func (s *Service) recordRun(ctx context.Context, jobID int64, status, trigger, message string) {
	now := s.Now().Unix()
	var runID int64
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		if runID, err = q.InsertJobRun(ctx, dbq.InsertJobRunParams{
			JobID: jobID, StartedAt: now, FinishedAt: now, Status: status, TriggerBy: trigger, Log: message + "\n",
		}); err != nil {
			return err
		}
		return q.PruneJobRuns(ctx, dbq.PruneJobRunsParams{Job: jobID, Keep: jobRunsKept})
	})
	if err != nil {
		slog.Warn("record job run", "err", err)
	} else {
		s.publish("job", JobTaskEvent{ID: runID, Status: status})
	}
}

func (s *Service) executeJob(ctx context.Context, job dbq.Job, trigger string) {
	kind := jobTypeOf(job.Kind)
	if kind == nil {
		return
	}
	var runID int64
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		runID, err = q.InsertJobRun(ctx, dbq.InsertJobRunParams{
			JobID: job.ID, StartedAt: s.Now().Unix(), Status: RunRunning, TriggerBy: trigger,
		})
		return err
	})
	if err != nil {
		slog.Warn("start job", "job", job.Kind, "err", err)
		return
	}
	s.scheduler.mu.Lock()
	if s.scheduler.logs == nil {
		s.scheduler.logs = map[int64]string{}
	}
	s.scheduler.logs[runID] = ""
	s.scheduler.mu.Unlock()
	defer func() {
		s.scheduler.mu.Lock()
		delete(s.scheduler.logs, runID)
		s.scheduler.mu.Unlock()
	}()
	s.publish("job", JobTaskEvent{ID: runID, Status: RunRunning})
	var log bytes.Buffer
	var logMu sync.Mutex
	logf := func(format string, args ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		if log.Len() < jobLogLimit {
			fmt.Fprintf(&log, format+"\n", args...)
			s.scheduler.mu.Lock()
			s.scheduler.logs[runID] = log.String()
			s.scheduler.mu.Unlock()
			s.publish("job", JobTaskEvent{ID: runID, Status: RunRunning})
		}
	}
	params := kind.Params
	if err := json.Unmarshal([]byte(job.Params), &params); err != nil {
		logf("参数无效：%v", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(job.Timeout)*time.Second)
	defer cancel()
	status := RunSucceeded
	if err := kind.Run(runCtx, s, params, logf); err != nil {
		status = RunFailed
		if errors.Is(err, context.DeadlineExceeded) || runCtx.Err() == context.DeadlineExceeded {
			status = RunTimeout
		}
		logf("%v", err)
		slog.Warn("job failed", "job", job.Kind, "err", err)
	}
	finish, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelFinish()
	logMu.Lock()
	text := log.String()
	logMu.Unlock()
	err = s.Store.InTx(finish, func(q *dbq.Queries) error {
		if err := q.FinishJobRun(finish, dbq.FinishJobRunParams{FinishedAt: s.Now().Unix(), Status: status, Log: text, ID: runID}); err != nil {
			return err
		}
		return q.PruneJobRuns(finish, dbq.PruneJobRunsParams{Job: job.ID, Keep: jobRunsKept})
	})
	if err != nil {
		slog.Warn("finish job", "job", job.Kind, "err", err)
	} else {
		s.publish("job", JobTaskEvent{ID: runID, Status: status})
	}
}

func (s *Service) runScheduler(ctx context.Context) {
	if err := s.seedJobs(ctx); err != nil {
		slog.Error("seed jobs", "err", err)
	}
	// 中断恢复只在调度器启动时执行，读取计划列表不能终止运行中的任务记录。
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error { return q.MarkInterruptedJobRuns(ctx, s.Now().Unix()) }); err != nil {
		slog.Error("recover interrupted jobs", "err", err)
	}
	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()
	for {
		s.SchedulerTick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// WaitJobs 等待正在执行的任务结束。
func (s *Service) WaitJobs() { s.scheduler.wg.Wait() }

// JobRun 是一次执行记录。
type JobRun struct {
	ID         int64     `json:"id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status" enum:"running,succeeded,failed,skipped,timeout"`
	Trigger    string    `json:"trigger" enum:"schedule,manual"`
	Log        string    `json:"log"`
}

// JobView 是定时任务页的一行。
type JobView struct {
	ID          int64     `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Schedule    string    `json:"schedule"`
	Default     string    `json:"default_schedule"`
	Enabled     bool      `json:"enabled"`
	Timeout     int       `json:"timeout"`
	Params      JobParams `json:"params"`
	// HasKeep 表示任务有「保留份数」参数。
	HasKeep bool `json:"has_keep"`
	// NextRuns 是未来的执行时间。
	NextRuns []time.Time `json:"next_runs"`
	LastRun  *JobRun     `json:"last_run,omitempty"`
}

func jobRunView(row dbq.JobRun) JobRun {
	run := JobRun{ID: row.ID, StartedAt: time.Unix(row.StartedAt, 0), Status: row.Status, Trigger: row.TriggerBy, Log: row.Log}
	if row.FinishedAt != 0 {
		run.FinishedAt = time.Unix(row.FinishedAt, 0)
	}
	return run
}

// ListJobs 返回全部定时任务、未来 5 次执行时间和最近一次执行结果。
func (s *Service) ListJobs(ctx context.Context) ([]JobView, error) {
	if err := s.seedJobs(ctx); err != nil {
		return nil, err
	}
	var rows []dbq.Job
	last := map[int64]dbq.JobRun{}
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		if rows, err = q.ListJobs(ctx); err != nil {
			return err
		}
		runs, err := q.LatestJobRuns(ctx)
		for _, run := range runs {
			last[run.JobID] = run
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]JobView, 0, len(rows))
	for _, row := range rows {
		kind := jobTypeOf(row.Kind)
		if kind == nil {
			continue
		}
		view := JobView{
			ID: row.ID, Kind: row.Kind, Name: kind.Name, Description: kind.Description, Schedule: row.Schedule, Default: kind.Schedule,
			Enabled: row.Enabled != 0, Timeout: int(row.Timeout), Params: kind.Params, HasKeep: kind.Params.Keep > 0, NextRuns: []time.Time{},
		}
		_ = json.Unmarshal([]byte(row.Params), &view.Params)
		if next, err := s.JobSchedulePreview(ctx, row.Schedule, 5); err == nil {
			view.NextRuns = next
		}
		if run, ok := last[row.ID]; ok {
			item := jobRunView(run)
			view.LastRun = &item
		}
		out = append(out, view)
	}
	return out, nil
}

// JobUpdate 是可修改的任务字段。
type JobUpdate struct {
	Schedule string    `json:"schedule"`
	Enabled  bool      `json:"enabled"`
	Timeout  int       `json:"timeout" minimum:"1"`
	Params   JobParams `json:"params"`
}

// UpdateJob 修改任务的计划、启用状态、超时和参数。
func (s *Service) UpdateJob(ctx context.Context, actor Actor, id int64, update JobUpdate) error {
	if _, err := s.JobSchedulePreview(ctx, update.Schedule, 1); err != nil {
		return err
	}
	if update.Timeout < 1 {
		return &FieldError{Field: "timeout", Message: "must be at least 1 second"}
	}
	var kind string
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		job, err := q.GetJob(ctx, id)
		if err != nil {
			return err
		}
		kind = job.Kind
		definition := jobTypeOf(kind)
		if definition == nil {
			return ErrNotFound
		}
		if definition.Params.Keep > 0 && update.Params.Keep < 1 {
			return &FieldError{Field: "params.keep", Message: "must be at least 1"}
		}
		if kind == JobEgressCheck {
			if update.Params.FailureThreshold < 1 || update.Params.FailureThreshold > 100 {
				return &FieldError{Field: "params.failure_threshold", Message: "must be between 1 and 100"}
			}
			if update.Params.ProbeTimeout < 1 || update.Params.ProbeTimeout > 60 {
				return &FieldError{Field: "params.probe_timeout", Message: "must be between 1 and 60 seconds"}
			}
		}
		params, err := json.Marshal(update.Params)
		if err != nil {
			return err
		}
		var enabled int64
		if update.Enabled {
			enabled = 1
		}
		if err := q.UpdateJob(ctx, dbq.UpdateJobParams{
			Schedule: update.Schedule, Enabled: enabled, Timeout: int64(update.Timeout), Params: string(params), ID: id,
		}); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionJobUpdate, Object: "job:" + kind,
			Summary: fmt.Sprintf("修改定时任务 %s：%s，启用=%t", definition.Name, update.Schedule, update.Enabled),
		})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	s.scheduler.mu.Lock()
	delete(s.scheduler.next, kind)
	s.scheduler.mu.Unlock()
	return nil
}

// RunJobNow 立即在后台执行任务。任务脱离请求的生命周期运行。
func (s *Service) RunJobNow(ctx context.Context, actor Actor, id int64) error {
	var job dbq.Job
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		if job, err = q.GetJob(ctx, id); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionJobRun, Object: "job:" + job.Kind, Summary: "立即执行定时任务",
		})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	s.startJob(context.WithoutCancel(ctx), job, "manual")
	return nil
}

// ListJobRuns 返回任务的执行记录，新的在前。
func (s *Service) ListJobRuns(ctx context.Context, id int64) ([]JobRun, error) {
	var rows []dbq.JobRun
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		rows, err = q.ListJobRuns(ctx, dbq.ListJobRunsParams{JobID: id, Limit: jobRunsKept})
		return err
	})
	out := make([]JobRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, jobRunView(row))
	}
	return out, err
}

// installAndWait 安装组件并等待后台作业结束。
func (s *Service) installAndWait(ctx context.Context, component string, logf Logf) error {
	id, err := s.InstallDep(ctx, SystemActor, InstallRequest{Component: component, Version: "latest", Restart: RestartSequential})
	if err != nil {
		return err
	}
	logf("已开始安装 %s（作业 #%d）", component, id)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		task, err := s.GetTask(ctx, id)
		if err != nil {
			return err
		}
		switch task.Status {
		case TaskRunning:
		case TaskSucceeded:
			logf("%s 安装完成", component)
			return nil
		default:
			return fmt.Errorf("install %s: %s", component, task.Error)
		}
	}
}

// inWindow 判断时刻是否落在 HH:MM 表示的时间窗内，窗口可以跨午夜。
func inWindow(at time.Time, start, end string) bool {
	parse := func(text string) (int, bool) {
		value, err := time.Parse("15:04", text)
		return value.Hour()*60 + value.Minute(), err == nil
	}
	from, ok1 := parse(start)
	to, ok2 := parse(end)
	if !ok1 || !ok2 {
		return false
	}
	minute := at.Hour()*60 + at.Minute()
	if from <= to {
		return minute >= from && minute < to
	}
	return minute >= from || minute < to
}

// runDepsUpgradeJob 在维护时间窗内升级开启了自动升级且有新版本的组件。
func runDepsUpgradeJob(ctx context.Context, s *Service, _ JobParams, logf Logf) error {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return err
	}
	wanted := map[string]bool{deps.Xray: settings.Deps.AutoUpgradeXray, deps.Mihomo: settings.Deps.AutoUpgradeMihomo}
	if !wanted[deps.Xray] && !wanted[deps.Mihomo] {
		logf("未开启自动升级")
		return nil
	}
	if !inWindow(s.Now().In(location(settings)), settings.Deps.WindowStart, settings.Deps.WindowEnd) {
		logf("不在维护时间窗内（%s–%s）", settings.Deps.WindowStart, settings.Deps.WindowEnd)
		return nil
	}
	if err := s.CheckDepUpdates(ctx); err != nil {
		return err
	}
	status, err := s.DepsStatus(ctx)
	if err != nil {
		return err
	}
	var failures []string
	for _, dep := range status {
		if !wanted[dep.Component] || !dep.UpdateAvailable {
			continue
		}
		if err := s.installAndWait(ctx, dep.Component, logf); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}
