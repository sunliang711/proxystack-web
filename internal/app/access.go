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

	"proxystack-web/internal/access"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/traffic"
	"proxystack-web/internal/units"
)

const (
	// accessTick 是访问统计检查设置和写库的间隔。
	accessTick = 30 * time.Second
	// accessRetryDelay 是日志读取中断后重新开始前的等待时间。
	accessRetryDelay = 5 * time.Second
	// accessListLimit 是域名列表的默认和最大条数。
	accessListLimit = 500
)

// accessKey 是访问计数的维度：用户、日期（设置时区）和主域名。
type accessKey struct {
	userID int64
	day    string
	domain string
}

// accessCount 是一个维度上放行和被拦截的连接次数。
type accessCount struct {
	count   int64
	blocked int64
}

// accessState 是访问统计的内存状态。pending 是已读取但尚未写库的计数，cursor 是已读取的最后一条日志的书签；
// 两者总是在同一把锁下更新并一起写库，书签之前的日志都已计入，重启后从书签继续读取不会重复计数。
// runMu 保护日志读取的启停（cancel、done、key），只由 AccessTick 和 StopAccess 使用。
type accessState struct {
	mu      sync.Mutex
	loaded  bool
	pending map[accessKey]accessCount
	cursor  string
	saved   string
	loc     *time.Location
	lastAt  time.Time
	running bool
	err     string

	runMu  sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	key    string
}

// runAccessStats 是访问统计循环。退出前停止读取并写库，正常停止不丢数据。
func (s *Service) runAccessStats(ctx context.Context) {
	for {
		s.AccessTick(ctx)
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s.StopAccess(shutdown)
			return
		case <-time.After(accessTick):
		}
	}
}

// AccessTick 执行一轮访问统计：按设置启停日志读取（读取的 unit 随已应用的 stack 变化），并把计数写库。
// 日志读取在后台持续进行，生命周期跟随 ctx。
func (s *Service) AccessTick(ctx context.Context) {
	s.access.runMu.Lock()
	defer s.access.runMu.Unlock()
	enabled, unitNames, err := s.accessUnits(ctx)
	if err != nil {
		slog.Warn("access stats", "err", err)
	} else {
		// key 的零值（进程刚启动或 StopAccess 之后）与 off、on:… 都不同，第一轮总会执行一次状态切换，
		// 保证启动时统计处于关闭状态也会清掉数据库里残留的书签。
		next := "off"
		if enabled {
			next = "on:" + strings.Join(unitNames, ",")
		}
		if next != s.access.key {
			s.stopAccessFollower()
			if err := s.flushAccess(ctx); err != nil {
				slog.Warn("access flush", "err", err)
			}
			switch {
			case !enabled:
				// 书签删除失败时不记下 key，下一轮重试。
				if s.resetAccess(ctx) {
					s.access.key = next
				}
			case len(unitNames) == 0:
				s.access.key = next
			default:
				// 书签读取失败时不记下 key，下一轮重试。
				if err := s.loadAccessCursor(ctx); err != nil {
					slog.Warn("access cursor", "err", err)
					break
				}
				s.access.cancel, s.access.done = s.startAccessFollower(ctx, unitNames)
				s.access.key = next
			}
		}
	}
	if err := s.flushAccess(ctx); err != nil {
		slog.Warn("access flush", "err", err)
	}
}

// StopAccess 停止日志读取并写库；之后的 AccessTick 会重新开始读取。
func (s *Service) StopAccess(ctx context.Context) {
	s.access.runMu.Lock()
	defer s.access.runMu.Unlock()
	s.stopAccessFollower()
	s.access.key = ""
	if err := s.flushAccess(ctx); err != nil {
		slog.Warn("access flush", "err", err)
	}
}

func (s *Service) stopAccessFollower() {
	if s.access.cancel != nil {
		s.access.cancel()
		<-s.access.done
		s.access.cancel, s.access.done = nil, nil
	}
}

// startAccessFollower 在后台开始读取日志，返回停止函数和读取结束的信号。
func (s *Service) startAccessFollower(ctx context.Context, unitNames []string) (context.CancelFunc, chan struct{}) {
	follow, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.followAccess(follow, unitNames)
	}()
	return cancel, done
}

// accessUnits 返回访问统计是否开启，以及要读取日志的 xray unit（已应用、启用并输出访问日志的 stack）。
// 同时刷新统计使用的时区。
func (s *Service) accessUnits(ctx context.Context) (bool, []string, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return false, nil, err
	}
	s.access.mu.Lock()
	s.access.loc = location(settings)
	s.access.mu.Unlock()
	if !settings.Traffic.AccessStats {
		return false, nil, nil
	}
	targets, err := s.collectTargets(ctx)
	if err != nil {
		return true, nil, err
	}
	names := []string{}
	for _, target := range targets {
		if target.accessLog {
			names = append(names, units.Name(units.Xray, target.name))
		}
	}
	slices.Sort(names)
	return true, names, nil
}

// loadAccessCursor 在本进程第一次开始读取前从数据库取回书签。
func (s *Service) loadAccessCursor(ctx context.Context) error {
	s.access.mu.Lock()
	loaded := s.access.loaded
	s.access.mu.Unlock()
	if loaded {
		return nil
	}
	var cursor string
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		row, err := q.GetAccessCursor(ctx)
		if err == nil {
			cursor = row.Cursor
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.access.mu.Lock()
	defer s.access.mu.Unlock()
	if !s.access.loaded {
		s.access.cursor, s.access.saved, s.access.loaded = cursor, cursor, true
	}
	return nil
}

// resetAccess 在统计关闭时丢弃书签和未写库的计数：之后重新开启只统计开启以后的访问，不补读关闭期间的日志。
// 返回书签是否已从数据库删除。
func (s *Service) resetAccess(ctx context.Context) bool {
	s.access.mu.Lock()
	hadCursor := s.access.cursor != "" || s.access.saved != "" || !s.access.loaded
	s.access.pending, s.access.cursor, s.access.saved, s.access.loaded = nil, "", "", true
	s.access.running, s.access.err = false, ""
	s.access.mu.Unlock()
	if !hadCursor {
		return true
	}
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error { return q.DeleteAccessCursor(ctx) }); err != nil {
		slog.Warn("access reset", "err", err)
		s.access.mu.Lock()
		s.access.loaded = false
		s.access.mu.Unlock()
		return false
	}
	return true
}

// followAccess 持续读取 unitNames 的日志，读取中断时等待后从最新书签继续。书签已失效（日志已被轮转清理）时
// 从当前位置重新开始。
func (s *Service) followAccess(ctx context.Context, unitNames []string) {
	for {
		s.access.mu.Lock()
		after := s.access.cursor
		s.access.running, s.access.err = true, ""
		s.access.mu.Unlock()
		read := false
		err := s.Logs.Follow(ctx, unitNames, after, func(entry units.LogEntry) {
			read = true
			s.recordAccess(entry)
		})
		if ctx.Err() != nil {
			s.access.mu.Lock()
			s.access.running = false
			s.access.mu.Unlock()
			return
		}
		message := "日志读取意外结束"
		if err != nil {
			message = err.Error()
		}
		s.access.mu.Lock()
		s.access.running, s.access.err = false, message
		if after != "" && !read && s.access.cursor == after && strings.Contains(strings.ToLower(message), "seek to cursor") {
			s.access.cursor = ""
			s.access.err = "日志书签已失效，从当前位置重新开始统计：" + message
		}
		s.access.mu.Unlock()
		slog.Warn("access follow", "err", message)
		select {
		case <-ctx.Done():
			return
		case <-time.After(accessRetryDelay):
		}
	}
}

// recordAccess 解析一条日志并累加计数；不是访问记录的日志只推进书签。
func (s *Service) recordAccess(entry units.LogEntry) {
	record, ok := access.ParseLine(entry.Message)
	s.access.mu.Lock()
	defer s.access.mu.Unlock()
	if entry.Cursor != "" {
		s.access.cursor = entry.Cursor
	}
	at := entry.Time
	if at.IsZero() {
		at = s.Now()
	}
	if at.After(s.access.lastAt) {
		s.access.lastAt = at
	}
	if !ok {
		return
	}
	loc := s.access.loc
	if loc == nil {
		loc = time.UTC
	}
	if s.access.pending == nil {
		s.access.pending = map[accessKey]accessCount{}
	}
	key := accessKey{userID: record.UserID, day: traffic.Day(at, loc), domain: record.Domain}
	total := s.access.pending[key]
	if record.Blocked {
		total.blocked++
	} else {
		total.count++
	}
	s.access.pending[key] = total
}

// flushAccess 把未写库的计数和书签在同一事务中写入；已删除用户的计数丢弃。写入失败时计数放回内存。
func (s *Service) flushAccess(ctx context.Context) error {
	s.access.mu.Lock()
	pending, cursor := s.access.pending, s.access.cursor
	if len(pending) == 0 && cursor == s.access.saved {
		s.access.mu.Unlock()
		return nil
	}
	s.access.pending = nil
	s.access.mu.Unlock()

	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		users, err := q.ListUsers(ctx)
		if err != nil {
			return err
		}
		known := make(map[int64]bool, len(users))
		for _, user := range users {
			known[user.ID] = true
		}
		for key, total := range pending {
			if !known[key.userID] {
				continue
			}
			if err := q.AddAccessDomain(ctx, dbq.AddAccessDomainParams{
				UserID: key.userID, Day: key.day, Domain: key.domain, Count: total.count, Blocked: total.blocked,
			}); err != nil {
				return err
			}
		}
		if cursor == "" {
			return q.DeleteAccessCursor(ctx)
		}
		return q.PutAccessCursor(ctx, dbq.PutAccessCursorParams{Cursor: cursor, At: s.Now().Unix()})
	})
	s.access.mu.Lock()
	defer s.access.mu.Unlock()
	if err != nil {
		if s.access.pending == nil {
			s.access.pending = map[accessKey]accessCount{}
		}
		for key, total := range pending {
			merged := s.access.pending[key]
			merged.count += total.count
			merged.blocked += total.blocked
			s.access.pending[key] = merged
		}
		return err
	}
	s.access.saved = cursor
	return nil
}

// pruneAccess 删除超过保留天数的访问统计。
func (s *Service) pruneAccess(ctx context.Context, days int, now time.Time, loc *time.Location) {
	if days <= 0 {
		return
	}
	cutoff := traffic.Day(now.AddDate(0, 0, -days), loc)
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error { return q.DeleteAccessBefore(ctx, cutoff) }); err != nil {
		slog.Warn("access prune", "err", err)
	}
}

// AccessStackStatus 是一个 stack 的访问日志状态。
type AccessStackStatus struct {
	StackID int64  `json:"stack_id"`
	Stack   string `json:"stack"`
	// AccessLog 表示该 stack 已应用的 xray 配置输出访问日志；关闭时它的访问不会被统计。
	AccessLog bool `json:"access_log"`
	// RateLimit 是 journald 对该 xray 的限速：ok 已放开，limited 沿用默认限速（连接多时会少算），unknown 无法判断。
	RateLimit string `json:"rate_limit" enum:"ok,limited,unknown"`
}

// AccessStatus 是访问统计的运行状态。
type AccessStatus struct {
	Enabled       bool   `json:"enabled"`
	Running       bool   `json:"running"`
	RetentionDays int    `json:"retention_days"`
	Timezone      string `json:"timezone"`
	// LastAt 是最近读到的一条日志的时间，零值表示本进程还没有读到日志。
	LastAt time.Time           `json:"last_at"`
	Error  string              `json:"error"`
	Stacks []AccessStackStatus `json:"stacks"`
}

// AccessStatusData 返回访问统计的开关、读取状态和各 stack 的访问日志情况。
func (s *Service) AccessStatusData(ctx context.Context) (*AccessStatus, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	targets, err := s.collectTargets(ctx)
	if err != nil {
		return nil, err
	}
	out := &AccessStatus{
		Enabled: settings.Traffic.AccessStats, RetentionDays: settings.Traffic.AccessRetentionDays,
		Timezone: location(settings).String(), Stacks: []AccessStackStatus{},
	}
	s.access.mu.Lock()
	out.Running, out.LastAt, out.Error = s.access.running, s.access.lastAt, s.access.err
	s.access.mu.Unlock()
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, units.Name(units.Xray, target.name))
	}
	var limited map[string]bool
	if limits, ok := s.Units.(units.LogRateLimits); ok {
		limited = limits.LogRateLimited(ctx, names)
	}
	for _, target := range targets {
		status := AccessStackStatus{StackID: target.stackID, Stack: target.name, AccessLog: target.accessLog, RateLimit: "unknown"}
		if value, ok := limited[units.Name(units.Xray, target.name)]; ok {
			status.RateLimit = "ok"
			if value {
				status.RateLimit = "limited"
			}
		}
		out.Stacks = append(out.Stacks, status)
	}
	return out, nil
}

// AccessQuery 是访问统计的查询条件。From、To 是时间范围（To 不含），按设置时区换算成日期。
type AccessQuery struct {
	From, To time.Time
	Limit    int64
}

// accessDays 把时间范围换算成设置时区下的起止日期（都含）。
func (s *Service) accessDays(ctx context.Context, query AccessQuery) (string, string, error) {
	if !query.To.After(query.From) {
		return "", "", &FieldError{Field: "to", Message: "must be after from"}
	}
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return "", "", err
	}
	loc := location(settings)
	return traffic.Day(query.From, loc), traffic.Day(query.To.Add(-time.Second), loc), nil
}

// accessLimit 返回域名条数上限：负数表示不限（导出），0 或超过上限时取上限。
func accessLimit(limit int64) int64 {
	if limit < 0 {
		return -1
	}
	if limit == 0 || limit > accessListLimit {
		return accessListLimit
	}
	return limit
}

// 主域名在域名黑名单中的状态：被全局名单或该用户的名单整体拦截（含全部子域名）。
const (
	BlockListedGlobal = "global"
	BlockListedUser   = "user"
)

// AccessDomainCount 是一个主域名的访问次数。
type AccessDomainCount struct {
	Domain string `json:"domain"`
	// Count 是放行的连接次数，Blocked 是被域名黑名单拦截的连接次数。
	Count   int64 `json:"count"`
	Blocked int64 `json:"blocked"`
	// Days 是有访问的天数（用户视角），Users 是访问过的用户数（全局视角）。
	Days  int64 `json:"days,omitempty"`
	Users int64 `json:"users,omitempty"`
	// Listed 是该主域名当前在哪个黑名单中被整体拦截：global、user（仅用户视角）或空。
	Listed string `json:"listed" enum:",global,user"`
}

// blockListed 返回主域名被哪个名单整体拦截（条目是它本身或它的上级域名，不含 full: 条目）。
func blockListed(name string, global, user []string) string {
	covers := func(entries []string) bool {
		for _, entry := range entries {
			if !strings.HasPrefix(entry, configdoc.BlockFullPrefix) && configdoc.BlockedDomainMatches(entry, name) {
				return true
			}
		}
		return false
	}
	switch {
	case covers(global):
		return BlockListedGlobal
	case covers(user):
		return BlockListedUser
	}
	return ""
}

// AccessDomains 是一段时间内的域名访问排行。
type AccessDomains struct {
	FromDay string `json:"from_day"`
	ToDay   string `json:"to_day"`
	// Total 是范围内放行的访问总次数，Blocked 是被拦截的总次数，DomainCount 是不同主域名的个数；Domains 只含排名靠前的部分。
	Total       int64               `json:"total"`
	Blocked     int64               `json:"blocked"`
	DomainCount int64               `json:"domain_count"`
	Domains     []AccessDomainCount `json:"domains"`
}

// AccessTopDomains 返回全部用户的主域名访问排行。
func (s *Service) AccessTopDomains(ctx context.Context, query AccessQuery) (*AccessDomains, error) {
	from, to, err := s.accessDays(ctx, query)
	if err != nil {
		return nil, err
	}
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := &AccessDomains{FromDay: from, ToDay: to, Domains: []AccessDomainCount{}}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		summary, err := q.CountAccessDomains(ctx, dbq.CountAccessDomainsParams{FromDay: from, ToDay: to})
		if err != nil {
			return err
		}
		out.Total, out.Blocked, out.DomainCount = summary.Count, summary.Blocked, summary.Domains
		rows, err := q.ListAccessTopDomains(ctx, dbq.ListAccessTopDomainsParams{FromDay: from, ToDay: to, RowLimit: accessLimit(query.Limit)})
		for _, row := range rows {
			out.Domains = append(out.Domains, AccessDomainCount{
				Domain: row.Domain, Count: row.Count, Blocked: row.Blocked, Users: row.Users,
				Listed: blockListed(row.Domain, settings.Traffic.BlockedDomains, nil),
			})
		}
		return err
	})
	return out, err
}

// AccessUserDomains 返回一个用户的主域名访问排行。
func (s *Service) AccessUserDomains(ctx context.Context, userID int64, query AccessQuery) (*AccessDomains, error) {
	from, to, err := s.accessDays(ctx, query)
	if err != nil {
		return nil, err
	}
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := &AccessDomains{FromDay: from, ToDay: to, Domains: []AccessDomainCount{}}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		users, err := q.ListUsers(ctx)
		if err != nil {
			return err
		}
		var own []string
		for _, user := range users {
			if user.ID == userID {
				own = configdoc.SplitBlockedDomains(user.BlockedDomains)
			}
		}
		rows, err := q.ListAccessUserDomains(ctx, dbq.ListAccessUserDomainsParams{UserID: userID, FromDay: from, ToDay: to, RowLimit: -1})
		if err != nil {
			return err
		}
		out.DomainCount = int64(len(rows))
		limit := accessLimit(query.Limit)
		for index, row := range rows {
			out.Total += row.Count
			out.Blocked += row.Blocked
			if limit < 0 || int64(index) < limit {
				out.Domains = append(out.Domains, AccessDomainCount{
					Domain: row.Domain, Count: row.Count, Blocked: row.Blocked, Days: row.Days,
					Listed: blockListed(row.Domain, settings.Traffic.BlockedDomains, own),
				})
			}
		}
		return nil
	})
	return out, err
}

// AccessDomainUser 是访问过某个主域名的一个用户。
type AccessDomainUser struct {
	UserID  int64  `json:"user_id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
	Count   int64  `json:"count"`
	Blocked int64  `json:"blocked"`
	Days    int64  `json:"days"`
}

// AccessDomainUsers 是访问过某个主域名的用户列表。
type AccessDomainUsers struct {
	// Domain 是归并后的主域名，输入 www.example.com 时为 example.com。
	Domain  string             `json:"domain"`
	FromDay string             `json:"from_day"`
	ToDay   string             `json:"to_day"`
	Users   []AccessDomainUser `json:"users"`
}

// AccessDomainUsersData 返回访问过某个域名的用户；输入的域名先归并为主域名。
func (s *Service) AccessDomainUsersData(ctx context.Context, name string, query AccessQuery) (*AccessDomainUsers, error) {
	normalized := access.Domain(name)
	if normalized == "" {
		return nil, &FieldError{Field: "domain", Message: "must be a domain name"}
	}
	from, to, err := s.accessDays(ctx, query)
	if err != nil {
		return nil, err
	}
	out := &AccessDomainUsers{Domain: normalized, FromDay: from, ToDay: to, Users: []AccessDomainUser{}}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		current, err := userNames(ctx, q)
		if err != nil {
			return err
		}
		history, err := q.ListTrafficNames(ctx)
		if err != nil {
			return err
		}
		rows, err := q.ListAccessDomainUsers(ctx, dbq.ListAccessDomainUsersParams{Domain: normalized, FromDay: from, ToDay: to})
		if err != nil {
			return err
		}
		for _, row := range rows {
			user := AccessDomainUser{UserID: row.UserID, Count: row.Count, Blocked: row.Blocked, Days: row.Days}
			if name, ok := current[row.UserID]; ok {
				user.Name = name
			} else {
				user.Deleted = true
				user.Name = "#" + strconv.FormatInt(row.UserID, 10)
				for _, item := range history {
					if item.Kind == "user" && item.ID == row.UserID {
						user.Name = item.Name
					}
				}
			}
			out.Users = append(out.Users, user)
		}
		return nil
	})
	return out, err
}

// AccessDomainsCSV 把域名排行导出为 CSV。
func AccessDomainsCSV(result *AccessDomains, perUser bool) []byte {
	var b strings.Builder
	if perUser {
		b.WriteString("domain,count,blocked,days\n")
	} else {
		b.WriteString("domain,count,blocked,users\n")
	}
	for _, row := range result.Domains {
		extra := row.Users
		if perUser {
			extra = row.Days
		}
		b.WriteString(csvField(row.Domain) + "," + strconv.FormatInt(row.Count, 10) + "," + strconv.FormatInt(row.Blocked, 10) + "," +
			strconv.FormatInt(extra, 10) + "\n")
	}
	return []byte(b.String())
}
