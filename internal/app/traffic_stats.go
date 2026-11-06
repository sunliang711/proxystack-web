package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/traffic"
)

// 统计视角。
const (
	StatsViewUser  = "user"
	StatsViewStack = "stack"
)

// 统计行的类型。
const (
	StatsRowTotal   = "total"
	StatsRowUser    = "user"
	StatsRowStack   = "stack"
	StatsRowProfile = "profile"
	// StatsRowUnknown 是无法映射到用户的用户计数，例如删除前未采集完的计数和占位用户。
	StatsRowUnknown = "unknown"
	// StatsRowRelay 是 edge stack 中未归属到用户、经 relay 入站进入的流量，主要来自 auto stack 的转发。
	StatsRowRelay = "relay"
	// StatsRowShared 是其余未归属到用户的流量，例如 SOCKS5 / HTTP 共享账号。
	StatsRowShared = "shared"
)

// maxStatsBuckets 限制单次查询的时间桶数量，约为一年半的小时数。
const maxStatsBuckets = 13000

// TrafficStatsQuery 是流量统计的查询条件。
type TrafficStatsQuery struct {
	View    string
	Level   string // hour / day / month / year
	From    time.Time
	To      time.Time
	Users   []int64
	Stacks  []int64
	Compare bool
}

// TrafficStatsRow 是统计结果中的一行。Series 只在需要绘制趋势时返回，与 TrafficStats.Buckets 一一对应。
type TrafficStatsRow struct {
	Kind    string `json:"kind" enum:"total,user,stack,profile,unknown,relay,shared"`
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
	Role    string `json:"role"`
	// Excluded 表示 auto stack：它的流量经 edge stack 的 relay 入站出去，已在 edge stack 计入，合计不含它。
	Excluded bool `json:"excluded"`
	traffic.Bytes
	SeriesUp   []int64 `json:"series_up,omitempty"`
	SeriesDown []int64 `json:"series_down,omitempty"`
	// Previous 是上一期同等时长的用量，只在要求对比时返回。
	Previous *traffic.Bytes `json:"previous,omitempty"`
	// Peak 是单分钟平均速率的最大值（字节/秒，上下行合计），分钟明细覆盖整个范围时才返回。
	Peak     *int64            `json:"peak,omitempty"`
	Children []TrafficStatsRow `json:"children"`
	Profiles []TrafficStatsRow `json:"profiles"`
}

// TrafficGap 是一段数据不完整的时间。
type TrafficGap struct {
	StackID int64  `json:"stack_id"`
	Stack   string `json:"stack"`
	// Kind 为 lost 时这段流量没有采集到；为 delayed 时流量计入了恢复采集的时刻，而不是发生的时刻。
	Kind   string    `json:"kind" enum:"lost,delayed"`
	Reason string    `json:"reason"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Open   bool      `json:"open"`
}

// TrafficStats 是流量统计结果。
type TrafficStats struct {
	View     string      `json:"view"`
	Level    string      `json:"level"`
	Timezone string      `json:"timezone"`
	From     time.Time   `json:"from"`
	To       time.Time   `json:"to"`
	Buckets  []time.Time `json:"buckets"`
	// AvailableFrom 是该粒度明细保留到的最早时间，早于它的时间桶没有数据；零值表示查询范围内都有数据。
	AvailableFrom time.Time `json:"available_from"`
	RetentionDays int       `json:"retention_days"`
	PreviousFrom  time.Time `json:"previous_from"`
	PreviousTo    time.Time `json:"previous_to"`
	PeakAvailable bool      `json:"peak_available"`
	// SeriesDetail 表示子行也带有趋势序列（只筛选了一个用户或一个 stack 时）。
	SeriesDetail bool              `json:"series_detail"`
	Total        TrafficStatsRow   `json:"total"`
	Rows         []TrafficStatsRow `json:"rows"`
	Gaps         []TrafficGap      `json:"gaps"`
}

// statsSeries 是一组按时间桶排列的上下行字节数。
type statsSeries struct{ up, down []int64 }

func newStatsSeries(n int) *statsSeries {
	return &statsSeries{up: make([]int64, n), down: make([]int64, n)}
}

func (s *statsSeries) add(index int, bytes traffic.Bytes) {
	s.up[index] += bytes.Up
	s.down[index] += bytes.Down
}

func (s *statsSeries) addSeries(other *statsSeries) {
	for index := range s.up {
		s.up[index] += other.up[index]
		s.down[index] += other.down[index]
	}
}

func (s *statsSeries) total() traffic.Bytes {
	var out traffic.Bytes
	for index := range s.up {
		out.Up += s.up[index]
		out.Down += s.down[index]
	}
	return out
}

// trafficDirectory 解析统计对象的名字：当前存在的用户和 stack 取当前名字，已删除的取最后一次保存的名字。
type trafficDirectory struct {
	users    map[int64]string
	userIDs  []int64
	stacks   map[int64]string
	stackIDs []int64
	roles    map[int64]string
	profiles map[int64]map[int64]string
	archive  map[string]map[int64]string
}

func (s *Service) trafficDirectory(ctx context.Context) (*trafficDirectory, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	var names []dbq.TrafficName
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		names, err = q.ListTrafficNames(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	dir := &trafficDirectory{
		users: map[int64]string{}, stacks: map[int64]string{}, roles: map[int64]string{}, profiles: map[int64]map[int64]string{},
		archive: map[string]map[int64]string{"user": {}, "stack": {}},
	}
	for _, name := range names {
		if dir.archive[name.Kind] != nil {
			dir.archive[name.Kind][name.ID] = name.Name
		}
		if name.Kind == "stack" {
			dir.roles[name.ID] = name.Role
		}
	}
	for _, user := range doc.Users {
		dir.users[user.ID] = user.Name
		dir.userIDs = append(dir.userIDs, user.ID)
		profiles := map[int64]string{}
		for _, profile := range user.Profiles {
			profiles[profile.ID] = profile.Name
		}
		dir.profiles[user.ID] = profiles
	}
	for _, stack := range doc.Stacks {
		dir.stacks[stack.ID] = stack.Name
		dir.stackIDs = append(dir.stackIDs, stack.ID)
		dir.roles[stack.ID] = stack.Role
	}
	return dir, nil
}

func (d *trafficDirectory) user(id int64) (string, bool) {
	if name, ok := d.users[id]; ok {
		return name, false
	}
	if name, ok := d.archive["user"][id]; ok {
		return name, true
	}
	return "#" + strconv.FormatInt(id, 10), true
}

func (d *trafficDirectory) stack(id int64) (string, bool) {
	if name, ok := d.stacks[id]; ok {
		return name, false
	}
	if name, ok := d.archive["stack"][id]; ok {
		return name, true
	}
	return "Stack #" + strconv.FormatInt(id, 10), true
}

// auto 判断 stack 是否为 auto 角色。跨 stack 加总入站流量时排除 auto stack，避免与 edge stack 的 relay 入站重复。
func (d *trafficDirectory) auto(id int64) bool { return d.roles[id] == "auto" }

func (d *trafficDirectory) profile(userID, profileID int64) string {
	if profileID == 0 {
		return "default"
	}
	if name, ok := d.profiles[userID][profileID]; ok {
		return name
	}
	return "档案 #" + strconv.FormatInt(profileID, 10)
}

func (d *trafficDirectory) userRow(id int64) TrafficStatsRow {
	if id <= 0 {
		return TrafficStatsRow{Kind: StatsRowUnknown, Name: "未识别用户", Children: []TrafficStatsRow{}, Profiles: []TrafficStatsRow{}}
	}
	name, deleted := d.user(id)
	return TrafficStatsRow{Kind: StatsRowUser, ID: id, Name: name, Deleted: deleted, Children: []TrafficStatsRow{}, Profiles: []TrafficStatsRow{}}
}

func (d *trafficDirectory) stackRow(id int64) TrafficStatsRow {
	name, deleted := d.stack(id)
	return TrafficStatsRow{Kind: StatsRowStack, ID: id, Name: name, Deleted: deleted, Role: d.roles[id], Children: []TrafficStatsRow{}, Profiles: []TrafficStatsRow{}}
}

// subjectUserID 返回用户计数的用户 ID 和档案 ID；无法归属到用户的计数返回 0。
func subjectUserID(subject string) (int64, int64) {
	head, tail, found := strings.Cut(subject, ".")
	id, err := strconv.ParseInt(head, 10, 64)
	if err != nil || id <= 0 {
		return 0, 0
	}
	if !found {
		return id, 0
	}
	profile, _ := strconv.ParseInt(tail, 10, 64)
	return id, profile
}

// isRelayInbound 判断入站计数是否来自 relay 入站（标签为 relay 或以 :relay 结尾）。
func isRelayInbound(tag string) bool {
	return tag == configdoc.RelayInbound || strings.HasSuffix(tag, ":"+configdoc.RelayInbound)
}

// floorLevel 返回 t 所在统计周期的开始时刻，按 loc 计算。
func floorLevel(t time.Time, level string, loc *time.Location) time.Time {
	local := t.In(loc)
	switch level {
	case "hour":
		return time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, loc)
	case "day":
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	case "month":
		return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	default:
		return time.Date(local.Year(), 1, 1, 0, 0, 0, 0, loc)
	}
}

// stepLevel 返回 start 之后第 n 个统计周期的开始时刻，start 必须是周期开始时刻。
func stepLevel(start time.Time, level string, n int) time.Time {
	local := start
	switch level {
	case "hour":
		return start.Add(time.Duration(n) * time.Hour)
	case "day":
		return time.Date(local.Year(), local.Month(), local.Day()+n, 0, 0, 0, 0, start.Location())
	case "month":
		return time.Date(local.Year(), local.Month()+time.Month(n), 1, 0, 0, 0, 0, start.Location())
	default:
		return time.Date(local.Year()+n, 1, 1, 0, 0, 0, 0, start.Location())
	}
}

// levelTable 返回统计粒度对应的明细级别，年由月明细聚合。
func levelTable(level string) string {
	switch level {
	case "hour":
		return store.LevelHour
	case "day":
		return store.LevelDay
	default:
		return store.LevelMonth
	}
}

// retentionDays 返回某个明细级别的保留天数，0 表示永久保留。
func retentionDays(settings configdoc.Settings, table string) int {
	switch table {
	case store.LevelMinute:
		return settings.Traffic.RetentionMinuteDays
	case store.LevelHour:
		return settings.Traffic.RetentionHourDays
	case store.LevelDay:
		return settings.Traffic.RetentionDayDays
	default:
		return settings.Traffic.RetentionMonthDays
	}
}

// availableFrom 返回明细保留到的最早完整周期开始时刻；永久保留时返回零值。
func availableFrom(settings configdoc.Settings, level string, now time.Time, loc *time.Location) (time.Time, int) {
	days := retentionDays(settings, levelTable(level))
	if days <= 0 {
		return time.Time{}, 0
	}
	cutoff := now.AddDate(0, 0, -days)
	start := floorLevel(cutoff, level, loc)
	if start.Before(cutoff) {
		start = stepLevel(start, level, 1)
	}
	return start, days
}

// previousRange 返回 [from, to) 的上一期：向前移动同样多个周期；当前周期尚未结束时只取同等的已过时长。
func previousRange(from, to, now time.Time, loc *time.Location) (time.Time, time.Time) {
	level := alignedLevel(from, to, loc)
	count := 0
	for at := from; at.Before(to); at = stepLevel(at, level, 1) {
		count++
	}
	prevFrom := stepLevel(floorLevel(from, level, loc), level, -count)
	prevTo := from
	if to.After(now) && now.After(from) {
		if cut := prevFrom.Add(now.Sub(from)); cut.Before(prevTo) {
			prevTo = cut
		}
	}
	return prevFrom, prevTo
}

// alignedLevel 返回 from 和 to 都对齐的最粗粒度。
func alignedLevel(from, to time.Time, loc *time.Location) string {
	for _, level := range []string{"month", "day", "hour"} {
		if floorLevel(from, level, loc).Equal(from) && floorLevel(to, level, loc).Equal(to) {
			return level
		}
	}
	return "hour"
}

// trafficSegment 是一段用同一级明细读取的时间范围。
type trafficSegment struct {
	table    string
	from, to time.Time
}

// trafficSegments 把 [from, to) 拆成尽量粗的明细段：能整月读取的部分用月明细，其余依次用日、小时明细；
// 不足一小时的边角在分钟明细保留期内用分钟明细，否则舍去。相邻的同级段合并。
func trafficSegments(settings configdoc.Settings, from, to, now time.Time, loc *time.Location) []trafficSegment {
	minuteDays := settings.Traffic.RetentionMinuteDays
	minuteFrom := now.AddDate(0, 0, -minuteDays)
	out := []trafficSegment{}
	add := func(table string, start, end time.Time) {
		if last := len(out) - 1; last >= 0 && out[last].table == table && out[last].to.Equal(start) {
			out[last].to = end
			return
		}
		out = append(out, trafficSegment{table, start, end})
	}
	levels := []struct{ level, table string }{{"month", store.LevelMonth}, {"day", store.LevelDay}, {"hour", store.LevelHour}}
	for cursor := from; cursor.Before(to); {
		advanced := false
		for _, level := range levels {
			next := stepLevel(cursor, level.level, 1)
			if floorLevel(cursor, level.level, loc).Equal(cursor) && !next.After(to) {
				add(level.table, cursor, next)
				cursor, advanced = next, true
				break
			}
		}
		if advanced {
			continue
		}
		end := stepLevel(floorLevel(cursor, "hour", loc), "hour", 1)
		if end.After(to) {
			end = to
		}
		if minuteDays <= 0 || !cursor.Before(minuteFrom) {
			add(store.LevelMinute, cursor, end)
		}
		cursor = end
	}
	return out
}

// sumTraffic 汇总 [from, to) 内各 stack、各对象的用量，并计入尚未落库的采集增量。
func (s *Service) sumTraffic(ctx context.Context, settings configdoc.Settings, scope string, from, to time.Time, stacks []int64) (map[traffic.Key]traffic.Bytes, error) {
	now, loc := s.Now(), location(settings)
	out := map[traffic.Key]traffic.Bytes{}
	for _, segment := range trafficSegments(settings, from, to, now, loc) {
		rows, err := s.Store.QueryTraffic(ctx, segment.table, scope, segment.from.Unix(), segment.to.Unix(), stacks)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			key := traffic.Key{StackID: row.StackID, Scope: scope, Subject: row.Subject}
			value := out[key]
			value.Add(traffic.Bytes{Up: row.Up, Down: row.Down})
			out[key] = value
		}
	}
	if !now.Before(from) && now.Before(to) {
		wanted := stackSet(stacks)
		s.traffic.mu.Lock()
		for key, bytes := range s.traffic.pending {
			if key.Scope == scope && (wanted == nil || wanted[key.StackID]) {
				value := out[key]
				value.Add(bytes)
				out[key] = value
			}
		}
		s.traffic.mu.Unlock()
	}
	return out, nil
}

func stackSet(ids []int64) map[int64]bool {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// statsRows 读取 [from, to) 内某个维度的明细，按时间桶归位，并把尚未落库的增量计入当前时间桶。
func (s *Service) statsRows(ctx context.Context, level, scope string, from, to time.Time, stacks []int64, index func(time.Time) (int, bool), each func(stackID int64, subject string, bucket int, bytes traffic.Bytes)) error {
	rows, err := s.Store.QueryTraffic(ctx, levelTable(level), scope, from.Unix(), to.Unix(), stacks)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if bucket, ok := index(time.Unix(row.Bucket, 0)); ok {
			each(row.StackID, row.Subject, bucket, traffic.Bytes{Up: row.Up, Down: row.Down})
		}
	}
	now := s.Now()
	bucket, ok := index(now)
	if !ok || now.Before(from) || !now.Before(to) {
		return nil
	}
	wanted := stackSet(stacks)
	s.traffic.mu.Lock()
	pending := make(map[traffic.Key]traffic.Bytes, len(s.traffic.pending))
	for key, bytes := range s.traffic.pending {
		if key.Scope == scope && (wanted == nil || wanted[key.StackID]) {
			pending[key] = bytes
		}
	}
	s.traffic.mu.Unlock()
	for key, bytes := range pending {
		each(key.StackID, key.Subject, bucket, bytes)
	}
	return nil
}

// TrafficStats 按用户或按 stack 统计 [From, To) 内的流量。
//
// 按用户统计时每个用户一行，展开为各 stack 和各凭据档案；按 stack 统计时每个 stack 一行，用入站计数，
// 展开为各用户以及未归属到用户的 relay 转入和共享账号流量，合计不含 auto stack。按 stack 统计且筛选了用户时
// 改用这些用户的计数，不存在重复，合计包含 auto stack。
func (s *Service) TrafficStats(ctx context.Context, query TrafficStatsQuery) (*TrafficStats, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	now, loc := s.Now(), location(settings)
	level := query.Level
	from := floorLevel(query.From, level, loc)
	to := query.To
	if !to.After(from) {
		to = stepLevel(from, level, 1)
	}
	out := &TrafficStats{View: query.View, Level: level, Timezone: loc.String(), From: from, Buckets: []time.Time{}, Rows: []TrafficStatsRow{}, Gaps: []TrafficGap{}}
	indexes := map[int64]int{}
	for at := from; at.Before(to); at = stepLevel(at, level, 1) {
		if len(out.Buckets) >= maxStatsBuckets {
			return nil, &FieldError{Field: "to", Message: "时间范围过大，请缩小范围或改用更粗的粒度"}
		}
		indexes[at.Unix()] = len(out.Buckets)
		out.Buckets = append(out.Buckets, at)
	}
	to = stepLevel(out.Buckets[len(out.Buckets)-1], level, 1)
	out.To = to
	index := func(t time.Time) (int, bool) {
		bucket, ok := indexes[floorLevel(t, level, loc).Unix()]
		return bucket, ok
	}
	if available, days := availableFrom(settings, level, now, loc); !available.IsZero() && from.Before(available) {
		out.AvailableFrom, out.RetentionDays = available, days
	}
	dir, err := s.trafficDirectory(ctx)
	if err != nil {
		return nil, err
	}
	if out.Gaps, err = s.trafficGaps(ctx, dir, from, to); err != nil {
		return nil, err
	}
	if query.Compare {
		out.PreviousFrom, out.PreviousTo = previousRange(from, to, now, loc)
	}
	minuteDays := settings.Traffic.RetentionMinuteDays
	out.PeakAvailable = minuteDays <= 0 || !from.Before(now.AddDate(0, 0, -minuteDays))
	if query.View == StatsViewStack {
		out.SeriesDetail = len(query.Stacks) == 1
		err = s.stackStats(ctx, settings, dir, query, out, index)
	} else {
		out.SeriesDetail = len(query.Users) == 1
		err = s.userStats(ctx, settings, dir, query, out, index)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) userStats(ctx context.Context, settings configdoc.Settings, dir *trafficDirectory, query TrafficStatsQuery, out *TrafficStats, index func(time.Time) (int, bool)) error {
	n := len(out.Buckets)
	wanted := map[int64]bool{}
	for _, id := range query.Users {
		wanted[id] = true
	}
	type userAgg struct {
		series   *statsSeries
		stacks   map[int64]*statsSeries
		profiles map[int64]traffic.Bytes
	}
	users := map[int64]*userAgg{}
	get := func(id int64) *userAgg {
		if users[id] == nil {
			users[id] = &userAgg{series: newStatsSeries(n), stacks: map[int64]*statsSeries{}, profiles: map[int64]traffic.Bytes{}}
		}
		return users[id]
	}
	for _, id := range dir.userIDs {
		if len(wanted) == 0 || wanted[id] {
			get(id)
		}
	}
	err := s.statsRows(ctx, out.Level, traffic.ScopeUser, out.From, out.To, query.Stacks, index, func(stackID int64, subject string, bucket int, bytes traffic.Bytes) {
		id, profile := subjectUserID(subject)
		if len(wanted) > 0 && !wanted[id] {
			return
		}
		agg := get(id)
		agg.series.add(bucket, bytes)
		if agg.stacks[stackID] == nil {
			agg.stacks[stackID] = newStatsSeries(n)
		}
		agg.stacks[stackID].add(bucket, bytes)
		if id > 0 {
			value := agg.profiles[profile]
			value.Add(bytes)
			agg.profiles[profile] = value
		}
	})
	if err != nil {
		return err
	}
	var previous map[int64]traffic.Bytes
	if query.Compare {
		sums, err := s.sumTraffic(ctx, settings, traffic.ScopeUser, out.PreviousFrom, out.PreviousTo, query.Stacks)
		if err != nil {
			return err
		}
		previous = map[int64]traffic.Bytes{}
		for key, bytes := range sums {
			id, _ := subjectUserID(key.Subject)
			if len(wanted) == 0 || wanted[id] {
				value := previous[id]
				value.Add(bytes)
				previous[id] = value
			}
		}
	}
	var peaks, totalPeak map[int64]int64
	if out.PeakAvailable {
		from, to := out.From.Unix(), out.To.Unix()
		if peaks, err = s.Store.MinutePeaks(ctx, traffic.ScopeUser, store.PeakByUser, from, to, query.Stacks, query.Users); err != nil {
			return err
		}
		if totalPeak, err = s.Store.MinutePeaks(ctx, traffic.ScopeUser, store.PeakTotal, from, to, query.Stacks, query.Users); err != nil {
			return err
		}
	}
	total := newStatsSeries(n)
	for id, agg := range users {
		row := dir.userRow(id)
		if id <= 0 && agg.series.total().Total() == 0 {
			continue
		}
		row.Bytes = agg.series.total()
		row.SeriesUp, row.SeriesDown = agg.series.up, agg.series.down
		total.addSeries(agg.series)
		if previous != nil {
			value := previous[id]
			row.Previous = &value
		}
		if peaks != nil {
			value := peakRate(peaks[max(id, 0)])
			row.Peak = &value
		}
		for stackID, series := range agg.stacks {
			child := dir.stackRow(stackID)
			child.Bytes = series.total()
			if out.SeriesDetail {
				child.SeriesUp, child.SeriesDown = series.up, series.down
			}
			row.Children = append(row.Children, child)
		}
		sortStatsRows(row.Children)
		if len(agg.profiles) > 1 || agg.profiles[0].Total() == 0 && len(agg.profiles) == 1 {
			for profile, bytes := range agg.profiles {
				row.Profiles = append(row.Profiles, TrafficStatsRow{Kind: StatsRowProfile, ID: profile, Name: dir.profile(id, profile), Bytes: bytes, Children: []TrafficStatsRow{}, Profiles: []TrafficStatsRow{}})
			}
			sortStatsRows(row.Profiles)
		}
		out.Rows = append(out.Rows, row)
	}
	sortStatsRows(out.Rows)
	out.Total = statsTotal(total, out.Rows, totalPeak)
	return nil
}

func (s *Service) stackStats(ctx context.Context, settings configdoc.Settings, dir *trafficDirectory, query TrafficStatsQuery, out *TrafficStats, index func(time.Time) (int, bool)) error {
	n := len(out.Buckets)
	byUsers := len(query.Users) > 0
	wanted := map[int64]bool{}
	for _, id := range query.Users {
		wanted[id] = true
	}
	stackFilter := stackSet(query.Stacks)
	type stackAgg struct {
		series *statsSeries
		relay  *statsSeries
		users  map[int64]*statsSeries
	}
	stacks := map[int64]*stackAgg{}
	get := func(id int64) *stackAgg {
		if stacks[id] == nil {
			stacks[id] = &stackAgg{series: newStatsSeries(n), relay: newStatsSeries(n), users: map[int64]*statsSeries{}}
		}
		return stacks[id]
	}
	for _, id := range dir.stackIDs {
		if stackFilter == nil || stackFilter[id] {
			get(id)
		}
	}
	err := s.statsRows(ctx, out.Level, traffic.ScopeUser, out.From, out.To, query.Stacks, index, func(stackID int64, subject string, bucket int, bytes traffic.Bytes) {
		id, _ := subjectUserID(subject)
		if byUsers && !wanted[id] {
			return
		}
		agg := get(stackID)
		if agg.users[id] == nil {
			agg.users[id] = newStatsSeries(n)
		}
		agg.users[id].add(bucket, bytes)
		if byUsers {
			agg.series.add(bucket, bytes)
		}
	})
	if err != nil {
		return err
	}
	if !byUsers {
		err = s.statsRows(ctx, out.Level, traffic.ScopeInbound, out.From, out.To, query.Stacks, index, func(stackID int64, subject string, bucket int, bytes traffic.Bytes) {
			agg := get(stackID)
			agg.series.add(bucket, bytes)
			if isRelayInbound(subject) {
				agg.relay.add(bucket, bytes)
			}
		})
		if err != nil {
			return err
		}
	}
	scope := traffic.ScopeInbound
	if byUsers {
		scope = traffic.ScopeUser
	}
	var previous map[int64]traffic.Bytes
	if query.Compare {
		sums, err := s.sumTraffic(ctx, settings, scope, out.PreviousFrom, out.PreviousTo, query.Stacks)
		if err != nil {
			return err
		}
		previous = map[int64]traffic.Bytes{}
		for key, bytes := range sums {
			if id, _ := subjectUserID(key.Subject); byUsers && !wanted[id] {
				continue
			}
			value := previous[key.StackID]
			value.Add(bytes)
			previous[key.StackID] = value
		}
	}
	var peaks, totalPeak map[int64]int64
	if out.PeakAvailable {
		from, to := out.From.Unix(), out.To.Unix()
		if peaks, err = s.Store.MinutePeaks(ctx, scope, store.PeakByStack, from, to, query.Stacks, query.Users); err != nil {
			return err
		}
		counted := []int64{}
		for id := range stacks {
			if byUsers || !dir.auto(id) {
				counted = append(counted, id)
			}
		}
		if len(counted) > 0 {
			if totalPeak, err = s.Store.MinutePeaks(ctx, scope, store.PeakTotal, from, to, counted, query.Users); err != nil {
				return err
			}
		}
	}
	total := newStatsSeries(n)
	for id, agg := range stacks {
		row := dir.stackRow(id)
		if row.Deleted && agg.series.total().Total() == 0 && len(agg.users) == 0 {
			continue
		}
		row.Excluded = !byUsers && dir.auto(id)
		row.Bytes = agg.series.total()
		row.SeriesUp, row.SeriesDown = agg.series.up, agg.series.down
		if !row.Excluded {
			total.addSeries(agg.series)
		}
		if previous != nil {
			value := previous[id]
			row.Previous = &value
		}
		if peaks != nil {
			value := peakRate(peaks[id])
			row.Peak = &value
		}
		attributed := newStatsSeries(n)
		for userID, series := range agg.users {
			attributed.addSeries(series)
			child := dir.userRow(userID)
			child.Bytes = series.total()
			if out.SeriesDetail {
				child.SeriesUp, child.SeriesDown = series.up, series.down
			}
			row.Children = append(row.Children, child)
		}
		sortStatsRows(row.Children)
		if !byUsers {
			relay, shared := splitUnattributed(agg.series, attributed, agg.relay, dir.auto(id))
			for _, part := range []struct {
				kind, name string
				series     *statsSeries
			}{{StatsRowRelay, "经 relay 入站（含 auto 转入）", relay}, {StatsRowShared, "共享账号等", shared}} {
				if part.series.total().Total() == 0 {
					continue
				}
				child := TrafficStatsRow{Kind: part.kind, Name: part.name, Bytes: part.series.total(), Children: []TrafficStatsRow{}, Profiles: []TrafficStatsRow{}}
				if out.SeriesDetail {
					child.SeriesUp, child.SeriesDown = part.series.up, part.series.down
				}
				row.Children = append(row.Children, child)
			}
		}
		out.Rows = append(out.Rows, row)
	}
	sortStatsRows(out.Rows)
	out.Total = statsTotal(total, out.Rows, totalPeak)
	return nil
}

// splitUnattributed 按时间桶计算入站总量中未归属到用户的部分，再拆成 relay 入站与其余部分。
// auto stack 的 relay 入站是面向客户端的共享账号入站，全部计入其余部分。两套计数器有少量误差，负值按 0 计。
func splitUnattributed(inbound, users, relayIn *statsSeries, auto bool) (*statsSeries, *statsSeries) {
	n := len(inbound.up)
	relay, shared := newStatsSeries(n), newStatsSeries(n)
	part := func(all, attributed, relayed int64) (int64, int64) {
		rest := max(all-attributed, 0)
		if auto {
			return 0, rest
		}
		fromRelay := min(rest, relayed)
		return fromRelay, rest - fromRelay
	}
	for index := 0; index < n; index++ {
		relay.up[index], shared.up[index] = part(inbound.up[index], users.up[index], relayIn.up[index])
		relay.down[index], shared.down[index] = part(inbound.down[index], users.down[index], relayIn.down[index])
	}
	return relay, shared
}

func statsTotal(total *statsSeries, rows []TrafficStatsRow, peak map[int64]int64) TrafficStatsRow {
	row := TrafficStatsRow{Kind: StatsRowTotal, Name: "合计", Bytes: total.total(), SeriesUp: total.up, SeriesDown: total.down, Children: []TrafficStatsRow{}, Profiles: []TrafficStatsRow{}}
	var previous *traffic.Bytes
	for _, item := range rows {
		if item.Previous == nil || item.Excluded {
			continue
		}
		if previous == nil {
			previous = &traffic.Bytes{}
		}
		previous.Add(*item.Previous)
	}
	row.Previous = previous
	if peak != nil {
		value := peakRate(peak[0])
		row.Peak = &value
	}
	return row
}

// peakRate 把单分钟字节数换算为每秒速率。
func peakRate(bytes int64) int64 { return bytes / 60 }

// sortStatsRows 按用量从高到低排序，同用量按名字和 ID。
func sortStatsRows(rows []TrafficStatsRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Total() != b.Total() {
			return a.Total() > b.Total()
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
}

// trafficGaps 返回与 [from, to) 重叠的数据缺口。
func (s *Service) trafficGaps(ctx context.Context, dir *trafficDirectory, from, to time.Time) ([]TrafficGap, error) {
	var rows []dbq.TrafficGap
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		rows, err = q.ListTrafficGaps(ctx, dbq.ListTrafficGapsParams{EndAt: from.Unix(), StartAt: to.Unix()})
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]TrafficGap, 0, len(rows))
	for _, row := range rows {
		gap := TrafficGap{StackID: row.StackID, Stack: "整个面板", Kind: row.Kind, Reason: row.Reason, From: time.Unix(row.StartAt, 0), To: time.Unix(row.EndAt, 0), Open: row.Open != 0}
		if row.StackID != 0 {
			gap.Stack, _ = dir.stack(row.StackID)
		}
		out = append(out, gap)
	}
	return out, nil
}

// TrafficRankingQuery 是用量排行的查询条件。Stack 为 0 时统计全部 stack。
type TrafficRankingQuery struct {
	From    time.Time
	To      time.Time
	Stack   int64
	Compare bool
}

// TrafficRankRow 是排行中的一个用户。
type TrafficRankRow struct {
	UserID  int64  `json:"user_id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
	traffic.Bytes
	Previous *traffic.Bytes `json:"previous,omitempty"`
}

// TrafficRanking 是用量排行。Rows 含全部现有用户（零用量也列出）和范围内有用量的已删除用户，按合计从高到低排列；
// 按上行或下行排序和名次由前端计算。
type TrafficRanking struct {
	Timezone     string           `json:"timezone"`
	From         time.Time        `json:"from"`
	To           time.Time        `json:"to"`
	PreviousFrom time.Time        `json:"previous_from"`
	PreviousTo   time.Time        `json:"previous_to"`
	Total        traffic.Bytes    `json:"total"`
	Previous     *traffic.Bytes   `json:"previous,omitempty"`
	Rows         []TrafficRankRow `json:"rows"`
	Gaps         []TrafficGap     `json:"gaps"`
}

// TrafficRanking 统计 [From, To) 内各用户的用量，合并凭据档案。
func (s *Service) TrafficRanking(ctx context.Context, query TrafficRankingQuery) (*TrafficRanking, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !query.To.After(query.From) {
		return nil, &FieldError{Field: "to", Message: "结束时间必须晚于开始时间"}
	}
	dir, err := s.trafficDirectory(ctx)
	if err != nil {
		return nil, err
	}
	now, loc := s.Now(), location(settings)
	out := &TrafficRanking{Timezone: loc.String(), From: query.From, To: query.To, Rows: []TrafficRankRow{}}
	var stacks []int64
	if query.Stack != 0 {
		stacks = []int64{query.Stack}
	}
	current, err := s.userTotals(ctx, settings, query.From, query.To, stacks)
	if err != nil {
		return nil, err
	}
	var previous map[int64]traffic.Bytes
	if query.Compare {
		out.PreviousFrom, out.PreviousTo = previousRange(query.From, query.To, now, loc)
		if previous, err = s.userTotals(ctx, settings, out.PreviousFrom, out.PreviousTo, stacks); err != nil {
			return nil, err
		}
		out.Previous = &traffic.Bytes{}
	}
	for _, id := range dir.userIDs {
		if _, ok := current[id]; !ok {
			current[id] = traffic.Bytes{}
		}
	}
	for id, bytes := range current {
		name, deleted := dir.user(id)
		row := TrafficRankRow{UserID: id, Name: name, Deleted: deleted, Bytes: bytes}
		if previous != nil {
			value := previous[id]
			row.Previous = &value
			out.Previous.Add(value)
		}
		out.Total.Add(bytes)
		out.Rows = append(out.Rows, row)
	}
	sortRankRows(out.Rows)
	if out.Gaps, err = s.trafficGaps(ctx, dir, query.From, query.To); err != nil {
		return nil, err
	}
	return out, nil
}

// userTotals 汇总 [from, to) 内每个用户的用量，合并档案；无法归属到用户的计数不计入。
func (s *Service) userTotals(ctx context.Context, settings configdoc.Settings, from, to time.Time, stacks []int64) (map[int64]traffic.Bytes, error) {
	sums, err := s.sumTraffic(ctx, settings, traffic.ScopeUser, from, to, stacks)
	if err != nil {
		return nil, err
	}
	out := map[int64]traffic.Bytes{}
	for key, bytes := range sums {
		if id, _ := subjectUserID(key.Subject); id > 0 {
			value := out[id]
			value.Add(bytes)
			out[id] = value
		}
	}
	return out, nil
}

func sortRankRows(rows []TrafficRankRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Total() != b.Total() {
			return a.Total() > b.Total()
		}
		return a.UserID < b.UserID
	})
}

// TrafficStatsCSV 把统计结果转成 CSV：每个时间桶、每一行一条记录，时间按统计时区显示，最后附合计。
func (s *Service) TrafficStatsCSV(result *TrafficStats) []byte {
	loc, err := time.LoadLocation(result.Timezone)
	if err != nil {
		loc = time.UTC
	}
	object := "用户"
	if result.View == StatsViewStack {
		object = "Stack"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\xef\xbb\xbf时间,%s,上行字节,下行字节,合计字节\r\n", object)
	write := func(bucket time.Time, row TrafficStatsRow, index int) {
		up, down := row.SeriesUp[index], row.SeriesDown[index]
		if up == 0 && down == 0 {
			return
		}
		name := row.Name
		if row.Deleted {
			name += "（已删除）"
		}
		if row.Excluded {
			name += "（不计入合计）"
		}
		fmt.Fprintf(&out, "%s,%s,%d,%d,%d\r\n", bucket.In(loc).Format(time.DateTime), csvField(name), up, down, up+down)
	}
	for index, bucket := range result.Buckets {
		for _, row := range result.Rows {
			write(bucket, row, index)
		}
		write(bucket, result.Total, index)
	}
	return []byte(out.String())
}
