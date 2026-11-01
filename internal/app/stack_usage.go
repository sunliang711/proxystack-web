package app

import (
	"context"
	"sort"
	"time"

	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/traffic"
)

// StackUsage 是单个 Stack 的用量，指定用户时合并其全部凭据档案。
type StackUsage struct {
	StackID    int64            `json:"stack_id"`
	Stack      string           `json:"stack"`
	Enabled    bool             `json:"enabled"`
	Today      traffic.Bytes    `json:"today"`
	Month      traffic.Bytes    `json:"month"`
	Recent     traffic.Bytes    `json:"recent"`
	Daily      []DayUsage       `json:"daily"`
	Collection CollectStatus    `json:"collection"`
	ByUser     []StackUserUsage `json:"by_user"`
}

// StackUserUsage 是本 Stack 内一个用户的用量，合并该用户的全部凭据档案。
type StackUserUsage struct {
	UserID int64         `json:"user_id"`
	User   string        `json:"user"`
	Today  traffic.Bytes `json:"today"`
	Month  traffic.Bytes `json:"month"`
	Recent traffic.Bytes `json:"recent"`
}

// StackUsageDetail 统计本 Stack 的今日、本月及近 30 天用量，并计入尚未落库的采集增量。
func (s *Service) StackUsageDetail(ctx context.Context, stackID, userID int64) (*StackUsage, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := &StackUsage{StackID: stackID, Enabled: settings.Traffic.Enabled, Daily: []DayUsage{}, ByUser: []StackUserUsage{}}
	var users map[int64]string
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		stacks, err := stackNames(ctx, q)
		if err != nil {
			return err
		}
		var ok bool
		if out.Stack, ok = stacks[stackID]; !ok {
			return ErrNotFound
		}
		users, err = userNames(ctx, q)
		if err != nil {
			return err
		}
		if userID != 0 {
			if _, ok := users[userID]; !ok {
				return ErrNotFound
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	now, loc := s.Now(), location(settings)
	from := traffic.DayStart(now, loc).AddDate(0, 0, -(dailySeriesDays - 1))
	today := traffic.Day(now, loc)
	days := map[string]traffic.Bytes{}
	byUser := map[int64]*StackUserUsage{}
	addUser := func(subject string, bytes traffic.Bytes, level, day string) {
		id, ok := traffic.SubjectUser(subject)
		name := subjectName(users, id)
		if !ok || id <= 0 {
			id, name = 0, "未识别用户"
		}
		item := byUser[id]
		if item == nil {
			item = &StackUserUsage{UserID: id, User: name}
			byUser[id] = item
		}
		if level == store.LevelMonth {
			item.Month.Add(bytes)
		} else {
			item.Recent.Add(bytes)
			if day == today {
				item.Today.Add(bytes)
			}
		}
	}
	// 全部流量使用入站计数；指定用户使用用户计数，两个口径分别统计，避免重复累计。
	scope := traffic.ScopeInbound
	if userID != 0 {
		scope = traffic.ScopeUser
	}
	scopes := []string{scope}
	if userID == 0 {
		scopes = append(scopes, traffic.ScopeUser)
	}
	for _, queryScope := range scopes {
		for _, period := range []struct {
			level string
			from  time.Time
		}{
			{store.LevelDay, from},
			{store.LevelMonth, traffic.MonthStart(now, loc)},
		} {
			rows, err := s.Store.QueryTraffic(ctx, period.level, queryScope, period.from.Unix(), now.Add(time.Hour).Unix(), []int64{stackID})
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if !matchesTrafficUser(row.Subject, userID) {
					continue
				}
				bytes := traffic.Bytes{Up: row.Up, Down: row.Down}
				if userID == 0 && queryScope == traffic.ScopeUser {
					addUser(row.Subject, bytes, period.level, traffic.Day(time.Unix(row.Bucket, 0), loc))
					continue
				}
				if period.level == store.LevelMonth {
					out.Month.Add(bytes)
				} else {
					day := traffic.Day(time.Unix(row.Bucket, 0), loc)
					value := days[day]
					value.Add(bytes)
					days[day] = value
				}
			}
		}
	}
	out.Collection = CollectStatus{StackID: stackID, Stack: out.Stack}
	s.traffic.mu.Lock()
	for key, bytes := range s.traffic.pending {
		if key.StackID != stackID {
			continue
		}
		if userID == 0 && key.Scope == traffic.ScopeUser {
			addUser(key.Subject, bytes, store.LevelDay, today)
			addUser(key.Subject, bytes, store.LevelMonth, today)
		}
		if key.Scope != scope || !matchesTrafficUser(key.Subject, userID) {
			continue
		}
		value := days[today]
		value.Add(bytes)
		days[today] = value
		out.Month.Add(bytes)
	}
	if status, ok := s.traffic.statuses[stackID]; ok {
		out.Collection = status
	}
	s.traffic.mu.Unlock()
	out.Today = days[today]
	for offset := 0; offset < dailySeriesDays; offset++ {
		day := traffic.Day(from.AddDate(0, 0, offset), loc)
		out.Daily = append(out.Daily, DayUsage{Day: day, Bytes: days[day]})
		out.Recent.Add(days[day])
	}
	for _, item := range byUser {
		out.ByUser = append(out.ByUser, *item)
	}
	sort.Slice(out.ByUser, func(i, j int) bool {
		a, b := out.ByUser[i], out.ByUser[j]
		if a.Month.Total() != b.Month.Total() {
			return a.Month.Total() > b.Month.Total()
		}
		return a.UserID < b.UserID
	})
	return out, nil
}
