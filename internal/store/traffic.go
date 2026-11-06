package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// 流量明细级别。
const (
	LevelMinute = "minute"
	LevelHour   = "hour"
	LevelDay    = "day"
	LevelMonth  = "month"
)

var trafficTables = map[string]string{
	LevelMinute: "traffic_minute", LevelHour: "traffic_hour", LevelDay: "traffic_day", LevelMonth: "traffic_month",
}

// rollup 用下一级明细重算上一级的一个桶，可重复执行。
func (s *Store) rollup(ctx context.Context, lower, upper string, start, end time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	from, to := trafficTables[lower], trafficTables[upper]
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+to+" WHERE bucket_start = ?", start.Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+to+` (bucket_start, stack_id, scope, subject, up, down)
		SELECT ?, stack_id, scope, subject, SUM(up), SUM(down) FROM `+from+`
		WHERE bucket_start >= ? AND bucket_start < ? GROUP BY stack_id, scope, subject`,
		start.Unix(), start.Unix(), end.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// rollupGrace 是进入新周期后继续重算上一周期的时长，保证上一周期最后写入的明细被汇总。
const rollupGrace = 10 * time.Minute

// RollupTraffic 重算当前的小时、日、月桶；刚跨入新周期时一并重算上一个桶。桶边界按 loc 计算。
func (s *Store) RollupTraffic(ctx context.Context, now time.Time, loc *time.Location) error {
	local := now.In(loc)
	levels := []struct {
		lower, upper string
		start        time.Time
		step         func(time.Time, int) time.Time
	}{
		{LevelMinute, LevelHour, time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, loc),
			func(t time.Time, n int) time.Time { return t.Add(time.Duration(n) * time.Hour) }},
		{LevelHour, LevelDay, time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc),
			func(t time.Time, n int) time.Time { return t.AddDate(0, 0, n) }},
		{LevelDay, LevelMonth, time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc),
			func(t time.Time, n int) time.Time { return t.AddDate(0, n, 0) }},
	}
	for _, level := range levels {
		if now.Sub(level.start) < rollupGrace {
			previous := level.step(level.start, -1)
			if err := s.rollup(ctx, level.lower, level.upper, previous, level.start); err != nil {
				return fmt.Errorf("rollup %s: %w", level.upper, err)
			}
		}
		if err := s.rollup(ctx, level.lower, level.upper, level.start, level.step(level.start, 1)); err != nil {
			return fmt.Errorf("rollup %s: %w", level.upper, err)
		}
	}
	return nil
}

// PruneTraffic 删除超过保留期的明细。days 按级别给出保留天数，0 表示永久保留。
func (s *Store) PruneTraffic(ctx context.Context, now time.Time, days map[string]int) error {
	for level, keep := range days {
		table, ok := trafficTables[level]
		if !ok || keep <= 0 {
			continue
		}
		cutoff := now.AddDate(0, 0, -keep).Unix()
		if _, err := s.DB.ExecContext(ctx, "DELETE FROM "+table+" WHERE bucket_start < ?", cutoff); err != nil {
			return fmt.Errorf("prune %s: %w", table, err)
		}
	}
	return nil
}

// TrafficRow 是一个桶内某个 stack 上某个对象的流量。
type TrafficRow struct {
	Bucket  int64
	StackID int64
	Subject string
	Up      int64
	Down    int64
}

// QueryTraffic 查询 [from, to) 内的明细，按时间排序。stacks 为空时不按 stack 过滤。
func (s *Store) QueryTraffic(ctx context.Context, level, scope string, from, to int64, stacks []int64) ([]TrafficRow, error) {
	table, ok := trafficTables[level]
	if !ok {
		return nil, fmt.Errorf("unknown traffic level %q", level)
	}
	query := "SELECT bucket_start, stack_id, subject, up, down FROM " + table + " WHERE scope = ? AND bucket_start >= ? AND bucket_start < ?"
	args := []any{scope, from, to}
	if len(stacks) > 0 {
		query += " AND stack_id IN (" + placeholders(len(stacks)) + ")"
		for _, id := range stacks {
			args = append(args, id)
		}
	}
	rows, err := s.DB.QueryContext(ctx, query+" ORDER BY bucket_start, subject", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrafficRow{}
	for rows.Next() {
		var row TrafficRow
		if err := rows.Scan(&row.Bucket, &row.StackID, &row.Subject, &row.Up, &row.Down); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// 峰值的分组方式。
const (
	PeakByUser  = "user"
	PeakByStack = "stack"
	PeakTotal   = "total"
)

// MinutePeaks 返回 [from, to) 内单分钟流量（上下行合计）的最大值。group 为 PeakByUser 时按用户分组
// （档案并入所属用户，非用户对象归入 0），PeakByStack 时按 stack 分组，PeakTotal 时全部相加后取最大值（键为 0）。
// stacks、users 为空时不过滤；users 只对用户维度生效。
func (s *Store) MinutePeaks(ctx context.Context, scope, group string, from, to int64, stacks, users []int64) (map[int64]int64, error) {
	key := "0"
	switch group {
	case PeakByUser:
		key = "CAST(subject AS INTEGER)"
	case PeakByStack:
		key = "stack_id"
	}
	inner := "SELECT bucket_start, " + key + " AS k, SUM(up + down) AS t FROM traffic_minute WHERE scope = ? AND bucket_start >= ? AND bucket_start < ?"
	args := []any{scope, from, to}
	if len(stacks) > 0 {
		inner += " AND stack_id IN (" + placeholders(len(stacks)) + ")"
		for _, id := range stacks {
			args = append(args, id)
		}
	}
	if len(users) > 0 {
		inner += " AND CAST(subject AS INTEGER) IN (" + placeholders(len(users)) + ")"
		for _, id := range users {
			args = append(args, id)
		}
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT k, MAX(t) FROM ("+inner+" GROUP BY bucket_start, k) GROUP BY k", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var k, peak int64
		if err := rows.Scan(&k, &peak); err != nil {
			return nil, err
		}
		out[k] = peak
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
