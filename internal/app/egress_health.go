package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/deps"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/units"
)

const (
	egressConcurrency = 4
	// 每轮首次查询后最多重试十次，重试前固定等待一秒。
	maxEgressRetries = 10
	egressRetryDelay = time.Second
)

// EgressHealth 是独立于浏览器出口缓存的后台检测结果。
type EgressHealth struct {
	StackID      int64     `json:"stack_id"`
	StackName    string    `json:"stack_name"`
	Status       string    `json:"status" enum:"unknown,healthy,failing,down,stopped,unsupported,paused,unavailable"`
	CheckedAt    time.Time `json:"checked_at"`
	FailureSince time.Time `json:"failure_since"`
	Failures     int64     `json:"failures"`
	IPv4         string    `json:"ipv4"`
	IPv6         string    `json:"ipv6"`
	IPv4Error    string    `json:"ipv4_error"`
	IPv6Error    string    `json:"ipv6_error"`
}

// EgressHealthView 包含检测任务开关及各 Stack 的状态。
type EgressHealthView struct {
	Enabled bool           `json:"enabled"`
	Stacks  []EgressHealth `json:"stacks"`
}

type egressTarget struct {
	id          int64
	name        string
	mode        string
	fingerprint string
	since       time.Time
	proxy       deps.ProxyConfig
	stateErr    error
}

func egressProxy(stack configdoc.Stack) deps.ProxyConfig {
	return listenerProxy(stack, "socks")
}

// egressTargets 必须在 runtimeMu 内调用，捕获已应用版本和实际 unit 的同一时点信息。
func (s *Service) egressTargets(ctx context.Context) ([]egressTarget, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	applied, err := s.Store.Q.ListAppliedState(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]dbq.AppliedState{}
	for _, row := range applied {
		byName[row.StackName] = row
	}
	snapshots := map[int64]*configdoc.Document{}
	targets := make([]egressTarget, 0, len(doc.Stacks))
	for _, stack := range doc.Stacks {
		target := egressTarget{id: stack.ID, name: stack.Name, mode: "unsupported"}
		if !stack.Enabled {
			target.mode = "stopped"
		} else if row, ok := byName[stack.Name]; ok {
			snapshot := snapshots[row.RevisionID]
			if snapshot == nil {
				snapshot, err = s.RevisionSnapshot(ctx, row.RevisionID)
				if err != nil {
					return nil, err
				}
				snapshots[row.RevisionID] = snapshot
			}
			index := snapshot.StackByName(stack.Name)
			if index >= 0 {
				actual := snapshot.Stacks[index]
				if actual.Mihomo.Enabled && actual.Mihomo.Socks.Enabled {
					target.fingerprint = row.MihomoSha
					target.proxy = egressProxy(actual)
					// 仅查询具备已应用检测通道的 unit，并隔离单个 Stack 的查询错误。
					name := units.Name(units.Mihomo, stack.Name)
					states, stateErr := s.Units.States(ctx, []string{name})
					state, exists := states[name]
					if stateErr != nil || !exists {
						target.mode = "unavailable"
						target.stateErr = stateErr
						if target.stateErr == nil {
							target.stateErr = fmt.Errorf("missing unit state: %s", name)
						}
					} else {
						target.since = state.Since
						target.mode = "stopped"
						if state.Active == "active" {
							target.mode = "ready"
						}
					}
				}
			}
		}
		targets = append(targets, target)
	}
	return targets, nil
}

// StackEgressHealth 只读取状态，不发起网络探测或累加失败次数。
func (s *Service) StackEgressHealth(ctx context.Context) (*EgressHealthView, error) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	targets, err := s.egressTargets(ctx)
	if err != nil {
		return nil, err
	}
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	out := &EgressHealthView{Stacks: []EgressHealth{}}
	for _, job := range jobs {
		if job.Kind == JobEgressCheck {
			out.Enabled = job.Enabled
		}
	}
	rows, err := s.Store.Q.ListStackEgressHealth(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[int64]dbq.StackEgressHealth{}
	for _, row := range rows {
		byID[row.StackID] = row
	}
	for _, target := range targets {
		row := byID[target.id]
		view := EgressHealth{StackID: target.id, StackName: target.name, Status: row.Status, Failures: row.Failures,
			IPv4: row.Ipv4, IPv6: row.Ipv6, IPv4Error: row.Ipv4Error, IPv6Error: row.Ipv6Error}
		if row.CheckedAt != 0 {
			view.CheckedAt = time.Unix(row.CheckedAt, 0)
		}
		if row.FailureSince != 0 {
			view.FailureSince = time.Unix(row.FailureSince, 0)
		}
		switch {
		case target.mode != "ready":
			view.Status = target.mode
		case !out.Enabled:
			view.Status = "paused"
		case row.Status == "" || row.Fingerprint != target.fingerprint || row.Status == "stopped" || row.Status == "unsupported":
			view.Status = "unknown"
		}
		out.Stacks = append(out.Stacks, view)
	}
	return out, nil
}

func (s *Service) probeStackEgress(ctx context.Context, target egressTarget, seconds int) ([]EgressResult, error) {
	timeout := time.Duration(seconds) * time.Second
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if s.ProbeEgress != nil {
		return s.ProbeEgress(probeCtx, target.proxy, timeout)
	}
	fetcher, err := deps.NewHTTPFetcher(target.proxy)
	if err != nil {
		return nil, err
	}
	defer fetcher.CloseIdleConnections()
	return probeEgressFamilies(probeCtx, fetcher, timeout/3), nil
}

func probeEgressFamilies(ctx context.Context, fetcher deps.Fetcher, sourceTimeout time.Duration) []EgressResult {
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// 成功后可直接返回；缓冲保证被取消的另一地址族仍能完成结果发送。
	completed := make(chan EgressResult, 2)
	for _, family := range []string{"ipv4", "ipv6"} {
		go func() {
			// 为三个回退源预留时间，同时限制每个 Stack 的整体检测时长。
			completed <- queryEgress(queryCtx, fetcher, family, sourceTimeout)
		}()
	}
	results := make([]EgressResult, 0, 2)
	for range 2 {
		result := <-completed
		results = append(results, result)
		if matchesFamily(result.IP, result.Family) {
			return results
		}
	}
	return results
}

type egressProbe struct {
	results   []EgressResult
	err       error
	attempts  int
	startedAt time.Time
}

// down 表示首次查询和全部重试都没有取得 IPv4 或 IPv6 出口地址。
func (p egressProbe) down() bool {
	ipv4, _ := egressFamily(p.results, "ipv4", p.err)
	ipv6, _ := egressFamily(p.results, "ipv6", p.err)
	return ipv4 == "" && ipv6 == ""
}

// probeEgressWithRetries 在单轮内确认故障，任一地址族成功就结束本轮。
func (s *Service) probeEgressWithRetries(ctx context.Context, target egressTarget, params JobParams, logf Logf) egressProbe {
	probe := egressProbe{startedAt: s.Now()}
	for retry := 0; retry <= params.RetryCount; retry++ {
		if retry > 0 {
			logf("Stack %s: retry %d/%d after 1 second", target.name, retry, params.RetryCount)
			timer := time.NewTimer(egressRetryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				probe.err = ctx.Err()
				return probe
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			probe.err = ctx.Err()
			return probe
		}
		probe.attempts++
		probe.results, probe.err = s.probeStackEgress(ctx, target, params.ProbeTimeout)
		ipv4, _ := egressFamily(probe.results, "ipv4", probe.err)
		ipv6, _ := egressFamily(probe.results, "ipv6", probe.err)
		if ipv4 != "" || ipv6 != "" {
			break
		}
	}
	return probe
}

func runEgressCheckJob(ctx context.Context, s *Service, params JobParams, logf Logf) error {
	if params.RetryCount < 0 || params.RetryCount > maxEgressRetries || params.ProbeTimeout < 1 || params.ProbeTimeout > 60 {
		return errors.New("invalid egress check parameters")
	}
	// 与手动执行、测试入口共用互斥，数据库中的每轮结果只能被累计一次。
	s.egressMu.Lock()
	defer s.egressMu.Unlock()
	s.runtimeMu.Lock()
	targets, err := s.egressTargets(ctx)
	s.runtimeMu.Unlock()
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	semaphore := make(chan struct{}, egressConcurrency)
targetsLoop:
	for _, target := range targets {
		if ctx.Err() != nil {
			break
		}
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			break targetsLoop
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-semaphore }()
			// 运行状态读取失败属于采集异常，保留已有出口状态与失败计数。
			if target.stateErr != nil {
				mu.Lock()
				failures = append(failures, fmt.Errorf("Stack %s: %w", target.name, target.stateErr))
				mu.Unlock()
				return
			}
			var probe egressProbe
			if target.mode == "ready" {
				probe = s.probeEgressWithRetries(ctx, target, params, logf)
			}
			if err := ctx.Err(); err != nil {
				return
			}
			recorded, err := s.recordEgressResult(ctx, target, probe)
			if err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
				return
			}
			logf("Stack %s: %s, queries=%d", target.name, target.mode, probe.attempts)
			// 重试耗尽仍取不到出口地址时本次执行记为失败，与告警邮件保持一致。
			if recorded && target.mode == "ready" && probe.down() {
				_, ipv4Error := egressFamily(probe.results, "ipv4", probe.err)
				_, ipv6Error := egressFamily(probe.results, "ipv6", probe.err)
				logf("Stack %s: IPv4 error: %s", target.name, ipv4Error)
				logf("Stack %s: IPv6 error: %s", target.name, ipv6Error)
				mu.Lock()
				failures = append(failures, fmt.Errorf("Stack %s: 出口检测失败，%d 次查询均未取得 IPv4 或 IPv6 地址", target.name, probe.attempts))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(failures...)
}

func egressFamily(results []EgressResult, family string, probeErr error) (string, string) {
	for _, result := range results {
		if result.Family != family {
			continue
		}
		if matchesFamily(result.IP, family) {
			return result.IP, ""
		}
		messages := []string{}
		for _, source := range result.Sources {
			if source.Error != "" {
				messages = append(messages, source.URL+": "+source.Error)
			}
		}
		if len(messages) > 0 {
			return "", strings.Join(messages, "; ")
		}
	}
	if probeErr != nil {
		return "", probeErr.Error()
	}
	return "", "No valid IP address returned"
}

// egressFailureLines 将存储的来源错误转换为适合纯文本邮件阅读的分行列表。
func egressFailureLines(family, message string) []string {
	lines := []string{family + "："}
	for _, item := range strings.Split(message, "; ") {
		source, detail, ok := strings.Cut(item, ": ")
		if !ok {
			lines = append(lines, "- "+item)
			continue
		}
		detail = strings.TrimPrefix(detail, `Get "`+source+`": `)
		lines = append(lines, "- "+source+"："+detail)
	}
	return lines
}

// recordEgressResult 保存一轮检测结果；结果因 Stack 已变化而被丢弃时 recorded 为 false。
func (s *Service) recordEgressResult(ctx context.Context, target egressTarget, probe egressProbe) (recorded bool, err error) {
	// 等待正在发送的邮件时不占用运行时锁，保持应用和启停操作可用。
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	// 探测过程中可能应用配置、停止或删除 Stack；丢弃这些过期结果，避免误报。
	current, err := s.egressTargets(ctx)
	if err != nil {
		return false, err
	}
	index := slices.IndexFunc(current, func(item egressTarget) bool { return item.id == target.id })
	if index < 0 {
		return false, nil
	}
	fresh := current[index]
	if fresh.mode != target.mode || fresh.fingerprint != target.fingerprint || !fresh.since.Equal(target.since) {
		return false, nil
	}
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return false, err
	}
	if settings.Basic.PanelURL == "" {
		if info, err := s.Info(ctx); err == nil {
			settings.Basic.PanelURL = info.PanelURL
		}
	}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		row, err := q.GetStackEgressHealth(ctx, target.id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		row.StackID = target.id
		if target.mode != "ready" {
			row.Status = target.mode
			row.Failures = 0
			if row.Alarmed == 0 {
				row.FailureSince = 0
			}
		} else {
			if row.Fingerprint != target.fingerprint && row.Alarmed == 0 {
				row.Failures, row.FailureSince = 0, 0
			}
			row.Fingerprint = target.fingerprint
			row.CheckedAt = s.Now().Unix()
			row.Ipv4, row.Ipv4Error = egressFamily(probe.results, "ipv4", probe.err)
			row.Ipv6, row.Ipv6Error = egressFamily(probe.results, "ipv6", probe.err)
			event := ""
			if row.Ipv4 != "" || row.Ipv6 != "" {
				row.Status, row.Failures = "healthy", 0
				if row.Alarmed != 0 {
					event = configdoc.EventEgressRecovered
				}
				row.Alarmed = 0
			} else {
				// 仅在首次查询和所有重试都失败后保存故障，不再跨调度周期累计。
				row.Failures = int64(probe.attempts)
				row.Status = "down"
				if row.Alarmed == 0 {
					row.FailureSince = probe.startedAt.Unix()
					row.Incident++
					row.Alarmed, event = 1, configdoc.EventEgressDown
				}
			}
			if event != "" {
				if err := q.SupersedeStackNotifications(ctx, target.id); err != nil {
					return err
				}
				if err := s.enqueueEgressNotification(ctx, q, settings, target, row, event); err != nil {
					return err
				}
			}
			if row.Status == "healthy" {
				row.FailureSince = 0
			}
		}
		return q.UpsertStackEgressHealth(ctx, dbq.UpsertStackEgressHealthParams{
			StackID: row.StackID, Status: row.Status, CheckedAt: row.CheckedAt, Failures: row.Failures,
			FailureSince: row.FailureSince, Incident: row.Incident, Alarmed: row.Alarmed, Fingerprint: row.Fingerprint,
			Ipv4: row.Ipv4, Ipv6: row.Ipv6, Ipv4Error: row.Ipv4Error, Ipv6Error: row.Ipv6Error,
		})
	})
	return err == nil, err
}

func (s *Service) enqueueEgressNotification(ctx context.Context, q *dbq.Queries, settings configdoc.Settings, target egressTarget, row dbq.StackEgressHealth, event string) error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	label := eventLabels[event]
	loc := location(settings)
	lines := []string{"事件：" + label, "实例：" + hostname, "Stack：" + target.name,
		"首次失败：" + time.Unix(row.FailureSince, 0).In(loc).Format(time.DateTime),
		"检测时间：" + s.Now().In(loc).Format(time.DateTime)}
	if event == configdoc.EventEgressDown {
		lines = append(lines, fmt.Sprintf("本轮查询失败次数：%d", row.Failures), "")
		lines = append(lines, egressFailureLines("IPv4", row.Ipv4Error)...)
		lines = append(lines, "")
		lines = append(lines, egressFailureLines("IPv6", row.Ipv6Error)...)
	} else {
		lines = append(lines, "故障持续时间："+s.Now().Sub(time.Unix(row.FailureSince, 0)).Round(time.Second).String(),
			"IPv4："+row.Ipv4, "IPv6："+row.Ipv6)
	}
	if settings.Basic.PanelURL != "" {
		lines = append(lines, "", "Stack 详情：", fmt.Sprintf("%s/stacks/%d", strings.TrimRight(settings.Basic.PanelURL, "/"), target.id))
	}
	status := NotifySkipped
	if settings.Notify.Enabled && slices.Contains(settings.Notify.Events, event) {
		status = NotifyPending
	}
	_, err = q.InsertNotification(ctx, dbq.InsertNotificationParams{
		At: s.Now().Unix(), Event: event, DedupKey: fmt.Sprintf("%s:stack:%d:%d", event, target.id, row.Incident),
		Subject: fmt.Sprintf("[proxystack-web] %s：%s", label, target.name), Body: strings.Join(lines, "\n") + "\n", Status: status,
		StackID: target.id, StackName: target.name, Incident: row.Incident,
	})
	return err
}

// obsoleteEgressNotification 阻止旧故障、旧恢复和已删除 Stack 的事件重新发送。
func (s *Service) obsoleteEgressNotification(ctx context.Context, item dbq.Notification) (bool, error) {
	if item.Event != configdoc.EventEgressDown && item.Event != configdoc.EventEgressRecovered {
		return false, nil
	}
	if item.StackID == 0 {
		return false, nil
	}
	row, err := s.Store.Q.GetStackEgressHealth(ctx, item.StackID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return row.Incident != item.Incident || (item.Event == configdoc.EventEgressDown) != (row.Alarmed != 0), nil
}
