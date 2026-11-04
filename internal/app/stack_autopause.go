package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/traffic"
	"proxystack-web/internal/units"
)

// pendingStacks 返回尚未落库的 Stack 入站流量。
func (s *Service) pendingStacks() map[int64]traffic.Bytes {
	s.traffic.mu.Lock()
	defer s.traffic.mu.Unlock()
	out := map[int64]traffic.Bytes{}
	for key, bytes := range s.traffic.pending {
		if key.Scope != traffic.ScopeInbound {
			continue
		}
		value := out[key.StackID]
		value.Add(bytes)
		out[key.StackID] = value
	}
	return out
}

// stackDailyUsage 返回某个 Stack 当天已落库和内存中的上下行合计。
func (s *Service) stackDailyUsage(ctx context.Context, stackID int64, dayStart int64) (traffic.Bytes, error) {
	var rows []dbq.ListStackDailyUsageRow
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		rows, err = q.ListStackDailyUsage(ctx, dayStart)
		return err
	})
	if err != nil {
		return traffic.Bytes{}, err
	}
	var out traffic.Bytes
	for _, row := range rows {
		if row.StackID == stackID {
			out.Up += row.Up
			out.Down += row.Down
		}
	}
	pending := s.pendingStacks()[stackID]
	out.Add(pending)
	return out, nil
}

// currentStackAutoStops 返回尚未由流量判定解除的自动停止记录。
func (s *Service) currentStackAutoStops(ctx context.Context) (map[int64]bool, error) {
	var rows []dbq.StackAutoStop
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		rows, err = q.ListStackAutoStops(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, row := range rows {
		out[row.StackID] = true
	}
	return out, nil
}

// evaluateStackLimits 按当前 Stack 日用量更新自动停止状态。跨日后记录失效并恢复 Stack；
// 同日调高阈值或关闭限制只解除标记，管理员可手动启动。
func (s *Service) evaluateStackLimits(ctx context.Context) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		slog.Warn("evaluate stack daily limits", "err", err)
		return
	}
	now, loc := s.Now(), location(settings)
	today := traffic.Day(now, loc)
	dayStart := traffic.DayStart(now, loc).Unix()
	var stacks []dbq.Stack
	var stops []dbq.StackAutoStop
	var daily []dbq.ListStackDailyUsageRow
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		if stacks, err = q.ListStacks(ctx); err != nil {
			return err
		}
		if stops, err = q.ListStackAutoStops(ctx); err != nil {
			return err
		}
		daily, err = q.ListStackDailyUsage(ctx, dayStart)
		return err
	})
	if err != nil {
		slog.Warn("read stack daily limits", "err", err)
		return
	}
	used := map[int64]traffic.Bytes{}
	for _, row := range daily {
		value := used[row.StackID]
		value.Up += row.Up
		value.Down += row.Down
		used[row.StackID] = value
	}
	for id, bytes := range s.pendingStacks() {
		value := used[id]
		value.Add(bytes)
		used[id] = value
	}
	current := map[int64]dbq.StackAutoStop{}
	for _, row := range stops {
		current[row.StackID] = row
	}
	want := map[int64]bool{}
	for _, stack := range stacks {
		if stack.DailyLimitBytes > 0 && used[stack.ID].Total() >= stack.DailyLimitBytes {
			want[stack.ID] = true
		}
	}

	s.runtimeMu.Lock()
	restore := map[int64]struct{ xray, mihomo bool }{}
	unitNames := []string{}
	for _, stack := range stacks {
		row, hasCurrent := current[stack.ID]
		if want[stack.ID] && (!hasCurrent || row.Day != today) {
			unitNames = append(unitNames, units.Name(units.Xray, stack.Name), units.Name(units.Mihomo, stack.Name))
		}
	}
	if len(unitNames) > 0 {
		states, err := s.Units.States(ctx, unitNames)
		if err != nil {
			s.runtimeMu.Unlock()
			slog.Warn("read stack units before daily limit stop", "err", err)
			return
		}
		for _, stack := range stacks {
			row, hasCurrent := current[stack.ID]
			if want[stack.ID] && (!hasCurrent || row.Day != today) {
				restore[stack.ID] = struct {
					xray   bool
					mihomo bool
				}{
					xray: states[units.Name(units.Xray, stack.Name)].Running(), mihomo: states[units.Name(units.Mihomo, stack.Name)].Running(),
				}
			}
		}
	}
	type recovery struct {
		stack         dbq.Stack
		restoreXray   bool
		restoreMihomo bool
	}
	recovered := map[int64]bool{}
	for _, stack := range stacks {
		row, hasCurrent := current[stack.ID]
		if !hasCurrent || row.Day == today || want[stack.ID] {
			continue
		}
		item := recovery{stack: stack, restoreXray: row.RestoreXray != 0, restoreMihomo: row.RestoreMihomo != 0}
		if err := s.restoreStackUnitsLocked(ctx, item.stack, item.restoreXray, item.restoreMihomo); err != nil {
			slog.Warn("recover stack after daily limit", "stack", stack.Name, "err", err)
			continue
		}
		recovered[stack.ID] = true
	}
	var notify []pendingEvent
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		for _, stack := range stacks {
			row, hasCurrent := current[stack.ID]
			active := hasCurrent && row.Day == today

			switch {
			case want[stack.ID] && !active:
				if hasCurrent {
					if err := q.DeleteStackAutoStop(ctx, stack.ID); err != nil {
						return err
					}
				}
				unitsToRestore := restore[stack.ID]
				if err := q.InsertStackAutoStop(ctx, dbq.InsertStackAutoStopParams{
					StackID: stack.ID, Day: today, Since: now.Unix(), RestoreXray: boolInt(unitsToRestore.xray), RestoreMihomo: boolInt(unitsToRestore.mihomo),
				}); err != nil {
					return err
				}
				if err := audit.Write(ctx, q, now, audit.Entry{Actor: SystemActor.Name, Action: ActionStackAutoStop, Object: "stack:" + stack.Name, Summary: fmt.Sprintf("Stack %s 达到每日阈值，自动停止", stack.Name)}); err != nil {
					return err
				}
				notify = append(notify, stackUsageEvent(stack.ID, stack.Name, today, used[stack.ID].Total(), stack.DailyLimitBytes))
			case !want[stack.ID] && hasCurrent:
				if row.Day != today && !recovered[stack.ID] {
					continue
				}
				if err := q.DeleteStackAutoStop(ctx, stack.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		s.runtimeMu.Unlock()
		slog.Warn("write stack daily limits", "err", err)
		return
	}
	for _, stack := range stacks {
		if !want[stack.ID] || stack.Enabled == 0 || (stack.XrayEnabled == 0 && stack.MihomoEnabled == 0) {
			continue
		}
		if err := s.stopStackUnitsLocked(ctx, stack.Name); err != nil {
			slog.Warn("stop stack after daily limit", "stack", stack.Name, "err", err)
		}
	}
	s.runtimeMu.Unlock()

	for _, event := range notify {
		s.enqueueNotification(ctx, settings, event)
	}
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func (s *Service) stopStackUnitsLocked(ctx context.Context, stack string) error {
	files, err := s.disk().Read(stack)
	if err != nil {
		return err
	}
	var failures []error
	for _, component := range []string{units.Xray, units.Mihomo} {
		if files.Get(component) == nil {
			continue
		}
		if err := s.Units.Stop(ctx, units.Name(component, stack)); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%v", failures)
	}
	return nil
}

func (s *Service) restoreStackUnitsLocked(ctx context.Context, stack dbq.Stack, restoreXray, restoreMihomo bool) error {
	if stack.Enabled == 0 || stack.DesiredRunning == 0 {
		return nil
	}
	files, err := s.disk().Read(stack.Name)
	if err != nil {
		return err
	}
	names := []string{}
	if restoreMihomo && files.Mihomo != nil {
		names = append(names, units.Name(units.Mihomo, stack.Name))
	}
	if restoreXray && files.Xray != nil {
		names = append(names, units.Name(units.Xray, stack.Name))
	}
	if len(names) == 0 {
		return nil
	}
	states, err := s.Units.States(ctx, names)
	if err != nil {
		return err
	}
	for _, name := range names {
		if states[name].Running() {
			continue
		}
		if err := s.Units.Start(ctx, name); err != nil {
			return err
		}
	}
	if delay := s.settleDelay(); len(names) > 0 && delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	after, err := s.Units.States(ctx, names)
	if err != nil {
		return err
	}
	for _, name := range names {
		if !after[name].Running() {
			return fmt.Errorf("%s did not remain running", name)
		}
	}
	return nil
}

func (s *Service) checkStackStartLimit(ctx context.Context, stackName string) error {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	index := doc.StackByName(stackName)
	if index < 0 || doc.Stacks[index].DailyLimitBytes == 0 {
		return s.clearStackAutoStop(ctx, indexStackID(doc, index))
	}
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return err
	}
	dayStart := traffic.DayStart(s.Now(), location(settings)).Unix()
	used, err := s.stackDailyUsage(ctx, doc.Stacks[index].ID, dayStart)
	if err != nil {
		return err
	}
	if used.Total() >= doc.Stacks[index].DailyLimitBytes {
		return &FieldError{Field: "action", Message: "stack daily traffic limit reached; increase the limit or wait until the next day"}
	}
	return s.clearStackAutoStop(ctx, doc.Stacks[index].ID)
}

func indexStackID(doc *configdoc.Document, index int) int64 {
	if index < 0 {
		return 0
	}
	return doc.Stacks[index].ID
}

func (s *Service) clearStackAutoStop(ctx context.Context, stackID int64) error {
	if stackID == 0 {
		return nil
	}
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return q.DeleteStackAutoStop(ctx, stackID)
	})
}
