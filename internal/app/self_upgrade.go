package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"

	"proxystack-web/internal/deps"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/version"
)

const (
	SelfUpgradeWaiting   = "waiting"
	SelfUpgradeUpdating  = "updating"
	SelfUpgradeSucceeded = "succeeded"
	SelfUpgradeTimeout   = "timeout"
	SelfUpgradeFailed    = "failed"
	SelfUpgradeCancelled = "cancelled"
	SelfUpgradeIdle      = "idle"

	defaultSelfUpgradeTimeoutSeconds  = 300
	defaultSelfUpgradeIntervalSeconds = 20
	minSelfUpgradeTimeoutSeconds      = 30
	maxSelfUpgradeTimeoutSeconds      = 24 * 60 * 60
	minSelfUpgradeIntervalSeconds     = 5
	maxSelfUpgradeIntervalSeconds     = 60 * 60
	selfUpgradePollInterval           = 2 * time.Second
)

var selfUpgradeVersionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

// SelfUpgradeSchedule 保存管理服务的持久化预约升级状态。
type SelfUpgradeSchedule struct {
	Target          string    `json:"target"`
	ResolvedVersion string    `json:"resolved_version,omitempty"`
	TimeoutSeconds  int       `json:"timeout_seconds"`
	IntervalSeconds int       `json:"interval_seconds"`
	Status          string    `json:"status" enum:"idle,waiting,updating,succeeded,timeout,failed,cancelled"`
	StartedAt       time.Time `json:"started_at"`
	DeadlineAt      time.Time `json:"deadline_at"`
	NextCheckAt     time.Time `json:"next_check_at"`
	LastCheckAt     time.Time `json:"last_check_at"`
	FinishedAt      time.Time `json:"finished_at"`
	LastError       string    `json:"last_error,omitempty"`
	TaskID          int64     `json:"task_id,omitempty"`
}

// SelfUpgradeRequest 配置一次持久化预约升级。
type SelfUpgradeRequest struct {
	Target          string `json:"target"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	IntervalSeconds int    `json:"interval_seconds"`
}

func (s *Service) SelfUpgradeSchedule(ctx context.Context) (*SelfUpgradeSchedule, error) {
	state, err := s.loadSelfUpgradeSchedule(ctx)
	if err != nil {
		return nil, err
	}
	if state.Status == "" {
		state.Status = SelfUpgradeIdle
	}
	return &state, nil
}

// ScheduleSelfUpgrade 持久化预约，由后台 watcher 负责轮询 Release。
func (s *Service) ScheduleSelfUpgrade(ctx context.Context, actor Actor, request SelfUpgradeRequest) (*SelfUpgradeSchedule, error) {
	request, err := normalizeSelfUpgradeRequest(request)
	if err != nil {
		return nil, err
	}
	s.selfUpgradeMu.Lock()
	defer s.selfUpgradeMu.Unlock()
	state, err := s.loadSelfUpgradeSchedule(ctx)
	if err != nil {
		return nil, err
	}
	if state.Status == SelfUpgradeWaiting || state.Status == SelfUpgradeUpdating {
		return nil, &FieldError{Field: "target", Message: "an upgrade appointment is already active"}
	}
	if s.selfUpdateRunning() {
		return nil, &FieldError{Field: "target", Message: "a management-service update is already running"}
	}
	now := s.Now()
	state = SelfUpgradeSchedule{
		Target: request.Target, TimeoutSeconds: request.TimeoutSeconds, IntervalSeconds: request.IntervalSeconds,
		Status: SelfUpgradeWaiting, StartedAt: now, DeadlineAt: now.Add(time.Duration(request.TimeoutSeconds) * time.Second), NextCheckAt: now,
	}
	if err := s.saveSelfUpgradeSchedule(ctx, state); err != nil {
		return nil, err
	}
	_ = actor
	return &state, nil
}

// CancelSelfUpgrade 取消等待中的预约，或清除已结束预约的结果；已经开始替换二进制的更新不可中断。
func (s *Service) CancelSelfUpgrade(ctx context.Context, _ Actor) (*SelfUpgradeSchedule, error) {
	s.selfUpgradeMu.Lock()
	defer s.selfUpgradeMu.Unlock()
	state, err := s.loadSelfUpgradeSchedule(ctx)
	if err != nil {
		return nil, err
	}
	if state.Status == SelfUpgradeUpdating {
		return nil, &FieldError{Field: "status", Message: "the upgrade is already being applied"}
	}
	if state.Status != SelfUpgradeWaiting {
		// 已结束的预约只保留结果供查看，清除后回到空闲状态。
		state = SelfUpgradeSchedule{Status: SelfUpgradeIdle}
		if err := s.saveSelfUpgradeSchedule(ctx, state); err != nil {
			return nil, err
		}
		return &state, nil
	}
	state.Status = SelfUpgradeCancelled
	state.FinishedAt = s.Now()
	state.NextCheckAt = time.Time{}
	if err := s.saveSelfUpgradeSchedule(ctx, state); err != nil {
		return nil, err
	}
	return &state, nil
}

func normalizeSelfUpgradeRequest(request SelfUpgradeRequest) (SelfUpgradeRequest, error) {
	request.Target = strings.TrimSpace(request.Target)
	if request.Target == "" {
		request.Target = "latest"
	}
	if request.Target != "latest" && !selfUpgradeVersionPattern.MatchString(request.Target) {
		return request, &FieldError{Field: "target", Message: "must be latest or a semantic version such as v0.3.7"}
	}
	if request.TimeoutSeconds == 0 {
		request.TimeoutSeconds = defaultSelfUpgradeTimeoutSeconds
	}
	if request.IntervalSeconds == 0 {
		request.IntervalSeconds = defaultSelfUpgradeIntervalSeconds
	}
	if request.TimeoutSeconds < minSelfUpgradeTimeoutSeconds || request.TimeoutSeconds > maxSelfUpgradeTimeoutSeconds {
		return request, &FieldError{Field: "timeout_seconds", Message: fmt.Sprintf("must be between %d and %d seconds", minSelfUpgradeTimeoutSeconds, maxSelfUpgradeTimeoutSeconds)}
	}
	if request.IntervalSeconds < minSelfUpgradeIntervalSeconds || request.IntervalSeconds > maxSelfUpgradeIntervalSeconds {
		return request, &FieldError{Field: "interval_seconds", Message: fmt.Sprintf("must be between %d and %d seconds", minSelfUpgradeIntervalSeconds, maxSelfUpgradeIntervalSeconds)}
	}
	return request, nil
}

func (s *Service) selfUpdateRunning() bool {
	s.tasks.mu.Lock()
	defer s.tasks.mu.Unlock()
	for _, task := range s.tasks.running {
		if task.Kind == "self-update" {
			return true
		}
	}
	return false
}

func (s *Service) loadSelfUpgradeSchedule(ctx context.Context) (SelfUpgradeSchedule, error) {
	var state SelfUpgradeSchedule
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		err := store.GetJSON(ctx, q, settingSelfUpgradeSchedule, &state)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	})
	return state, err
}

func (s *Service) saveSelfUpgradeSchedule(ctx context.Context, state SelfUpgradeSchedule) error {
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return store.PutJSON(ctx, q, settingSelfUpgradeSchedule, state)
	})
}

func (s *Service) runSelfUpgradeWatcher(ctx context.Context) {
	ticker := time.NewTicker(selfUpgradePollInterval)
	defer ticker.Stop()
	for {
		s.checkSelfUpgradeSchedule(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) checkSelfUpgradeSchedule(ctx context.Context) {
	state, err := s.loadSelfUpgradeSchedule(ctx)
	if err != nil || (state.Status != SelfUpgradeWaiting && state.Status != SelfUpgradeUpdating) {
		return
	}
	now := s.Now()
	if state.Status == SelfUpgradeUpdating {
		s.checkSelfUpgradeResult(ctx, state)
		return
	}
	if !now.Before(state.DeadlineAt) {
		state.Status, state.FinishedAt, state.NextCheckAt = SelfUpgradeTimeout, now, time.Time{}
		state.LastError = "目标版本在截止时间前没有发布"
		if state.Target == "latest" {
			state.LastError = "等待期内没有发布比当前更新的版本"
		}
		_ = s.saveSelfUpgradeSchedule(ctx, state)
		return
	}
	if now.Before(state.NextCheckAt) {
		return
	}
	state.LastCheckAt = now
	state.NextCheckAt = now.Add(time.Duration(state.IntervalSeconds) * time.Second)
	_ = s.saveSelfUpgradeSchedule(ctx, state)

	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	release, err := s.findSelfRelease(checkCtx, state.Target)
	cancel()
	if err != nil {
		state.LastError = err.Error()
		_ = s.saveSelfUpgradeSchedule(ctx, state)
		return
	}
	if _, digest, err := deps.SelectAsset(deps.Self, release, runtime.GOOS, runtime.GOARCH, ""); err != nil || digest == nil {
		if err != nil {
			state.LastError = err.Error()
		} else {
			state.LastError = fmt.Sprintf("版本 %s 没有当前平台的 SHA-256 校验文件", release.Tag)
		}
		_ = s.saveSelfUpgradeSchedule(ctx, state)
		return
	}
	if !deps.IsNewerVersion(version.Current().Version, release.Tag) {
		// latest 表示等待期内一旦发布更新的版本就升级：当前已是最新时继续等到截止时间。
		if state.Target == "latest" {
			state.ResolvedVersion = release.Tag
			state.LastError = ""
			_ = s.saveSelfUpgradeSchedule(ctx, state)
			return
		}
		state.Status, state.ResolvedVersion, state.FinishedAt, state.NextCheckAt = SelfUpgradeSucceeded, release.Tag, now, time.Time{}
		state.LastError = "当前已是目标版本或更新的版本，无需升级"
		_ = s.saveSelfUpgradeSchedule(ctx, state)
		return
	}

	s.selfUpgradeMu.Lock()
	defer s.selfUpgradeMu.Unlock()
	latest, loadErr := s.loadSelfUpgradeSchedule(ctx)
	if loadErr != nil || latest.Status != SelfUpgradeWaiting {
		return
	}
	taskID, updateErr := s.SelfUpdateVersion(ctx, Actor{Name: "scheduled-upgrade"}, release.Tag)
	if updateErr != nil {
		latest.Status, latest.FinishedAt, latest.LastError = SelfUpgradeFailed, s.Now(), updateErr.Error()
		_ = s.saveSelfUpgradeSchedule(ctx, latest)
		return
	}
	latest.Status, latest.ResolvedVersion, latest.TaskID, latest.LastError = SelfUpgradeUpdating, release.Tag, taskID, ""
	_ = s.saveSelfUpgradeSchedule(ctx, latest)
}

func (s *Service) checkSelfUpgradeResult(ctx context.Context, state SelfUpgradeSchedule) {
	now := s.Now()
	if strings.TrimPrefix(version.Current().Version, "v") == strings.TrimPrefix(state.ResolvedVersion, "v") {
		state.Status, state.FinishedAt, state.NextCheckAt = SelfUpgradeSucceeded, now, time.Time{}
		state.LastError = ""
		_ = s.saveSelfUpgradeSchedule(ctx, state)
		return
	}
	if state.TaskID == 0 {
		return
	}
	task, err := s.GetTask(ctx, state.TaskID)
	if err == nil && task.Status == TaskFailed {
		state.Status, state.FinishedAt, state.NextCheckAt = SelfUpgradeFailed, now, time.Time{}
		state.LastError = task.Error
		_ = s.saveSelfUpgradeSchedule(ctx, state)
	}
}

func (s *Service) findSelfRelease(ctx context.Context, target string) (deps.Release, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return deps.Release{}, err
	}
	fetcher, _, err := s.fetcher(ctx, doc, doc.Settings.DownloadProxy)
	if err != nil {
		return deps.Release{}, err
	}
	defer fetcher.CloseIdleConnections()
	return deps.FindRelease(ctx, fetcher, deps.Self, target)
}
