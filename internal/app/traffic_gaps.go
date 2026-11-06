package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store/dbq"
)

// 一次采集中单个 stack 的结果。
type gapResult int

const (
	gapCollected gapResult = iota
	// gapFailed 表示 xray 在运行但读取计数失败：计数仍在 xray 中累积，恢复后计入恢复时刻。
	gapFailed
	// gapStopped 表示 xray 未运行，没有流量。
	gapStopped
	// gapUncounted 表示 stack 未开启统计接口，流量不会被统计。
	gapUncounted
)

// 缺口类型。
const (
	GapLost    = "lost"
	GapDelayed = "delayed"
)

// minTrafficGap 是记录丢失缺口的最短时长。计划内的重启前会先补采一次，补采与重启之间的间隔不计。
const minTrafficGap = 5 * time.Second

type gapCheck struct {
	stackID int64
	result  gapResult
	// since 是 xray 进入当前运行或停止状态的时间。
	since  time.Time
	reason string
}

// recordTrafficGaps 根据本轮采集结果记录数据缺口：
//   - xray 在两次成功采集之间重启或停止过，其间未采集的计数随进程丢失；
//   - 读取计数失败期间的流量在恢复后计入恢复时刻；
//   - 面板停机期间的流量在恢复后计入恢复时刻；
//   - 未开启统计接口的 stack 不统计流量。
//
// 调用方持有 collectMu。
func (s *Service) recordTrafficGaps(ctx context.Context, settings configdoc.Settings, now time.Time, checks []gapCheck) {
	if s.traffic.lastCollected == nil {
		s.traffic.lastCollected = map[int64]time.Time{}
	}
	interval := time.Duration(settings.Traffic.Interval) * time.Second
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if !s.traffic.gapsReady {
			if err := s.checkDowntime(ctx, q, now, interval, checks); err != nil {
				return err
			}
		}
		for _, check := range checks {
			last := s.traffic.lastCollected[check.stackID]
			open, err := q.GetOpenTrafficGap(ctx, check.stackID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			hasOpen := err == nil
			lost := !last.IsZero() && !check.since.IsZero() && check.since.After(last.Add(minTrafficGap)) && !check.since.After(now)
			switch check.result {
			case gapCollected, gapStopped:
				if hasOpen {
					if err := q.CloseTrafficGap(ctx, dbq.CloseTrafficGapParams{EndAt: now.Unix(), ID: open.ID}); err != nil {
						return err
					}
				}
				if lost {
					reason := "xray 在两次采集之间重启，重启前未采集的流量已丢失"
					if check.result == gapStopped {
						reason = "xray 停止前未采集的流量已丢失"
					}
					if err := q.InsertTrafficGap(ctx, dbq.InsertTrafficGapParams{StackID: check.stackID, Kind: GapLost, Reason: reason, StartAt: last.Unix(), EndAt: check.since.Unix()}); err != nil {
						return err
					}
				}
				if check.result == gapCollected {
					s.traffic.lastCollected[check.stackID] = now
				} else {
					delete(s.traffic.lastCollected, check.stackID)
				}
			case gapFailed, gapUncounted:
				kind, reason := GapDelayed, "读取计数失败："+check.reason
				if check.result == gapUncounted {
					kind, reason = GapLost, check.reason
				}
				if hasOpen {
					if err := q.ExtendTrafficGap(ctx, dbq.ExtendTrafficGapParams{EndAt: now.Unix(), Reason: reason, ID: open.ID}); err != nil {
						return err
					}
					continue
				}
				start := now
				if !last.IsZero() {
					start = last
				}
				if err := q.InsertTrafficGap(ctx, dbq.InsertTrafficGapParams{StackID: check.stackID, Kind: kind, Reason: reason, StartAt: start.Unix(), EndAt: now.Unix(), Open: 1}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("traffic gaps", "err", err)
	}
}

// checkDowntime 在进程启动后的首轮采集时执行：面板停机超过几个采集间隔时记录缺口，以停机前最后一次写入为各 stack
// 的上次采集时间，并结束已不再采集的 stack 遗留的未结束缺口。
func (s *Service) checkDowntime(ctx context.Context, q *dbq.Queries, now time.Time, interval time.Duration, checks []gapCheck) error {
	at, err := q.GetTrafficHeartbeat(ctx)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		last := time.Unix(at, 0)
		if now.Sub(last) > 3*interval+time.Minute {
			if err := q.InsertTrafficGap(ctx, dbq.InsertTrafficGapParams{
				Kind: GapDelayed, Reason: "面板未运行，期间的流量计入恢复采集后的第一分钟", StartAt: last.Unix(), EndAt: now.Unix(),
			}); err != nil {
				return err
			}
		}
		for _, check := range checks {
			s.traffic.lastCollected[check.stackID] = last
		}
	}
	ids := []int64{-1}
	for _, check := range checks {
		ids = append(ids, check.stackID)
	}
	if err := q.CloseOpenTrafficGapsExcept(ctx, ids); err != nil {
		return err
	}
	s.traffic.gapsReady = true
	return nil
}
