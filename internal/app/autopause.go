package app

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/domain"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/traffic"
)

// 自动暂停的原因。
const (
	PauseDaily   = "daily"
	PauseQuota   = "quota"
	PauseExpired = "expired"
	// PauseSchedule 是每日停服时段内的暂停，时段结束后自动解除。
	PauseSchedule = "schedule"
)

// SystemActor 是系统自动操作的审计操作者。
var SystemActor = Actor{Name: "系统"}

var pauseLabels = map[string]string{PauseDaily: "每日阈值", PauseQuota: "月度额度", PauseExpired: "到期", PauseSchedule: "停服时段"}

type autoPauseKey struct {
	user   int64
	reason string
}

// autoPauseState 串行化自动暂停的判定和生效；notified 记住已入库的通知，避免每轮判定都写数据库。
type autoPauseState struct {
	mu       sync.Mutex
	notified map[string]bool
}

// autoPaused 返回自动暂停（每日阈值、额度、到期、停服时段）的用户集合：自动暂停作用于用户所在的全部 stack，
// 包括配置中的 stack 和已应用但尚未从配置中同步的 stack。
func (s *Service) autoPaused(ctx context.Context) domain.DisabledUserSet {
	set := domain.DisabledUserSet{}
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListAutoPauses(ctx)
		if err != nil || len(rows) == 0 {
			return err
		}
		names, err := userNames(ctx, q)
		if err != nil {
			return err
		}
		stacks, err := q.ListStacks(ctx)
		if err != nil {
			return err
		}
		applied, err := q.ListAppliedState(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			name, ok := names[row.UserID]
			if !ok {
				continue
			}
			for _, stack := range stacks {
				set.Add(stack.Name, name)
			}
			for _, stack := range applied {
				set.Add(stack.StackName, name)
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("read automatic pauses", "err", err)
	}
	return set
}

// unionDisabled 合并两个暂停集合。
func unionDisabled(a, b domain.DisabledUserSet) domain.DisabledUserSet {
	out := domain.DisabledUserSet{}
	for _, set := range []domain.DisabledUserSet{a, b} {
		for stack, users := range set {
			for user := range users {
				out.Add(stack, user)
			}
		}
	}
	return out
}

// evaluateAutoPause 按当前用量、额度、到期时间和停服时段算出每个用户应有的自动暂停，与现状比较后生效。
// 恢复不需要单独的定时任务：跨天后当日用量归零、周期重置后用量归零、调高阈值或延长到期时间后、
// 停服时段结束后，对应的条件不再成立，下一轮判定自然解除。
func (s *Service) evaluateAutoPause(ctx context.Context) {
	s.autoPause.mu.Lock()
	defer s.autoPause.mu.Unlock()
	settings, err := s.loadSettings(ctx)
	if err != nil {
		slog.Warn("evaluate automatic pause", "err", err)
		return
	}
	now, loc := s.Now(), location(settings)
	today := traffic.Day(now, loc)
	pending := s.pendingUsers()

	var users []dbq.User
	current := map[autoPauseKey]bool{}
	usage := map[int64]dbq.UserUsage{}
	daily := map[int64]dbq.UserDailyUsage{}
	exempt := map[int64]bool{}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if users, err = q.ListUsers(ctx); err != nil {
			return err
		}
		pauses, err := q.ListAutoPauses(ctx)
		if err != nil {
			return err
		}
		for _, row := range pauses {
			current[autoPauseKey{row.UserID, row.Reason}] = true
		}
		usageRows, err := q.ListUserUsage(ctx)
		if err != nil {
			return err
		}
		for _, row := range usageRows {
			usage[row.UserID] = row
		}
		dailyRows, err := q.ListUserDailyUsage(ctx, today)
		if err != nil {
			return err
		}
		for _, row := range dailyRows {
			daily[row.UserID] = row
		}
		exempts, err := q.ListWindowExempts(ctx, now.Unix())
		if err != nil {
			return err
		}
		for _, row := range exempts {
			exempt[row.UserID] = true
		}
		return nil
	})
	if err != nil {
		slog.Warn("evaluate automatic pause", "err", err)
		return
	}

	want := map[autoPauseKey]bool{}
	warn := int64(settings.Notify.WarnPercent)
	var events []pendingEvent
	for _, user := range users {
		view := notifyUser{id: user.ID, name: user.Name, remark: user.Remark}
		used := traffic.Bytes{Up: daily[user.ID].Up, Down: daily[user.ID].Down}
		used.Add(pending[user.ID])
		if limit := user.DailyLimitBytes; limit > 0 {
			switch {
			case used.Total() >= limit:
				paused := user.DailyLimitAction != configdoc.DailyActionNotify
				if paused && daily[user.ID].Exempt == 0 {
					want[autoPauseKey{user.ID, PauseDaily}] = true
				}
				events = append(events, usageEvent(configdoc.EventDailyLimit, view, today, "每日用量", used.Total(), limit, paused))
			case used.Total()*100 >= limit*warn:
				events = append(events, usageEvent(configdoc.EventDailyWarning, view, today, "每日用量", used.Total(), limit, false))
			}
		}
		period := traffic.PeriodStart(now, loc, user.ResetMode, int(user.ResetDay))
		var periodUsed traffic.Bytes
		if row, ok := usage[user.ID]; ok && row.PeriodStart == period {
			periodUsed = traffic.Bytes{Up: row.Up, Down: row.Down}
		}
		periodUsed.Add(pending[user.ID])
		if quota := user.QuotaBytes; quota > 0 {
			key := fmt.Sprintf("%d", period)
			switch {
			case periodUsed.Total() >= quota:
				want[autoPauseKey{user.ID, PauseQuota}] = true
				events = append(events, usageEvent(configdoc.EventQuotaLimit, view, key, "本周期用量", periodUsed.Total(), quota, true))
			case periodUsed.Total()*100 >= quota*warn:
				events = append(events, usageEvent(configdoc.EventQuotaWarning, view, key, "本周期用量", periodUsed.Total(), quota, false))
			}
		}
		// 停服时段是预期行为，不发送通知；已停用的用户无需再暂停，重新启用后下一轮判定补上。
		if _, in := pauseWindowEnd(now.In(loc), userPauseWindows(user, settings)); in && !exempt[user.ID] && user.Enabled != 0 {
			want[autoPauseKey{user.ID, PauseSchedule}] = true
		}
		if user.ExpireAt != "" {
			if expire, err := time.Parse(time.RFC3339, user.ExpireAt); err == nil {
				switch {
				case !now.Before(expire):
					want[autoPauseKey{user.ID, PauseExpired}] = true
					events = append(events, expiryEvent(configdoc.EventExpired, view, user.ExpireAt, expire.In(loc), true))
				case expire.Sub(now) <= time.Duration(settings.Notify.ExpireDays)*24*time.Hour:
					events = append(events, expiryEvent(configdoc.EventExpiring, view, user.ExpireAt, expire.In(loc), false))
				}
			}
		}
	}
	for _, event := range events {
		s.enqueueNotification(ctx, settings, event)
	}
	if maps.Equal(want, current) {
		return
	}
	s.applyAutoPause(ctx, settings, users, current, want)
}

// applyAutoPause 写入自动暂停的变化和审计记录，再让生成物和运行中的 xray 跟上。
func (s *Service) applyAutoPause(ctx context.Context, settings configdoc.Settings, users []dbq.User, current, want map[autoPauseKey]bool) {
	before := s.autoPaused(ctx)
	names := make(map[int64]string, len(users))
	for _, user := range users {
		names[user.ID] = user.Name
	}
	paused := false
	// 停服时段每天让大批用户同时暂停和恢复，审计各汇总为一条，避免逐个用户刷屏。
	var windowPaused, windowResumed []string
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		now := s.Now()
		write := func(key autoPauseKey, summary string) error {
			if key.reason == PauseSchedule {
				if current[key] {
					windowResumed = append(windowResumed, names[key.user])
				} else {
					windowPaused = append(windowPaused, names[key.user])
				}
				return nil
			}
			return audit.Write(ctx, q, now, audit.Entry{
				Actor: SystemActor.Name, Action: ActionUserPause, Object: "user:" + names[key.user],
				Summary: fmt.Sprintf(summary, names[key.user], pauseLabels[key.reason]),
			})
		}
		for key := range want {
			if current[key] {
				continue
			}
			paused = true
			if err := q.InsertAutoPause(ctx, dbq.InsertAutoPauseParams{UserID: key.user, Reason: key.reason, Since: now.Unix()}); err != nil {
				return err
			}
			if err := write(key, "用户 %s 自动暂停（%s）"); err != nil {
				return err
			}
		}
		for key := range current {
			if want[key] {
				continue
			}
			if err := q.DeleteAutoPause(ctx, dbq.DeleteAutoPauseParams{UserID: key.user, Reason: key.reason}); err != nil {
				return err
			}
			if err := write(key, "用户 %s 解除自动暂停（%s）"); err != nil {
				return err
			}
		}
		for _, group := range []struct {
			users   []string
			summary string
		}{{windowPaused, "进入停服时段，自动暂停 %d 位用户：%s"}, {windowResumed, "解除 %d 位用户的停服时段暂停：%s"}} {
			if len(group.users) == 0 {
				continue
			}
			slices.Sort(group.users)
			object := "users"
			if len(group.users) == 1 {
				object = "user:" + group.users[0]
			}
			if err := audit.Write(ctx, q, now, audit.Entry{
				Actor: SystemActor.Name, Action: ActionUserPause, Object: object,
				Summary: fmt.Sprintf(group.summary, len(group.users), strings.Join(group.users, "、")),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("write automatic pause", "err", err)
		return
	}
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		slog.Error("apply automatic pause", "err", err)
		return
	}
	manual := configdoc.DisabledUsers(doc)
	notes := s.syncDisabled(ctx, doc, unionDisabled(manual, before), unionDisabled(manual, s.autoPaused(ctx)), paused && settings.Traffic.DisconnectOnPause)
	if len(notes) > 0 {
		// 这里没有管理员在看结果，写进审计日志留下痕迹：配置已更新，但运行中的实例可能还放行该用户。
		summary := strings.Join(notes, "；")
		slog.Warn("automatic pause", "notes", summary)
		_ = s.Store.InTx(ctx, func(q *dbq.Queries) error {
			return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: SystemActor.Name, Action: ActionUserPause, Object: "runtime", Summary: summary})
		})
	}
}

// ReleaseAutoPause 解除用户当天的每日暂停，当天不再按每日阈值暂停，次日重新判定；
// 或放行用户本次停服时段，到这段停服结束为止不再按停服时段暂停。
// 额度和到期暂停由用量和到期时间决定，调整后自动解除，不能单独解除。
func (s *Service) ReleaseAutoPause(ctx context.Context, actor Actor, id int64, reason string) error {
	if reason == PauseSchedule {
		return s.releaseWindowPause(ctx, actor, id)
	}
	if reason != PauseDaily {
		return &FieldError{Field: "reason", Message: fmt.Sprintf("%s暂停在调整额度或到期时间后自动解除", pauseLabels[reason])}
	}
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return err
	}
	var name string
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		names, err := userNames(ctx, q)
		if err != nil {
			return err
		}
		var ok bool
		if name, ok = names[id]; !ok {
			return ErrNotFound
		}
		if err := q.ExemptUserDaily(ctx, dbq.ExemptUserDailyParams{UserID: id, Day: traffic.Day(s.Now(), location(settings))}); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionUserPause, Object: "user:" + name,
			Summary: "解除用户 " + name + " 当天的每日阈值暂停",
		})
	})
	if err != nil {
		return err
	}
	s.evaluateAutoPause(ctx)
	return nil
}

// releaseWindowPause 放行用户本次停服时段：记录这段连续停服的结束时刻，此前不再按停服时段暂停。
func (s *Service) releaseWindowPause(ctx context.Context, actor Actor, id int64) error {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return err
	}
	now, loc := s.Now(), location(settings)
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		users, err := q.ListUsers(ctx)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(users, func(user dbq.User) bool { return user.ID == id })
		if index < 0 {
			return ErrNotFound
		}
		user := users[index]
		until, in := pauseWindowEnd(now.In(loc), userPauseWindows(user, settings))
		if !in {
			return &FieldError{Field: "reason", Message: "用户当前不在停服时段内"}
		}
		if err := q.DeleteWindowExemptsBefore(ctx, now.Unix()); err != nil {
			return err
		}
		if err := q.UpsertWindowExempt(ctx, dbq.UpsertWindowExemptParams{UserID: id, Until: until.Unix()}); err != nil {
			return err
		}
		return audit.Write(ctx, q, now, audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionUserPause, Object: "user:" + user.Name,
			Summary: fmt.Sprintf("放行用户 %s 本次停服时段，至 %s", user.Name, until.In(loc).Format("01-02 15:04")),
		})
	})
	if err != nil {
		return err
	}
	s.evaluateAutoPause(ctx)
	return nil
}
