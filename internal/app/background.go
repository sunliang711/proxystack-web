package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/eagle/proxystack-web/internal/store/dbq"
)

// 后台循环的间隔。
const (
	unitMonitorInterval = 3 * time.Second
	reconcileInterval   = 5 * time.Minute
)

// StartBackground 在服务启动时执行一次对账（纠正 unit 状态），并启动 unit 状态推送和定期对账报告。
func (s *Service) StartBackground(ctx context.Context) error {
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return q.MarkInterruptedTasks(ctx, s.Now().Unix())
	}); err != nil {
		return err
	}
	findings, err := s.Reconcile(ctx, true)
	if err != nil {
		slog.Error("startup reconcile", "err", err)
	}
	for _, finding := range findings {
		slog.Info("startup reconcile", "finding", finding)
	}
	go s.MonitorUnits(ctx, unitMonitorInterval)
	go func() {
		ticker := time.NewTicker(reconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 运行期间只报告，不反复拉起崩溃的 unit（由 systemd 的 Restart=on-failure 负责）。
				findings, err := s.Reconcile(ctx, false)
				if err != nil {
					slog.Warn("reconcile", "err", err)
				}
				for _, finding := range findings {
					slog.Warn("reconcile", "finding", finding)
				}
			}
		}
	}()
	return nil
}
