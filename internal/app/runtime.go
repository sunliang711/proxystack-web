package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/domain"
	"proxystack-web/internal/generator/xray"
	"proxystack-web/internal/runtime"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/units"
)

// 运行时相关的审计动作。
const (
	ActionRuntimeApply  = "runtime.apply"
	ActionRuntimeUnit   = "runtime.unit"
	ActionStackAutoStop = "stack.auto_stop"
	ActionUserPause     = "user.pause"
)

// componentOf 把引用图里的组件名（clash）映射为 unit 组件名（mihomo）。
func componentOf(node string) string {
	if node == "clash" {
		return units.Mihomo
	}
	return node
}

func (s *Service) disk() runtime.Disk { return runtime.Disk{Layout: s.Layout} }

// PendingView 是待应用变更：当前配置渲染出的生成物与磁盘上生成物的差异。
type PendingView struct {
	Revision int64            `json:"revision"`
	Changes  []runtime.Change `json:"changes"`
	Error    string           `json:"error,omitempty"`
}

// Pending 计算待应用变更。
func (s *Service) Pending(ctx context.Context) (*PendingView, error) {
	doc, revision, err := s.loadWithRevision(ctx)
	if err != nil {
		return nil, err
	}
	view := &PendingView{Revision: revision, Changes: []runtime.Change{}}
	rendered, err := runtime.Render(doc, s.autoPaused(ctx))
	if err != nil {
		view.Error = err.Error()
		return view, nil
	}
	current, err := s.readAllGenerated(rendered.Files)
	if err != nil {
		return nil, err
	}
	view.Changes = runtime.Diff(rendered.Files, current)
	return view, nil
}

func (s *Service) loadWithRevision(ctx context.Context) (*configdoc.Document, int64, error) {
	var doc *configdoc.Document
	var revision int64
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		if doc, err = store.LoadDocument(ctx, q); err != nil {
			return err
		}
		revision, err = latestRevision(ctx, q)
		return err
	})
	return doc, revision, err
}

func latestRevision(ctx context.Context, q *dbq.Queries) (int64, error) {
	value, err := q.LatestConfigRevisionID(ctx)
	if err != nil {
		return 0, err
	}
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case nil:
		return 0, nil
	default:
		return strconv.ParseInt(fmt.Sprint(typed), 10, 64)
	}
}

// readAllGenerated 读取 desired 中的 stack 以及磁盘上已有的全部 stack 的生成物。
func (s *Service) readAllGenerated(desired map[string]runtime.Files) (map[string]runtime.Files, error) {
	names, err := s.disk().Stacks()
	if err != nil {
		return nil, err
	}
	for name := range desired {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	current := map[string]runtime.Files{}
	for _, name := range names {
		files, err := s.disk().Read(name)
		if err != nil {
			return nil, err
		}
		if files.Xray != nil || files.Mihomo != nil {
			current[name] = files
		}
	}
	return current, nil
}

// Apply 在后台作业中应用配置：检查 → 写生成物 → 按依赖顺序启停 unit → 检查运行状态。
// only 为空时应用全部 stack。
func (s *Service) Apply(ctx context.Context, actor Actor, only []string) (int64, error) {
	title := "应用全部配置"
	if len(only) > 0 {
		title = "应用 " + strings.Join(only, "、")
	}
	return s.startTask("apply", title, actor.Name, func(ctx context.Context, logf Logf) error {
		s.runtimeMu.Lock()
		defer s.runtimeMu.Unlock()
		return s.apply(ctx, actor, only, logf)
	})
}

func (s *Service) apply(ctx context.Context, actor Actor, only []string, logf Logf) error {
	// 重启前补采：xray 重启后计数清零。放在写生成物之前，API 端口变化时仍能读到运行中的实例。
	s.collectTraffic(ctx)
	doc, revision, err := s.loadWithRevision(ctx)
	if err != nil {
		return err
	}
	rendered, err := runtime.Render(doc, s.autoPaused(ctx))
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	current, err := s.readAllGenerated(rendered.Files)
	if err != nil {
		return err
	}
	selected := func(stack string) bool { return len(only) == 0 || slices.Contains(only, stack) }
	changed := map[string]bool{}
	removed := map[string]bool{}
	for _, change := range runtime.Diff(rendered.Files, current) {
		if !selected(change.Stack) {
			continue
		}
		changed[units.Name(change.Component, change.Stack)] = true
		if _, ok := rendered.Files[change.Stack]; !ok {
			removed[change.Stack] = true
		}
		logf("%s %s %s", map[string]string{runtime.ChangeCreate: "新增", runtime.ChangeUpdate: "更新", runtime.ChangeDelete: "删除"}[change.Kind], change.Stack, change.Component)
	}
	if len(changed) == 0 {
		logf("生成物没有变化")
	}

	// 1. 用已安装的二进制检查新配置，任何一个不被接受就中止，磁盘上的文件保持不变。
	for name, files := range rendered.Files {
		if !selected(name) {
			continue
		}
		for _, component := range runtime.Components {
			content := files.Get(component)
			if content == nil || !changed[units.Name(component, name)] {
				continue
			}
			err := s.Checker.Check(ctx, component, name, content)
			switch {
			case errors.Is(err, runtime.ErrBinaryMissing):
				logf("跳过 %s %s 的配置检查：%s 尚未安装", name, component, component)
			case err != nil:
				return err
			default:
				logf("%s %s 配置检查通过", name, component)
			}
		}
	}

	// 2. 写生成物并记录应用状态。
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		for name, files := range rendered.Files {
			if !selected(name) {
				continue
			}
			if err := s.disk().Write(name, files); err != nil {
				return err
			}
			if err := q.UpsertAppliedState(ctx, dbq.UpsertAppliedStateParams{
				StackName: name, RevisionID: revision, XraySha: runtime.Hash(files.Xray),
				MihomoSha: runtime.Hash(files.Mihomo), AppliedAt: s.Now().Unix(),
			}); err != nil {
				return err
			}
		}
		for name := range removed {
			if err := s.disk().Write(name, runtime.Files{}); err != nil {
				return err
			}
			if err := q.DeleteAppliedState(ctx, name); err != nil {
				return err
			}
		}
		summary := fmt.Sprintf("应用版本 #%d", revision)
		if len(only) > 0 {
			summary += "：" + strings.Join(only, "、")
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: actor.Name, SourceIP: actor.IP, Action: ActionRuntimeApply, Object: "runtime", Summary: summary})
	})
	if err != nil {
		return err
	}
	logf("已写入生成物（版本 #%d）", revision)

	// 3. 启停 unit：先停掉不再需要的，再按依赖顺序启动或重启。
	desired := map[string]bool{}
	autoStopped, err := s.currentStackAutoStops(ctx)
	if err != nil {
		return err
	}
	for _, stack := range doc.Stacks {
		desired[stack.Name] = stack.Enabled && stack.DesiredRunning && !autoStopped[stack.ID]
	}
	allUnits := []string{}
	for name := range current {
		if selected(name) {
			allUnits = append(allUnits, units.Name(units.Xray, name), units.Name(units.Mihomo, name))
		}
	}
	for name := range rendered.Files {
		if selected(name) && current[name].Xray == nil && current[name].Mihomo == nil {
			allUnits = append(allUnits, units.Name(units.Xray, name), units.Name(units.Mihomo, name))
		}
	}
	states, err := s.Units.States(ctx, allUnits)
	if err != nil {
		return err
	}
	var failures []string
	for _, unit := range allUnits {
		stack, component := splitUnit(unit)
		files, exists := rendered.Files[stack]
		keep := exists && files.Get(component) != nil && desired[stack]
		if !keep && states[unit].Running() {
			logf("停止 %s", unit)
			if err := s.Units.Stop(ctx, unit); err != nil {
				failures = append(failures, err.Error())
			}
		}
	}
	started := []string{}
	ordered := map[string]bool{}
	run := func(stack, component string) {
		unit := units.Name(component, stack)
		ordered[unit] = true
		files, exists := rendered.Files[stack]
		if !selected(stack) || !exists || files.Get(component) == nil || !desired[stack] {
			return
		}
		var err error
		switch {
		case changed[unit] && states[unit].Running():
			logf("重启 %s", unit)
			err = s.Units.Restart(ctx, unit)
		case !states[unit].Running():
			logf("启动 %s", unit)
			err = s.Units.Start(ctx, unit)
		default:
			return
		}
		if err != nil {
			failures = append(failures, err.Error())
			return
		}
		started = append(started, unit)
	}
	for _, node := range rendered.Order {
		run(node.Stack, componentOf(node.Component))
	}
	names := slices.Sorted(maps.Keys(rendered.Files))
	for _, name := range names {
		for _, component := range runtime.Components {
			if !ordered[units.Name(component, name)] {
				run(name, component)
			}
		}
	}

	// 4. 检查启动结果。
	if len(started) > 0 {
		select {
		case <-time.After(s.settleDelay()):
		case <-ctx.Done():
		}
		after, err := s.Units.States(ctx, started)
		if err != nil {
			return err
		}
		for _, unit := range started {
			if !after[unit].Running() {
				failures = append(failures, fmt.Sprintf("%s 未能保持运行（%s）", unit, after[unit].Active))
			} else {
				logf("%s 运行中", unit)
			}
		}
	}
	s.publishUnitStates(ctx)
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "；"))
	}
	logf("应用完成")
	return nil
}

func (s *Service) settleDelay() time.Duration {
	if s.SettleDelay > 0 {
		return s.SettleDelay
	}
	return 2 * time.Second
}

func splitUnit(unit string) (stack, component string) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(unit, "psw-"), ".service")
	component, stack, _ = strings.Cut(trimmed, "@")
	return stack, component
}

// UnitView 是一个 unit 的状态。
type UnitView struct {
	Unit    string    `json:"unit"`
	Present bool      `json:"present" doc:"生成物存在"`
	Active  string    `json:"active"`
	Sub     string    `json:"sub"`
	Since   time.Time `json:"since"`
}

// StackRuntime 是一个 stack 的运行状态。
type StackRuntime struct {
	Name            string     `json:"name"`
	DesiredRunning  bool       `json:"desired_running"`
	AutoStopped     bool       `json:"auto_stopped" doc:"因每日流量阈值自动停止"`
	AppliedRevision int64      `json:"applied_revision"`
	AppliedAt       time.Time  `json:"applied_at"`
	Drift           bool       `json:"drift" doc:"磁盘上的生成物与最近一次应用不一致"`
	Units           []UnitView `json:"units"`
}

// RuntimeStatus 汇总全部 stack 的 unit 状态、应用版本和漂移。
func (s *Service) RuntimeStatus(ctx context.Context) ([]StackRuntime, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	var applied []dbq.AppliedState
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		applied, err = q.ListAppliedState(ctx)
		return err
	}); err != nil {
		return nil, err
	}
	autoStopped, err := s.currentStackAutoStops(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]dbq.AppliedState{}
	for _, row := range applied {
		byName[row.StackName] = row
	}
	names := map[string]bool{}
	for _, stack := range doc.Stacks {
		names[stack.Name] = true
	}
	for name := range byName {
		names[name] = true
	}
	sorted := slices.Sorted(maps.Keys(names))
	unitNames := []string{}
	for _, name := range sorted {
		unitNames = append(unitNames, units.Name(units.Xray, name), units.Name(units.Mihomo, name))
	}
	states, err := s.Units.States(ctx, unitNames)
	if err != nil {
		return nil, err
	}
	out := []StackRuntime{}
	for _, name := range sorted {
		view := StackRuntime{Name: name, Units: []UnitView{}}
		if index := doc.StackByName(name); index >= 0 {
			view.DesiredRunning = doc.Stacks[index].Enabled && doc.Stacks[index].DesiredRunning
			view.AutoStopped = autoStopped[doc.Stacks[index].ID]
		}
		files, err := s.disk().Read(name)
		if err != nil {
			return nil, err
		}
		if row, ok := byName[name]; ok {
			view.AppliedRevision, view.AppliedAt = row.RevisionID, time.Unix(row.AppliedAt, 0)
			view.Drift = runtime.Hash(files.Xray) != row.XraySha || runtime.Hash(files.Mihomo) != row.MihomoSha
		}
		for _, component := range []string{units.Xray, units.Mihomo} {
			unit := units.Name(component, name)
			state := states[unit]
			view.Units = append(view.Units, UnitView{
				Unit: unit, Present: files.Get(component) != nil, Active: state.Active, Sub: state.Sub, Since: state.Since,
			})
		}
		out = append(out, view)
	}
	return out, nil
}

// UnitAction 立即启动、停止或重启 stack 的组件（component 为 all 时作用于两个组件）。
func (s *Service) UnitAction(ctx context.Context, actor Actor, stack, component, action string) error {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.collectTraffic(ctx)
	if action == "start" || action == "restart" {
		if err := s.checkStackStartLimit(ctx, stack); err != nil {
			return err
		}
	}
	files, err := s.disk().Read(stack)
	if err != nil {
		return err
	}
	components := []string{component}
	if component == "all" {
		components = runtime.Components
		if action == "stop" {
			components = []string{units.Xray, units.Mihomo}
		}
	}
	var failures []string
	clearedRestore := map[string]bool{}
	for _, component := range components {
		unit := units.Name(component, stack)
		if action != "stop" && files.Get(component) == nil {
			if len(components) == 1 {
				return &FieldError{Field: "component", Message: "the component has no applied config; apply first"}
			}
			continue
		}
		switch action {
		case "start":
			err = s.Units.Start(ctx, unit)
		case "stop":
			err = s.Units.Stop(ctx, unit)
		case "restart":
			err = s.Units.Restart(ctx, unit)
		default:
			return &FieldError{Field: "action", Message: "unknown action"}
		}
		if err != nil {
			failures = append(failures, err.Error())
		} else if action == "stop" {
			clearedRestore[component] = true
		}
	}
	if action == "stop" && len(clearedRestore) > 0 {
		if err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
			return q.ClearStackAutoStopRestore(ctx, dbq.ClearStackAutoStopRestoreParams{
				ClearXray: boolInt(clearedRestore[units.Xray]), ClearMihomo: boolInt(clearedRestore[units.Mihomo]), StackName: stack,
			})
		}); err != nil {
			return err
		}
	}
	_ = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionRuntimeUnit, Object: "stack:" + stack, Summary: action + " " + component,
		})
	})
	s.publishUnitStates(ctx)
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "；"))
	}
	return nil
}

// Reconcile 让 unit 状态与期望运行状态一致：fix 为 true 时拉起应运行而未运行的 unit、停止不应运行的 unit，
// 否则只报告。返回发现的不一致描述。
func (s *Service) Reconcile(ctx context.Context, fix bool) ([]string, error) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	status, err := s.RuntimeStatus(ctx)
	if err != nil {
		return nil, err
	}
	autoStopped := map[string]bool{}
	for _, stack := range status {
		autoStopped[stack.Name] = stack.AutoStopped
	}
	var findings []string
	for _, stack := range status {
		if stack.Drift {
			findings = append(findings, fmt.Sprintf("%s 的生成物与最近一次应用不一致", stack.Name))
		}
		for _, unit := range stack.Units {
			running := unit.Active == "active" || unit.Active == "activating"
			want := stack.DesiredRunning && unit.Present && !autoStopped[stack.Name]
			switch {
			case want && !running:
				findings = append(findings, unit.Unit+" 应运行但未运行")
				if fix {
					if err := s.Units.Start(ctx, unit.Unit); err != nil {
						findings = append(findings, err.Error())
					}
				}
			case !want && running:
				findings = append(findings, unit.Unit+" 不应运行但在运行")
				if fix {
					if err := s.Units.Stop(ctx, unit.Unit); err != nil {
						findings = append(findings, err.Error())
					}
				}
			}
		}
	}
	return findings, nil
}

// publishUnitStates 推送最新的 unit 状态。
func (s *Service) publishUnitStates(ctx context.Context) {
	status, err := s.RuntimeStatus(ctx)
	if err != nil {
		slog.Warn("collect unit states", "err", err)
		return
	}
	s.publish("runtime", status)
}

// MonitorUnits 每隔 interval 检查一次 unit 状态，有变化时推送事件，直到 ctx 结束。
func (s *Service) MonitorUnits(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status, err := s.RuntimeStatus(ctx)
			if err != nil {
				continue
			}
			data, _ := json.Marshal(status)
			if string(data) != last {
				last = string(data)
				s.publish("runtime", status)
			}
		}
	}
}

// syncPauses 让手动暂停当场生效：比较保存前后的暂停状态（含自动暂停），让生成物和运行中的 xray 跟上。
// 返回需要提示管理员的情况。
func (s *Service) syncPauses(ctx context.Context, before, after *configdoc.Document) []string {
	auto := s.autoPaused(ctx)
	return s.syncDisabled(ctx, after, unionDisabled(configdoc.DisabledUsers(before), auto), unionDisabled(configdoc.DisabledUsers(after), auto), false)
}

// syncDisabled 对已应用的 stack，按「已应用版本 + 新的暂停状态」重写生成物，并通过 HandlerService 热增删用户。
// disconnect 为真时，有用户被暂停的 xray 重启一次，切断已建立的连接。
func (s *Service) syncDisabled(ctx context.Context, current *configdoc.Document, oldSet, newSet domain.DisabledUserSet, disconnect bool) []string {
	if equalDisabled(oldSet, newSet) {
		return nil
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	// 热增删用户前补采一次，暂停前的用量不丢失。
	s.collectTraffic(ctx)
	var applied []dbq.AppliedState
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		applied, err = q.ListAppliedState(ctx)
		return err
	}); err != nil {
		return []string{err.Error()}
	}
	var notes []string
	snapshots := map[int64]*configdoc.Document{}
	auto := s.autoPaused(ctx)
	for _, row := range applied {
		paused, resumed := toggled(oldSet, newSet, row.StackName)
		if len(paused) == 0 && len(resumed) == 0 {
			continue
		}
		rendered, _, err := s.rewriteApplied(ctx, row, current, auto, snapshots)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s：%v", row.StackName, err))
			continue
		}
		notes = append(notes, s.hotToggle(ctx, rendered.Set, row.StackName, paused, resumed)...)
		if disconnect && len(paused) > 0 {
			unit := units.Name(units.Xray, row.StackName)
			if states, err := s.Units.States(ctx, []string{unit}); err == nil && states[unit].Running() {
				if err := s.Units.Restart(ctx, unit); err != nil {
					notes = append(notes, fmt.Sprintf("stack %s 重启 xray 失败：%v", row.StackName, err))
				}
			}
		}
	}
	s.publishUnitStates(ctx)
	return notes
}

// rewriteApplied 按「已应用版本 + 当前暂停状态」重新渲染并写入一个 stack 的生成物。
func (s *Service) rewriteApplied(ctx context.Context, row dbq.AppliedState, current *configdoc.Document, auto domain.DisabledUserSet, snapshots map[int64]*configdoc.Document) (*runtime.Rendered, runtime.Files, error) {
	snapshot, ok := snapshots[row.RevisionID]
	if !ok {
		var err error
		if snapshot, err = s.RevisionSnapshot(ctx, row.RevisionID); err != nil {
			return nil, runtime.Files{}, fmt.Errorf("读取已应用版本失败：%w", err)
		}
		snapshots[row.RevisionID] = snapshot
	}
	overlay := snapshot.Clone()
	for index := range overlay.Users {
		if currentIndex := current.UserByName(overlay.Users[index].Name); currentIndex >= 0 {
			overlay.Users[index].Enabled = current.Users[currentIndex].Enabled
			overlay.Users[index].PausedStacks = current.Users[currentIndex].PausedStacks
		} else {
			overlay.Users[index].Enabled = false
		}
	}
	rendered, err := runtime.Render(overlay, auto)
	if err != nil {
		return nil, runtime.Files{}, fmt.Errorf("重新生成失败：%w", err)
	}
	files := rendered.Files[row.StackName]
	if err := s.disk().Write(row.StackName, files); err != nil {
		return nil, runtime.Files{}, fmt.Errorf("写入生成物失败：%w", err)
	}
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return q.UpdateAppliedHashes(ctx, dbq.UpdateAppliedHashesParams{
			XraySha: runtime.Hash(files.Xray), MihomoSha: runtime.Hash(files.Mihomo), StackName: row.StackName,
		})
	}); err != nil {
		return nil, runtime.Files{}, err
	}
	return rendered, files, nil
}

// hotToggle 对运行中的 xray 实例逐个入站移除或加回用户。
func (s *Service) hotToggle(ctx context.Context, set domain.StackSet, stackName string, paused, resumed []string) []string {
	var stack *domain.Stack
	for index := range set.Stacks {
		if set.Stacks[index].Name == stackName {
			stack = &set.Stacks[index]
		}
	}
	if stack == nil || !stack.Enabled || !stack.Xray.Enabled {
		return nil
	}
	unit := units.Name(units.Xray, stackName)
	states, err := s.Units.States(ctx, []string{unit})
	if err != nil || !states[unit].Running() {
		return nil
	}
	api := domain.ResolveXrayAPIConfig(set.Config.Defaults.Xray, stack.Xray)
	if !api.Enabled || !api.HasService("HandlerService") {
		return []string{fmt.Sprintf("stack %s 未开启 HandlerService，暂停状态已写入配置，重启该 stack 后生效", stackName)}
	}
	var notes []string
	for _, inbound := range stack.Xray.Inbounds {
		if !xray.SupportsUserToggle(inbound.Protocol) {
			continue
		}
		for _, user := range inbound.Users {
			switch {
			case slices.Contains(paused, user.User):
				email := user.StatsEmail()
				err := s.XrayAPI.RemoveUser(ctx, api.Listen, inbound.TagOrDefault(), email)
				if userID, profileID, ok := domain.ParseUserStatsEmail(email); err != nil && ok {
					// 升级前启动的实例仍可能使用不含用户名的 email，暂停时兼容移除旧标识。
					if legacy := domain.UserStatsEmail("", userID, profileID); legacy != email {
						if legacyErr := s.XrayAPI.RemoveUser(ctx, api.Listen, inbound.TagOrDefault(), legacy); legacyErr != nil {
							err = fmt.Errorf("%v; legacy email: %w", err, legacyErr)
						} else {
							err = nil
						}
					}
				}
				if err != nil {
					notes = append(notes, fmt.Sprintf("stack %s 移除用户 %s 失败，重启该 stack 后生效：%v", stackName, user.User, err))
				}
			case slices.Contains(resumed, user.User):
				patch, err := xray.DumpsInboundUserPatch(inbound, user)
				if err == nil {
					err = s.XrayAPI.AddUser(ctx, api.Listen, patch)
				}
				if err != nil {
					notes = append(notes, fmt.Sprintf("stack %s 加回用户 %s 失败，重启该 stack 后生效：%v", stackName, user.User, err))
				}
			}
		}
	}
	return notes
}

func equalDisabled(a, b domain.DisabledUserSet) bool {
	flatten := func(set domain.DisabledUserSet) []string {
		out := []string{}
		for stack, users := range set {
			for user := range users {
				out = append(out, stack+"\x00"+user)
			}
		}
		sort.Strings(out)
		return out
	}
	return slices.Equal(flatten(a), flatten(b))
}

func toggled(before, after domain.DisabledUserSet, stack string) (paused, resumed []string) {
	for user := range after[stack] {
		if !before.Has(stack, user) {
			paused = append(paused, user)
		}
	}
	for user := range before[stack] {
		if !after.Has(stack, user) {
			resumed = append(resumed, user)
		}
	}
	return paused, resumed
}

// PauseResult 是暂停或恢复用户的结果。
type PauseResult struct {
	User  *configdoc.User
	Notes []string
}

// SetUserPause 设置用户的启用开关和逐 stack 暂停，当场生效并写入配置。
func (s *Service) SetUserPause(ctx context.Context, actor Actor, id int64, enabled bool, pausedStacks []string) (*PauseResult, error) {
	change := &ConfigChange{Action: ActionUserPause}
	var index int
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		index = doc.UserByID(id)
		if index < 0 {
			return ErrNotFound
		}
		user := &doc.Users[index]
		user.Enabled = enabled
		user.PausedStacks = append([]string{}, pausedStacks...)
		state := "启用"
		if !enabled {
			state = "暂停（全部 stack）"
		} else if len(pausedStacks) > 0 {
			state = "在 " + strings.Join(pausedStacks, "、") + " 上暂停"
		}
		change.Object, change.Summary, change.Scope = "user:"+user.Name, "用户 "+user.Name+" "+state, fmt.Sprintf("users[%d]", index)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &PauseResult{User: &doc.Users[index], Notes: change.Notes}, nil
}
