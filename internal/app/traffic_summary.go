package app

import (
	"context"
	"sort"
	"strconv"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/traffic"
)

// UserLivePoint 是一个用户在各 stack 上的实时速率。
type UserLivePoint struct {
	traffic.Bytes
	Stacks map[string]traffic.Bytes `json:"stacks"`
}

// StackTraffic 是总览中一个 stack 的入站用量和用户可归属用量。
type StackTraffic struct {
	StackID int64  `json:"stack_id"`
	Stack   string `json:"stack"`
	// Aggregator 表示只经其他 stack 的 relay 出网的聚合 stack，它的入站流量已在被引用 stack 计入，不进合计。
	Aggregator bool          `json:"aggregator"`
	Today      traffic.Bytes `json:"today"`
	Month      traffic.Bytes `json:"month"`
	UserToday  traffic.Bytes `json:"user_today"`
	UserMonth  traffic.Bytes `json:"user_month"`
}

// StackSeries 保留明细的 stack 归属，同名入站或用户不会跨 stack 混淆。
type StackSeries struct {
	StackID int64 `json:"stack_id"`
	// Aggregator 同 StackTraffic.Aggregator；入站和出站维度的合计不含它。
	Aggregator bool `json:"aggregator"`
	TrafficSeries
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

func (s *Service) trafficOverview(ctx context.Context, userID int64) (*TrafficOverview, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	now, loc := s.Now(), location(settings)
	out := &TrafficOverview{Enabled: settings.Traffic.Enabled, TopUsers: []UserTraffic{}, TopUsersToday: []UserTraffic{}, Live: []LivePoint{}, Stacks: []CollectStatus{}, ByStack: []StackTraffic{}}
	var names, stacks map[int64]string
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if names, err = userNames(ctx, q); err != nil {
			return err
		}
		if stacks, err = stackNames(ctx, q); err != nil {
			return err
		}
		out.FailedNotifications, err = q.CountFailedNotifications(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	if _, ok := names[userID]; userID != 0 && !ok {
		return nil, ErrNotFound
	}
	aggregators, err := s.aggregatorStacks(ctx)
	if err != nil {
		return nil, err
	}
	byStack := map[int64]*StackTraffic{}
	getStack := func(id int64) *StackTraffic {
		if byStack[id] == nil {
			byStack[id] = &StackTraffic{StackID: id, Stack: stackName(stacks, id), Aggregator: aggregators[id]}
		}
		return byStack[id]
	}
	for id := range stacks {
		getStack(id)
	}
	// 全部流量以入站计数为准；指定用户时仅累计该用户及其档案，避免重复相加入站和用户计数。
	scope := traffic.ScopeInbound
	if userID != 0 {
		scope = traffic.ScopeUser
	}
	for _, period := range []struct {
		level string
		from  time.Time
	}{
		{store.LevelDay, traffic.DayStart(now, loc)},
		{store.LevelMonth, traffic.MonthStart(now, loc)},
	} {
		rows, err := s.Store.QueryTraffic(ctx, period.level, scope, period.from.Unix(), now.Add(time.Hour).Unix(), nil)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if !matchesTrafficUser(row.Subject, userID) {
				continue
			}
			bytes := traffic.Bytes{Up: row.Up, Down: row.Down}
			stack := getStack(row.StackID)
			if period.level == store.LevelDay {
				stack.Today.Add(bytes)
			} else {
				stack.Month.Add(bytes)
			}
		}
	}
	totals := map[int64]traffic.Bytes{}
	todayTotals := map[int64]traffic.Bytes{}
	for _, period := range []struct {
		level  string
		from   time.Time
		to     time.Time
		totals map[int64]traffic.Bytes
	}{
		{store.LevelDay, traffic.DayStart(now, loc), traffic.DayStart(now, loc).AddDate(0, 0, 1), todayTotals},
		{store.LevelMonth, traffic.MonthStart(now, loc), traffic.MonthStart(now, loc).AddDate(0, 1, 0), totals},
	} {
		rows, err := s.Store.QueryTraffic(ctx, period.level, traffic.ScopeUser, period.from.Unix(), period.to.Unix(), nil)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if id, ok := traffic.SubjectUser(row.Subject); ok && matchesTrafficUser(row.Subject, userID) {
				bytes := traffic.Bytes{Up: row.Up, Down: row.Down}
				value := period.totals[id]
				value.Add(bytes)
				period.totals[id] = value
				stack := getStack(row.StackID)
				if period.level == store.LevelDay {
					stack.UserToday.Add(bytes)
				} else {
					stack.UserMonth.Add(bytes)
				}
			}
		}
	}
	s.traffic.mu.Lock()
	for key, bytes := range s.traffic.pending {
		if key.Scope == scope && matchesTrafficUser(key.Subject, userID) {
			stack := getStack(key.StackID)
			stack.Today.Add(bytes)
			stack.Month.Add(bytes)
		}
		if key.Scope == traffic.ScopeUser && matchesTrafficUser(key.Subject, userID) {
			stack := getStack(key.StackID)
			stack.UserToday.Add(bytes)
			stack.UserMonth.Add(bytes)
		}
	}
	for id, bytes := range s.traffic.users {
		if userID != 0 && userID != id {
			continue
		}
		value := totals[id]
		value.Add(bytes)
		totals[id] = value
		value = todayTotals[id]
		value.Add(bytes)
		todayTotals[id] = value
	}
	out.Live = append(out.Live, s.traffic.live...)
	for _, status := range s.traffic.statuses {
		out.Stacks = append(out.Stacks, status)
	}
	s.traffic.mu.Unlock()
	for _, stack := range byStack {
		// 全部流量按入站计数时，聚合 stack 的流量会在成员 stack 的 relay 入站再计一次，合计里跳过；
		// 指定用户时按用户计数，不存在重复。
		if userID != 0 || !stack.Aggregator {
			out.Today.Add(stack.Today)
			out.Month.Add(stack.Month)
		}
		out.UserToday.Add(stack.UserToday)
		out.UserMonth.Add(stack.UserMonth)
		out.ByStack = append(out.ByStack, *stack)
	}
	sort.Slice(out.ByStack, func(i, j int) bool { return out.ByStack[i].Stack < out.ByStack[j].Stack })
	sort.Slice(out.Stacks, func(i, j int) bool { return out.Stacks[i].Stack < out.Stacks[j].Stack })
	// 两个周期分别排序和取前十，避免按月截断后遗漏今日活跃用户。
	rankUsers := func(totals map[int64]traffic.Bytes) []UserTraffic {
		ranked := []UserTraffic{}
		for id, bytes := range totals {
			ranked = append(ranked, UserTraffic{ID: id, Name: subjectName(names, id), Bytes: bytes})
		}
		sort.Slice(ranked, func(i, j int) bool {
			a, b := ranked[i], ranked[j]
			if a.Total() != b.Total() {
				return a.Total() > b.Total()
			}
			return a.ID < b.ID
		})
		if len(ranked) > topUsers {
			ranked = ranked[:topUsers]
		}
		return ranked
	}
	out.TopUsers = rankUsers(totals)
	out.TopUsersToday = rankUsers(todayTotals)
	return out, nil
}

func (s *Service) userStackDaily(ctx context.Context, id int64, now time.Time, loc *time.Location) ([]StackDailyUsage, error) {
	from := traffic.DayStart(now, loc).AddDate(0, 0, -(dailySeriesDays - 1))
	result, err := s.QueryTraffic(ctx, TrafficQuery{Level: store.LevelDay, Scope: traffic.ScopeUser, From: from, To: now.Add(time.Hour), Subjects: []string{strconv.FormatInt(id, 10)}})
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
	for _, series := range result.ByStack {
		days := map[string]traffic.Bytes{}
		for index, bucket := range result.Buckets {
			days[traffic.Day(bucket, loc)] = traffic.Bytes{Up: series.Up[index], Down: series.Down[index]}
		}
		byStack[series.StackID] = days
	}
	today := traffic.Day(now, loc)
	s.traffic.mu.Lock()
	for key, bytes := range s.traffic.pending {
		if key.Scope != traffic.ScopeUser || !matchesTrafficUser(key.Subject, id) {
			continue
		}
		if byStack[key.StackID] == nil {
			byStack[key.StackID] = map[string]traffic.Bytes{}
		}
		value := byStack[key.StackID][today]
		value.Add(bytes)
		byStack[key.StackID][today] = value
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

// aggregatorStacks 按当前配置返回只经其他 stack 的 relay 出网的 stack，多个 stack 加总入站流量时排除它们。
func (s *Service) aggregatorStacks(ctx context.Context) (map[int64]bool, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for index := range doc.Stacks {
		if configdoc.RelaysOnly(&doc.Stacks[index]) {
			out[doc.Stacks[index].ID] = true
		}
	}
	return out, nil
}
