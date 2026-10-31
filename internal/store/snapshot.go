package store

import (
	"context"
	"time"
)

// SnapshotStats 是数据库内容的概要，写入迁移包的 manifest，恢复前用于预览。
type SnapshotStats struct {
	Counts      map[string]int64
	TrafficFrom time.Time
	TrafficTo   time.Time
}

// Stats 统计用户、stack、手工节点、修订版本和流量明细的条数，以及流量明细的时间范围。
func (s *Store) Stats(ctx context.Context) (*SnapshotStats, error) {
	stats := &SnapshotStats{Counts: map[string]int64{}}
	var traffic int64
	for name, table := range map[string]string{
		"users": "users", "stacks": "stacks", "manual_nodes": "manual_nodes", "revisions": "config_revisions",
		"traffic_minute": "traffic_minute", "traffic_hour": "traffic_hour", "traffic_day": "traffic_day", "traffic_month": "traffic_month",
	} {
		var count int64
		if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			return nil, err
		}
		if len(name) > 8 && name[:8] == "traffic_" {
			traffic += count
		} else {
			stats.Counts[name] = count
		}
	}
	stats.Counts["traffic"] = traffic
	var from, to int64
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MIN(bucket_start), 0), COALESCE(MAX(bucket_start), 0) FROM (
		SELECT bucket_start FROM traffic_month UNION ALL SELECT bucket_start FROM traffic_day
		UNION ALL SELECT bucket_start FROM traffic_hour UNION ALL SELECT bucket_start FROM traffic_minute)`).Scan(&from, &to); err != nil {
		return nil, err
	}
	if from != 0 {
		stats.TrafficFrom, stats.TrafficTo = time.Unix(from, 0), time.Unix(to, 0)
	}
	return stats, nil
}
