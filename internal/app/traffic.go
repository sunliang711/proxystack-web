package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/domain"
	"proxystack-web/internal/events"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/traffic"
	"proxystack-web/internal/units"
)

const (
	// liveWindow 是内存中保留实时速率的时长。
	liveWindow = time.Hour
	// dailySeriesDays 是用户详情页的日用量趋势天数。
	dailySeriesDays = 30
	// topUsers 是总览页用量排行的人数。
	topUsers = 10
)

// CollectStatus 是一个 stack 的采集状态。
type CollectStatus struct {
	StackID int64     `json:"stack_id"`
	Stack   string    `json:"stack"`
	LastAt  time.Time `json:"last_at"`
	Error   string    `json:"error"`
}

// LivePoint 是一次采集算出的实时速率（字节/秒）。
type LivePoint struct {
	At     time.Time                `json:"at"`
	Up     int64                    `json:"up"`
	Down   int64                    `json:"down"`
	Stacks map[string]traffic.Bytes `json:"stacks"`
	Users  map[int64]UserLivePoint  `json:"users"`
}

type collectTarget struct {
	stackID int64
	name    string
	server  string
	// aggregator 表示 auto stack：它的流量经 edge stack 的 relay 出网，实时总速率不重复相加它。
	aggregator bool
	// problem 非空时该 stack 无法采集（未开启统计接口）。
	problem string
}

// trafficState 是采集器的内存状态。mu 保护数据字段；collectMu 串行化采集，
// 保证定时采集和重启前的补采不会同时清零同一个实例的计数。
type trafficState struct {
	collectMu sync.Mutex
	mu        sync.Mutex
	pending   map[traffic.Key]traffic.Bytes
	users     map[int64]traffic.Bytes
	live      []LivePoint
	statuses  map[int64]CollectStatus
	lastAt    time.Time
	targetKey string
	targets   []collectTarget

	lastMinute time.Time

	// lastCollected 是各 stack 最近一次成功采集的时间，用于判断 xray 是否在两次采集之间重启或停止。
	lastCollected map[int64]time.Time
	// gapsReady 表示本进程已检查过停机期间的缺口。
	gapsReady bool
}

// location 返回设置的时区；无效时使用 UTC。
func location(settings configdoc.Settings) *time.Location {
	if loc, err := time.LoadLocation(settings.Basic.Timezone); err == nil && settings.Basic.Timezone != "" {
		return loc
	}
	return time.UTC
}

func (s *Service) loadSettings(ctx context.Context) (configdoc.Settings, error) {
	var settings configdoc.Settings
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		settings, err = store.LoadSettings(ctx, q)
		return err
	})
	return settings, err
}

// collectTargets 返回各已应用 stack 的 API 地址。地址取自已应用的版本（运行中的进程监听的就是它），
// 结果按已应用版本缓存。
func (s *Service) collectTargets(ctx context.Context) ([]collectTarget, error) {
	var applied []dbq.AppliedState
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		applied, err = q.ListAppliedState(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	parts := make([]string, 0, len(applied))
	for _, row := range applied {
		parts = append(parts, row.StackName+"@"+strconv.FormatInt(row.RevisionID, 10))
	}
	key := strings.Join(parts, "|")
	s.traffic.mu.Lock()
	cachedKey, cached := s.traffic.targetKey, s.traffic.targets
	s.traffic.mu.Unlock()
	if key == cachedKey && cached != nil {
		return cached, nil
	}
	targets := []collectTarget{}
	snapshots := map[int64]*configdoc.Document{}
	sets := map[int64]domain.StackSet{}
	for _, row := range applied {
		snapshot, ok := snapshots[row.RevisionID]
		if !ok {
			if snapshot, err = s.RevisionSnapshot(ctx, row.RevisionID); err != nil {
				return nil, err
			}
			set, err := configdoc.Compile(snapshot)
			if err != nil {
				return nil, err
			}
			snapshots[row.RevisionID], sets[row.RevisionID] = snapshot, set
		}
		index := snapshot.StackByName(row.StackName)
		if index < 0 {
			continue
		}
		set := sets[row.RevisionID]
		stack := set.ByName()[row.StackName]
		if stack == nil || !stack.Enabled || !stack.Xray.Enabled {
			continue
		}
		target := collectTarget{stackID: snapshot.Stacks[index].ID, name: row.StackName, aggregator: snapshot.Stacks[index].Role == "auto"}
		api := domain.ResolveXrayAPIConfig(set.Config.Defaults.Xray, stack.Xray)
		stats := domain.ResolveXrayStatsConfig(set.Config.Defaults.Xray, stack.Xray)
		if !api.Enabled || !api.HasService("StatsService") || !stats.Enabled {
			target.problem = "未开启 stats 或 StatsService，应用配置后恢复采集"
		}
		target.server = api.Listen
		targets = append(targets, target)
	}
	s.traffic.mu.Lock()
	s.traffic.targetKey, s.traffic.targets = key, targets
	s.traffic.mu.Unlock()
	return targets, nil
}

// collectTraffic 读取各运行中 xray 实例的计数并累加到内存。计数读取时被清零，
// 所以重启实例和热增删用户之前也要调用它，避免丢失两次采集之间的流量。
func (s *Service) collectTraffic(ctx context.Context) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		slog.Warn("traffic collect", "err", err)
		return
	}
	if !settings.Traffic.Enabled {
		return
	}
	s.traffic.collectMu.Lock()
	defer s.traffic.collectMu.Unlock()
	targets, err := s.collectTargets(ctx)
	if err != nil {
		slog.Warn("traffic collect", "err", err)
		return
	}
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, units.Name(units.Xray, target.name))
	}
	states, err := s.Units.States(ctx, names)
	if err != nil {
		slog.Warn("traffic collect", "err", err)
		return
	}
	known := map[int64]bool{}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		users, err := q.ListUsers(ctx)
		for _, user := range users {
			known[user.ID] = true
		}
		return err
	})
	if err != nil {
		slog.Warn("traffic collect", "err", err)
		return
	}
	now := s.Now()
	point := LivePoint{At: now, Stacks: map[string]traffic.Bytes{}, Users: map[int64]UserLivePoint{}}
	var total traffic.Bytes
	statuses := map[int64]CollectStatus{}
	deltas := []traffic.Delta{}
	checks := make([]gapCheck, 0, len(targets))
	for _, target := range targets {
		status := CollectStatus{StackID: target.stackID, Stack: target.name, LastAt: now}
		state := states[units.Name(units.Xray, target.name)]
		check := gapCheck{stackID: target.stackID, since: state.Since}
		switch {
		case target.problem != "":
			status.Error = target.problem
			check.result = gapUncounted
		case !state.Running():
			status.Error = "xray 未运行"
			check.result = gapStopped
		default:
			stats, err := s.XrayAPI.QueryStats(ctx, target.server)
			if err != nil {
				status.Error = err.Error()
				check.result = gapFailed
				break
			}
			check.result = gapCollected
			delta := traffic.Parse(target.stackID, stats, func(id int64) bool { return known[id] })
			deltas = append(deltas, delta)
			point.Stacks[target.name] = delta.Stack
			for id, bytes := range delta.Users {
				user := point.Users[id]
				if user.Stacks == nil {
					user.Stacks = map[string]traffic.Bytes{}
				}
				user.Bytes.Add(bytes)
				user.Stacks[target.name] = bytes
				point.Users[id] = user
			}
			if !target.aggregator {
				total.Add(delta.Stack)
			}
		}
		check.reason = status.Error
		checks = append(checks, check)
		statuses[target.stackID] = status
	}
	s.recordTrafficGaps(ctx, settings, now, checks)

	s.traffic.mu.Lock()
	defer s.traffic.mu.Unlock()
	if s.traffic.pending == nil {
		s.traffic.pending, s.traffic.users = map[traffic.Key]traffic.Bytes{}, map[int64]traffic.Bytes{}
	}
	for _, delta := range deltas {
		for key, bytes := range delta.Details {
			value := s.traffic.pending[key]
			value.Add(bytes)
			s.traffic.pending[key] = value
		}
		for id, bytes := range delta.Users {
			value := s.traffic.users[id]
			value.Add(bytes)
			s.traffic.users[id] = value
		}
	}
	s.traffic.statuses = statuses
	if !s.traffic.lastAt.IsZero() {
		if elapsed := int64(now.Sub(s.traffic.lastAt).Seconds()); elapsed > 0 {
			point.Up, point.Down = total.Up/elapsed, total.Down/elapsed
			for name, bytes := range point.Stacks {
				point.Stacks[name] = traffic.Bytes{Up: bytes.Up / elapsed, Down: bytes.Down / elapsed}
			}
			for id, user := range point.Users {
				user.Up, user.Down = user.Up/elapsed, user.Down/elapsed
				for name, bytes := range user.Stacks {
					user.Stacks[name] = traffic.Bytes{Up: bytes.Up / elapsed, Down: bytes.Down / elapsed}
				}
				point.Users[id] = user
			}
			s.traffic.live = append(s.traffic.live, point)
			s.Events.Publish(events.Event{Type: "traffic", Data: point})
		}
	}
	s.traffic.lastAt = now
	cutoff := now.Add(-liveWindow)
	s.traffic.live = slices.DeleteFunc(s.traffic.live, func(p LivePoint) bool { return p.At.Before(cutoff) })
}

// flushTraffic 把内存中的增量写入分钟明细和用量表，同时让用量表跟上各用户当前的计费周期。
// 写入失败时增量放回内存，下次重试。
func (s *Service) flushTraffic(ctx context.Context, settings configdoc.Settings) error {
	s.traffic.mu.Lock()
	pending, users := s.traffic.pending, s.traffic.users
	s.traffic.pending, s.traffic.users = nil, nil
	s.traffic.mu.Unlock()

	now := s.Now()
	loc := location(settings)
	bucket := now.Truncate(time.Minute).Unix()
	day := traffic.Day(now, loc)
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListUsers(ctx)
		if err != nil {
			return err
		}
		usage, err := q.ListUserUsage(ctx)
		if err != nil {
			return err
		}
		periods := map[int64]int64{}
		for _, row := range usage {
			periods[row.UserID] = row.PeriodStart + 1
		}
		for _, user := range rows {
			start := traffic.PeriodStart(now, loc, user.ResetMode, int(user.ResetDay))
			if periods[user.ID] != start+1 {
				if err := q.ResetUserUsage(ctx, dbq.ResetUserUsageParams{UserID: user.ID, PeriodStart: start}); err != nil {
					return err
				}
			}
		}
		if err := q.PutTrafficHeartbeat(ctx, now.Unix()); err != nil {
			return err
		}
		for key, bytes := range pending {
			if err := q.AddTrafficMinute(ctx, dbq.AddTrafficMinuteParams{
				BucketStart: bucket, StackID: key.StackID, Scope: key.Scope, Subject: key.Subject, Up: bytes.Up, Down: bytes.Down,
			}); err != nil {
				return err
			}
		}
		for _, user := range rows {
			bytes, ok := users[user.ID]
			if !ok {
				continue
			}
			if err := q.AddUserUsage(ctx, dbq.AddUserUsageParams{Up: bytes.Up, Down: bytes.Down, UserID: user.ID}); err != nil {
				return err
			}
			if err := q.AddUserDailyUsage(ctx, dbq.AddUserDailyUsageParams{UserID: user.ID, Day: day, Up: bytes.Up, Down: bytes.Down}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.traffic.mu.Lock()
		defer s.traffic.mu.Unlock()
		if s.traffic.pending == nil {
			s.traffic.pending, s.traffic.users = map[traffic.Key]traffic.Bytes{}, map[int64]traffic.Bytes{}
		}
		for key, bytes := range pending {
			value := s.traffic.pending[key]
			value.Add(bytes)
			s.traffic.pending[key] = value
		}
		for id, bytes := range users {
			value := s.traffic.users[id]
			value.Add(bytes)
			s.traffic.users[id] = value
		}
		return err
	}
	return nil
}

// pendingUsers 返回内存中尚未落库的用户用量。
func (s *Service) pendingUsers() map[int64]traffic.Bytes {
	s.traffic.mu.Lock()
	defer s.traffic.mu.Unlock()
	out := make(map[int64]traffic.Bytes, len(s.traffic.users))
	for id, bytes := range s.traffic.users {
		out[id] = bytes
	}
	return out
}

// TrafficTick 执行一轮采集和判定：每次都采集并判定自动暂停，跨分钟时落库和汇总。
func (s *Service) TrafficTick(ctx context.Context) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		slog.Warn("traffic tick", "err", err)
		return
	}
	s.collectTraffic(ctx)
	now := s.Now()
	if minute := now.Truncate(time.Minute); !minute.Equal(s.traffic.lastMinute) {
		s.traffic.lastMinute = minute
		if err := s.flushTraffic(ctx, settings); err != nil {
			slog.Warn("traffic flush", "err", err)
		} else if err := s.Store.RollupTraffic(ctx, now, location(settings)); err != nil {
			slog.Warn("traffic rollup", "err", err)
		}
	}
	s.evaluateAutoPause(ctx)
	s.evaluateStackLimits(ctx)
}

// PruneNow 立即清理过期明细。
func (s *Service) PruneNow(ctx context.Context) {
	if settings, err := s.loadSettings(ctx); err == nil {
		s.pruneTraffic(ctx, settings, s.Now())
	}
}

func (s *Service) pruneTraffic(ctx context.Context, settings configdoc.Settings, now time.Time) {
	retention := settings.Traffic
	if err := s.Store.PruneTraffic(ctx, now, map[string]int{
		store.LevelMinute: retention.RetentionMinuteDays, store.LevelHour: retention.RetentionHourDays,
		store.LevelDay: retention.RetentionDayDays, store.LevelMonth: retention.RetentionMonthDays,
	}); err != nil {
		slog.Warn("traffic prune", "err", err)
	}
	cutoff := traffic.Day(now.AddDate(0, 0, -2*dailySeriesDays), location(settings))
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if err := q.DeleteUserDailyBefore(ctx, cutoff); err != nil {
			return err
		}
		// 缺口记录与保留最久的明细一起清理；月明细永久保留时缺口也永久保留。
		if retention.RetentionMonthDays > 0 {
			keep := max(retention.RetentionHourDays, retention.RetentionDayDays, retention.RetentionMonthDays)
			if err := q.DeleteTrafficGapsBefore(ctx, now.AddDate(0, 0, -keep).Unix()); err != nil {
				return err
			}
		}
		return q.DeleteNotificationsBefore(ctx, now.AddDate(-1, 0, 0).Unix())
	}); err != nil {
		slog.Warn("traffic prune", "err", err)
	}
}

// runTraffic 是采集循环，间隔跟随设置。退出前再采集一次并落库，正常停止不丢数据。
func (s *Service) runTraffic(ctx context.Context) {
	for {
		s.TrafficTick(ctx)
		interval := 30 * time.Second
		if settings, err := s.loadSettings(ctx); err == nil && settings.Traffic.Enabled {
			interval = time.Duration(settings.Traffic.Interval) * time.Second
		}
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s.collectTraffic(shutdown)
			if settings, err := s.loadSettings(shutdown); err == nil {
				if err := s.flushTraffic(shutdown, settings); err != nil {
					slog.Warn("traffic flush", "err", err)
				}
			}
			return
		case <-time.After(interval):
		}
	}
}

func userNames(ctx context.Context, q *dbq.Queries) (map[int64]string, error) {
	rows, err := q.ListUsers(ctx)
	names := make(map[int64]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, err
}

// subjectName 返回用户名；已删除的用户显示为 #ID。
func subjectName(names map[int64]string, id int64) string {
	if name, ok := names[id]; ok {
		return name
	}
	return "#" + strconv.FormatInt(id, 10)
}

// DayUsage 是一天的用量。
type DayUsage struct {
	Day string `json:"day"`
	traffic.Bytes
}

// AutoPause 是一条生效中的自动暂停。
type AutoPause struct {
	Reason string    `json:"reason" enum:"daily,quota,expired,schedule"`
	Since  time.Time `json:"since"`
	// ResumeAt 是预计恢复时间，零值表示要由管理员调整后才恢复。
	ResumeAt time.Time `json:"resume_at"`
}

// UserUsage 是用户详情页和用户列表的用量数据。
type UserUsage struct {
	UserID int64         `json:"user_id"`
	Today  traffic.Bytes `json:"today"`
	Period traffic.Bytes `json:"period"`
	// PeriodStart 为零值表示不按周期重置。
	PeriodStart time.Time `json:"period_start"`
	NextReset   time.Time `json:"next_reset"`
	// Exempt 表示当天已解除每日暂停，不再按每日阈值暂停。
	Exempt bool `json:"exempt"`
	// WindowExemptUntil 非零时表示本次停服时段已放行，到该时刻为止不按停服时段暂停。
	WindowExemptUntil time.Time         `json:"window_exempt_until"`
	AutoPauses        []AutoPause       `json:"auto_pauses"`
	Daily             []DayUsage        `json:"daily"`
	ByStack           []StackDailyUsage `json:"by_stack"`
}

// UsersUsage 返回全部用户的今日用量、本周期用量和自动暂停，不含日用量趋势。
func (s *Service) UsersUsage(ctx context.Context) ([]UserUsage, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	now, loc := s.Now(), location(settings)
	today := traffic.Day(now, loc)
	pending := s.pendingUsers()
	var out []UserUsage
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		users, err := q.ListUsers(ctx)
		if err != nil {
			return err
		}
		usage, err := q.ListUserUsage(ctx)
		if err != nil {
			return err
		}
		daily, err := q.ListUserDailyUsage(ctx, today)
		if err != nil {
			return err
		}
		pauses, err := q.ListAutoPauses(ctx)
		if err != nil {
			return err
		}
		exempts, err := q.ListWindowExempts(ctx, now.Unix())
		if err != nil {
			return err
		}
		out = make([]UserUsage, 0, len(users))
		for _, user := range users {
			item := UserUsage{UserID: user.ID, AutoPauses: []AutoPause{}, Daily: []DayUsage{}, ByStack: []StackDailyUsage{}}
			period := traffic.PeriodStart(now, loc, user.ResetMode, int(user.ResetDay))
			if period != 0 {
				item.PeriodStart = time.Unix(period, 0)
				item.NextReset = traffic.NextPeriodStart(now, loc, user.ResetMode, int(user.ResetDay))
			}
			for _, row := range usage {
				if row.UserID == user.ID && row.PeriodStart == period {
					item.Period = traffic.Bytes{Up: row.Up, Down: row.Down}
				}
			}
			for _, row := range daily {
				if row.UserID == user.ID {
					item.Today = traffic.Bytes{Up: row.Up, Down: row.Down}
					item.Exempt = row.Exempt != 0
				}
			}
			item.Today.Add(pending[user.ID])
			item.Period.Add(pending[user.ID])
			for _, row := range exempts {
				if row.UserID == user.ID {
					item.WindowExemptUntil = time.Unix(row.Until, 0)
				}
			}
			for _, row := range pauses {
				if row.UserID != user.ID {
					continue
				}
				pause := AutoPause{Reason: row.Reason, Since: time.Unix(row.Since, 0)}
				switch row.Reason {
				case PauseDaily:
					pause.ResumeAt = traffic.DayStart(now, loc).AddDate(0, 0, 1)
				case PauseQuota:
					pause.ResumeAt = item.NextReset
				case PauseSchedule:
					pause.ResumeAt, _ = pauseWindowEnd(now.In(loc), userPauseWindows(user, settings))
				}
				item.AutoPauses = append(item.AutoPauses, pause)
			}
			out = append(out, item)
		}
		return nil
	})
	return out, err
}

// UserUsageDetail 返回单个用户的用量和最近 30 天的日用量。
func (s *Service) UserUsageDetail(ctx context.Context, id int64) (*UserUsage, error) {
	all, err := s.UsersUsage(ctx)
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(all, func(item UserUsage) bool { return item.UserID == id })
	if index < 0 {
		return nil, ErrNotFound
	}
	item := all[index]
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	now, loc := s.Now(), location(settings)
	first := traffic.Day(now.AddDate(0, 0, -(dailySeriesDays-1)), loc)
	var rows []dbq.UserDailyUsage
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		rows, err = q.ListUserDailySeries(ctx, dbq.ListUserDailySeriesParams{UserID: id, Day: first})
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	byDay := map[string]traffic.Bytes{}
	for _, row := range rows {
		byDay[row.Day] = traffic.Bytes{Up: row.Up, Down: row.Down}
	}
	today := traffic.Day(now, loc)
	byDay[today] = item.Today
	for offset := dailySeriesDays - 1; offset >= 0; offset-- {
		day := traffic.Day(now.AddDate(0, 0, -offset), loc)
		item.Daily = append(item.Daily, DayUsage{Day: day, Bytes: byDay[day]})
	}
	if item.ByStack, err = s.userStackDaily(ctx, id, now, loc); err != nil {
		return nil, err
	}
	return &item, nil
}

// csvField 按 CSV 规则转义，并在可能被表格软件当公式执行的内容前加单引号。
func csvField(value string) string {
	if value != "" && strings.ContainsAny(value[:1], "=+-@") {
		value = "'" + value
	}
	if strings.ContainsAny(value, ",\"\r\n") {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return value
}
