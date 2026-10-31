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
)

// WaitBackground 等待需要在退出前收尾的后台循环结束（ctx 取消后调用，并且要早于关闭数据库）。
func (s *Service) WaitBackground() { s.background.Wait() }

// StartBackground 在服务启动时执行一次对账（纠正 unit 状态），并启动 unit 状态推送和定期对账报告。
func (s *Service) StartBackground(ctx context.Context) error {
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return q.MarkInterruptedTasks(ctx, s.Now().Unix())
	}); err != nil {
		return err
	}
	s.resumeAfterRestore(ctx)
	findings, err := s.Reconcile(ctx, true)
	if err != nil {
		slog.Error("startup reconcile", "err", err)
	}
	for _, finding := range findings {
		slog.Info("startup reconcile", "finding", finding)
	}
	go s.MonitorUnits(ctx, unitMonitorInterval)
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		s.runTraffic(ctx)
	}()
	go s.runNotifications(ctx)
	go s.runScheduler(ctx)
	return nil
}
