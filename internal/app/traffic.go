package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eagle/proxystack-web/internal/configdoc"
	"github.com/eagle/proxystack-web/internal/domain"
	"github.com/eagle/proxystack-web/internal/events"
	"github.com/eagle/proxystack-web/internal/store"
	"github.com/eagle/proxystack-web/internal/store/dbq"
	"github.com/eagle/proxystack-web/internal/traffic"
	"github.com/eagle/proxystack-web/internal/units"
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
}

type collectTarget struct {
	stackID int64
	name    string
	server  string
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
	lastPrune  time.Time
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
		target := collectTarget{stackID: snapshot.Stacks[index].ID, name: row.StackName}
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
	point := LivePoint{At: now, Stacks: map[string]traffic.Bytes{}}
	var total traffic.Bytes
	statuses := map[int64]CollectStatus{}
	deltas := []traffic.Delta{}
	for _, target := range targets {
		status := CollectStatus{StackID: target.stackID, Stack: target.name, LastAt: now}
		switch {
		case target.problem != "":
			status.Error = target.problem
		case !states[units.Name(units.Xray, target.name)].Running():
			status.Error = "xray 未运行"
		default:
			stats, err := s.XrayAPI.QueryStats(ctx, target.server)
			if err != nil {
				status.Error = err.Error()
				break
			}
			delta := traffic.Parse(target.stackID, stats, func(id int64) bool { return known[id] })
			deltas = append(deltas, delta)
			point.Stacks[target.name] = delta.Stack
			total.Add(delta.Stack)
		}
		statuses[target.stackID] = status
	}

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

// TrafficTick 执行一轮采集和判定：每次都采集并判定自动暂停，跨分钟时落库和汇总，每小时清理过期明细。
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
	if now.Sub(s.traffic.lastPrune) >= time.Hour {
		s.traffic.lastPrune = now
		s.pruneTraffic(ctx, settings, now)
	}
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

// TrafficOverview 是总览页的数据。
type TrafficOverview struct {
	Enabled  bool            `json:"enabled"`
	Today    traffic.Bytes   `json:"today"`
	Month    traffic.Bytes   `json:"month"`
	TopUsers []UserTraffic   `json:"top_users"`
	Live     []LivePoint     `json:"live"`
	Stacks   []CollectStatus `json:"stacks"`
	// FailedNotifications 是发送失败的通知数。
	FailedNotifications int64 `json:"failed_notifications"`
}

// UserTraffic 是一个用户的用量。
type UserTraffic struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	traffic.Bytes
}

// TrafficOverviewData 汇总今日、本月合计和用量排行，并合并内存中尚未落库的部分。
func (s *Service) TrafficOverviewData(ctx context.Context) (*TrafficOverview, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	now, loc := s.Now(), location(settings)
	out := &TrafficOverview{Enabled: settings.Traffic.Enabled, TopUsers: []UserTraffic{}, Live: []LivePoint{}, Stacks: []CollectStatus{}}
	end := now.Add(time.Hour).Unix()
	sum := func(level string, from time.Time) (traffic.Bytes, error) {
		var total traffic.Bytes
		rows, err := s.Store.QueryTraffic(ctx, level, traffic.ScopeInbound, from.Unix(), end, nil)
		for _, row := range rows {
			total.Add(traffic.Bytes{Up: row.Up, Down: row.Down})
		}
		return total, err
	}
	if out.Today, err = sum(store.LevelDay, traffic.DayStart(now, loc)); err != nil {
		return nil, err
	}
	if out.Month, err = sum(store.LevelMonth, traffic.MonthStart(now, loc)); err != nil {
		return nil, err
	}
	rows, err := s.Store.QueryTraffic(ctx, store.LevelMonth, traffic.ScopeUser, traffic.MonthStart(now, loc).Unix(), end, nil)
	if err != nil {
		return nil, err
	}
	totals := map[int64]traffic.Bytes{}
	for _, row := range rows {
		if id, ok := traffic.SubjectUser(row.Subject); ok {
			value := totals[id]
			value.Add(traffic.Bytes{Up: row.Up, Down: row.Down})
			totals[id] = value
		}
	}
	s.traffic.mu.Lock()
	for key, bytes := range s.traffic.pending {
		if key.Scope == traffic.ScopeInbound {
			out.Today.Add(bytes)
			out.Month.Add(bytes)
		}
	}
	for id, bytes := range s.traffic.users {
		value := totals[id]
		value.Add(bytes)
		totals[id] = value
	}
	out.Live = append(out.Live, s.traffic.live...)
	for _, status := range s.traffic.statuses {
		out.Stacks = append(out.Stacks, status)
	}
	s.traffic.mu.Unlock()
	sort.Slice(out.Stacks, func(i, j int) bool { return out.Stacks[i].Stack < out.Stacks[j].Stack })

	var names map[int64]string
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if names, err = userNames(ctx, q); err != nil {
			return err
		}
		out.FailedNotifications, err = q.CountFailedNotifications(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	for id, bytes := range totals {
		out.TopUsers = append(out.TopUsers, UserTraffic{ID: id, Name: subjectName(names, id), Bytes: bytes})
	}
	sort.Slice(out.TopUsers, func(i, j int) bool {
		a, b := out.TopUsers[i], out.TopUsers[j]
		if a.Total() != b.Total() {
			return a.Total() > b.Total()
		}
		return a.ID < b.ID
	})
	if len(out.TopUsers) > topUsers {
		out.TopUsers = out.TopUsers[:topUsers]
	}
	return out, nil
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

// TrafficQuery 是明细查询条件。
type TrafficQuery struct {
	Level    string // hour / day / month / year
	Scope    string // user / inbound / outbound
	From     time.Time
	To       time.Time
	Stacks   []int64
	Subjects []string
}

// TrafficSeries 是一个对象的按时间序列，Up/Down 与 Buckets 一一对应。
type TrafficSeries struct {
	Subject string  `json:"subject"`
	Label   string  `json:"label"`
	Up      []int64 `json:"up"`
	Down    []int64 `json:"down"`
	traffic.Bytes
}

// TrafficResult 是明细查询结果。
type TrafficResult struct {
	// Timezone 是统计周期使用的时区，前端按它显示时间桶。
	Timezone string          `json:"timezone"`
	Buckets  []time.Time     `json:"buckets"`
	Series   []TrafficSeries `json:"series"`
}

// QueryTraffic 查询流量明细。用户维度把档案合并到所属用户；年粒度由月明细聚合。
func (s *Service) QueryTraffic(ctx context.Context, query TrafficQuery) (*TrafficResult, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	loc := location(settings)
	level := query.Level
	if level == "year" {
		level = store.LevelMonth
	}
	rows, err := s.Store.QueryTraffic(ctx, level, query.Scope, query.From.Unix(), query.To.Unix(), query.Stacks)
	if err != nil {
		return nil, err
	}
	var names map[int64]string
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		names, err = userNames(ctx, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, subject := range query.Subjects {
		wanted[subject] = true
	}
	type cell struct{ bucket, subject string }
	cells := map[cell]traffic.Bytes{}
	buckets := map[int64]bool{}
	subjects := map[string]bool{}
	for _, row := range rows {
		subject := row.Subject
		if query.Scope == traffic.ScopeUser {
			if id, ok := traffic.SubjectUser(subject); ok {
				subject = strconv.FormatInt(id, 10)
			}
		}
		if len(wanted) > 0 && !wanted[subject] {
			continue
		}
		bucket := row.Bucket
		if query.Level == "year" {
			local := time.Unix(bucket, 0).In(loc)
			bucket = time.Date(local.Year(), 1, 1, 0, 0, 0, 0, loc).Unix()
		}
		key := cell{strconv.FormatInt(bucket, 10), subject}
		value := cells[key]
		value.Add(traffic.Bytes{Up: row.Up, Down: row.Down})
		cells[key] = value
		buckets[bucket], subjects[subject] = true, true
	}
	ordered := make([]int64, 0, len(buckets))
	for bucket := range buckets {
		ordered = append(ordered, bucket)
	}
	slices.Sort(ordered)
	result := &TrafficResult{Timezone: loc.String(), Buckets: make([]time.Time, 0, len(ordered)), Series: []TrafficSeries{}}
	for _, bucket := range ordered {
		result.Buckets = append(result.Buckets, time.Unix(bucket, 0))
	}
	for subject := range subjects {
		series := TrafficSeries{Subject: subject, Label: subjectLabel(query.Scope, subject, names), Up: make([]int64, len(ordered)), Down: make([]int64, len(ordered))}
		for index, bucket := range ordered {
			value := cells[cell{strconv.FormatInt(bucket, 10), subject}]
			series.Up[index], series.Down[index] = value.Up, value.Down
			series.Bytes.Add(value)
		}
		result.Series = append(result.Series, series)
	}
	sort.Slice(result.Series, func(i, j int) bool {
		a, b := result.Series[i], result.Series[j]
		if a.Total() != b.Total() {
			return a.Total() > b.Total()
		}
		return a.Subject < b.Subject
	})
	return result, nil
}

func subjectLabel(scope, subject string, names map[int64]string) string {
	if subject == traffic.SubjectUnknown {
		return "未知"
	}
	if scope == traffic.ScopeUser {
		if id, ok := traffic.SubjectUser(subject); ok {
			return subjectName(names, id)
		}
	}
	return subject
}

// TrafficSubject 是明细筛选项。
type TrafficSubject struct {
	Subject string `json:"subject"`
	Label   string `json:"label"`
}

// TrafficSubjects 返回某个维度出现过的对象，用户维度合并档案。
func (s *Service) TrafficSubjects(ctx context.Context, scope string) ([]TrafficSubject, error) {
	subjects, err := s.Store.TrafficSubjects(ctx, scope)
	if err != nil {
		return nil, err
	}
	var names map[int64]string
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		names, err = userNames(ctx, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := []TrafficSubject{}
	seen := map[string]bool{}
	for _, subject := range subjects {
		if scope == traffic.ScopeUser {
			if id, ok := traffic.SubjectUser(subject); ok {
				subject = strconv.FormatInt(id, 10)
			}
		}
		if !seen[subject] {
			seen[subject] = true
			out = append(out, TrafficSubject{Subject: subject, Label: subjectLabel(scope, subject, names)})
		}
	}
	return out, nil
}

// DayUsage 是一天的用量。
type DayUsage struct {
	Day string `json:"day"`
	traffic.Bytes
}

// AutoPause 是一条生效中的自动暂停。
type AutoPause struct {
	Reason string    `json:"reason" enum:"daily,quota,expired"`
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
	Exempt     bool        `json:"exempt"`
	AutoPauses []AutoPause `json:"auto_pauses"`
	Daily      []DayUsage  `json:"daily"`
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
		out = make([]UserUsage, 0, len(users))
		for _, user := range users {
			item := UserUsage{UserID: user.ID, AutoPauses: []AutoPause{}, Daily: []DayUsage{}}
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
	return &item, nil
}

// TrafficCSV 把查询结果转成 CSV：时间、对象、上行、下行，时间按设置时区显示。
func (s *Service) TrafficCSV(ctx context.Context, result *TrafficResult) ([]byte, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	loc := location(settings)
	var out strings.Builder
	out.WriteString("\xef\xbb\xbf时间,对象,上行字节,下行字节\r\n")
	for index, bucket := range result.Buckets {
		for _, series := range result.Series {
			if series.Up[index] == 0 && series.Down[index] == 0 {
				continue
			}
			fmt.Fprintf(&out, "%s,%s,%d,%d\r\n", bucket.In(loc).Format(time.DateTime), csvField(series.Label), series.Up[index], series.Down[index])
		}
	}
	return []byte(out.String()), nil
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
