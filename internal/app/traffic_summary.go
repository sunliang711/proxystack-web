package app

import (
	"context"
	"sort"
	"strconv"
	"time"

	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/traffic"
)

// UserLivePoint 是一个用户在各 stack 上的实时速率。
type UserLivePoint struct {
	traffic.Bytes
	Stacks map[string]traffic.Bytes `json:"stacks"`
}

// StackDailyUsage 是用户在一个 stack 上的今日用量和最近 30 天趋势。
type StackDailyUsage struct {
	StackID int64         `json:"stack_id"`
	Stack   string        `json:"stack"`
	Today   traffic.Bytes `json:"today"`
	Recent  traffic.Bytes `json:"recent"`
	Daily   []DayUsage    `json:"daily"`
}

func stackNames(ctx context.Context, q *dbq.Queries) (map[int64]string, error) {
	rows, err := q.ListStacks(ctx)
	names := make(map[int64]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, err
}

func stackName(names map[int64]string, id int64) string {
	if name, ok := names[id]; ok {
		return name
	}
	return "已删除 Stack #" + strconv.FormatInt(id, 10)
}

func matchesTrafficUser(subject string, userID int64) bool {
	id, ok := traffic.SubjectUser(subject)
	return userID == 0 || ok && id == userID
}

func (s *Service) userStackDaily(ctx context.Context, id int64, now time.Time, loc *time.Location) ([]StackDailyUsage, error) {
	from := traffic.DayStart(now, loc).AddDate(0, 0, -(dailySeriesDays - 1))
	rows, err := s.Store.QueryTraffic(ctx, store.LevelDay, traffic.ScopeUser, from.Unix(), now.Add(time.Hour).Unix(), nil)
	if err != nil {
		return nil, err
	}
	var names map[int64]string
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		names, err = stackNames(ctx, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	byStack := map[int64]map[string]traffic.Bytes{}
	add := func(stackID int64, day string, bytes traffic.Bytes) {
		if byStack[stackID] == nil {
			byStack[stackID] = map[string]traffic.Bytes{}
		}
		value := byStack[stackID][day]
		value.Add(bytes)
		byStack[stackID][day] = value
	}
	for _, row := range rows {
		if matchesTrafficUser(row.Subject, id) {
			add(row.StackID, traffic.Day(time.Unix(row.Bucket, 0), loc), traffic.Bytes{Up: row.Up, Down: row.Down})
		}
	}
	today := traffic.Day(now, loc)
	s.traffic.mu.Lock()
	for key, bytes := range s.traffic.pending {
		if key.Scope == traffic.ScopeUser && matchesTrafficUser(key.Subject, id) {
			add(key.StackID, today, bytes)
		}
	}
	s.traffic.mu.Unlock()
	out := make([]StackDailyUsage, 0, len(byStack))
	for stackID, days := range byStack {
		item := StackDailyUsage{StackID: stackID, Stack: stackName(names, stackID), Today: days[today], Daily: []DayUsage{}}
		for offset := 0; offset < dailySeriesDays; offset++ {
			day := traffic.Day(from.AddDate(0, 0, offset), loc)
			item.Daily = append(item.Daily, DayUsage{Day: day, Bytes: days[day]})
			item.Recent.Add(days[day])
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Stack < out[j].Stack })
	return out, nil
}

// TrafficOverview 是总览页的数据。
type TrafficOverview struct {
	Enabled             bool             `json:"enabled"`
	FailedNotifications int64            `json:"failed_notifications"`
	Timezone            string           `json:"timezone"`
	Today               OverviewPeriod   `json:"today"`
	Month               OverviewPeriod   `json:"month"`
	Live                []LivePoint      `json:"live"`
	TopToday            []TrafficRankRow `json:"top_today"`
	TopMonth            []TrafficRankRow `json:"top_month"`
	ByStack             []OverviewStack  `json:"by_stack"`
	Stacks              []CollectStatus  `json:"stacks"`
	// Gaps 是最近 24 小时内的数据缺口。
	Gaps []TrafficGap `json:"gaps"`
}

// OverviewPeriod 是今日或本月的合计，以及上一期同时段（昨日、上月的同等已过时长）的合计。
type OverviewPeriod struct {
	From         time.Time `json:"from"`
	PreviousFrom time.Time `json:"previous_from"`
	PreviousTo   time.Time `json:"previous_to"`
	// Total 是全部流量：各 stack 入站之和，不含 auto stack。
	Total traffic.Bytes `json:"total"`
	// Users 是归属到用户的流量。
	Users    traffic.Bytes `json:"users"`
	Previous traffic.Bytes `json:"previous"`
}

// OverviewStack 是总览中一个 stack 的今日和本月用量。
type OverviewStack struct {
	StackID int64  `json:"stack_id"`
	Stack   string `json:"stack"`
	Role    string `json:"role"`
	// Excluded 表示 auto stack，合计不含它。
	Excluded bool                `json:"excluded"`
	Deleted  bool                `json:"deleted"`
	Today    OverviewStackPeriod `json:"today"`
	Month    OverviewStackPeriod `json:"month"`
}

// OverviewStackPeriod 是一个 stack 在一个周期内的入站用量，以及归属到用户、经 relay 入站和共享账号等的部分。
type OverviewStackPeriod struct {
	Total  traffic.Bytes `json:"total"`
	Users  traffic.Bytes `json:"users"`
	Relay  traffic.Bytes `json:"relay"`
	Shared traffic.Bytes `json:"shared"`
}

// overviewTop 是总览排行的人数。
const overviewTop = 5

// TrafficOverviewData 汇总今日、本月的流量和上一期同时段对比、实时速率、排行、各 stack 用量和最近的数据缺口，
// 并计入尚未落库的采集增量。
func (s *Service) TrafficOverviewData(ctx context.Context) (*TrafficOverview, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	now, loc := s.Now(), location(settings)
	out := &TrafficOverview{Enabled: settings.Traffic.Enabled, Timezone: loc.String(), Live: []LivePoint{}, TopToday: []TrafficRankRow{}, TopMonth: []TrafficRankRow{}, ByStack: []OverviewStack{}, Stacks: []CollectStatus{}}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		out.FailedNotifications, err = q.CountFailedNotifications(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	dir, err := s.trafficDirectory(ctx)
	if err != nil {
		return nil, err
	}
	stacks := map[int64]*OverviewStack{}
	get := func(id int64) *OverviewStack {
		if stacks[id] == nil {
			name, deleted := dir.stack(id)
			stacks[id] = &OverviewStack{StackID: id, Stack: name, Role: dir.roles[id], Excluded: dir.auto(id), Deleted: deleted}
		}
		return stacks[id]
	}
	for _, id := range dir.stackIDs {
		get(id)
	}
	dayStart, monthStart := traffic.DayStart(now, loc), traffic.MonthStart(now, loc)
	for _, period := range []struct {
		target *OverviewPeriod
		from   time.Time
		to     time.Time
		pick   func(*OverviewStack) *OverviewStackPeriod
		top    *[]TrafficRankRow
	}{
		{&out.Today, dayStart, dayStart.AddDate(0, 0, 1), func(item *OverviewStack) *OverviewStackPeriod { return &item.Today }, &out.TopToday},
		{&out.Month, monthStart, monthStart.AddDate(0, 1, 0), func(item *OverviewStack) *OverviewStackPeriod { return &item.Month }, &out.TopMonth},
	} {
		period.target.From = period.from
		period.target.PreviousFrom, period.target.PreviousTo = previousRange(period.from, period.to, now, loc)
		inbound, err := s.sumTraffic(ctx, settings, traffic.ScopeInbound, period.from, period.to, nil)
		if err != nil {
			return nil, err
		}
		users, err := s.sumTraffic(ctx, settings, traffic.ScopeUser, period.from, period.to, nil)
		if err != nil {
			return nil, err
		}
		relays := map[int64]traffic.Bytes{}
		for key, bytes := range inbound {
			item := period.pick(get(key.StackID))
			item.Total.Add(bytes)
			if isRelayInbound(key.Subject) {
				value := relays[key.StackID]
				value.Add(bytes)
				relays[key.StackID] = value
			}
		}
		ranking := map[int64]traffic.Bytes{}
		for key, bytes := range users {
			period.pick(get(key.StackID)).Users.Add(bytes)
			period.target.Users.Add(bytes)
			if id, _ := subjectUserID(key.Subject); id > 0 {
				value := ranking[id]
				value.Add(bytes)
				ranking[id] = value
			}
		}
		for id, item := range stacks {
			usage := period.pick(item)
			usage.Relay, usage.Shared = splitBytes(usage.Total, usage.Users, relays[id], item.Excluded)
			if !item.Excluded {
				period.target.Total.Add(usage.Total)
			}
		}
		previous, err := s.sumTraffic(ctx, settings, traffic.ScopeInbound, period.target.PreviousFrom, period.target.PreviousTo, nil)
		if err != nil {
			return nil, err
		}
		for key, bytes := range previous {
			if !dir.auto(key.StackID) {
				period.target.Previous.Add(bytes)
			}
		}
		rows := []TrafficRankRow{}
		for id, bytes := range ranking {
			if bytes.Total() == 0 {
				continue
			}
			name, deleted := dir.user(id)
			rows = append(rows, TrafficRankRow{UserID: id, Name: name, Deleted: deleted, Bytes: bytes})
		}
		sortRankRows(rows)
		*period.top = rows[:min(len(rows), overviewTop)]
	}
	for _, item := range stacks {
		if item.Deleted && item.Month.Total.Total() == 0 && item.Month.Users.Total() == 0 {
			continue
		}
		out.ByStack = append(out.ByStack, *item)
	}
	sort.Slice(out.ByStack, func(i, j int) bool { return out.ByStack[i].Stack < out.ByStack[j].Stack })
	s.traffic.mu.Lock()
	out.Live = append(out.Live, s.traffic.live...)
	for _, status := range s.traffic.statuses {
		out.Stacks = append(out.Stacks, status)
	}
	s.traffic.mu.Unlock()
	sort.Slice(out.Stacks, func(i, j int) bool { return out.Stacks[i].Stack < out.Stacks[j].Stack })
	if out.Gaps, err = s.trafficGaps(ctx, dir, now.Add(-24*time.Hour), now.Add(time.Minute)); err != nil {
		return nil, err
	}
	return out, nil
}

// splitBytes 是 splitUnattributed 的合计版本，用于没有时间桶的总览。
func splitBytes(inbound, users, relayIn traffic.Bytes, auto bool) (traffic.Bytes, traffic.Bytes) {
	one := func(values ...int64) *statsSeries { return &statsSeries{up: values[:1], down: values[1:]} }
	relay, shared := splitUnattributed(one(inbound.Up, inbound.Down), one(users.Up, users.Down), one(relayIn.Up, relayIn.Down), auto)
	return relay.total(), shared.total()
}
