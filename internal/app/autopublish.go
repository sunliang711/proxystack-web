package app

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/subscription"
)

// 自动发布的节奏：保存后稍等片刻合并连续的改动；后台每分钟兜底检查一次；
// 同一份内容发往同一组订阅端未能全部送达时，重试间隔从 10 分钟起逐次翻倍，最长 6 小时，避免反复产生失败任务。
const (
	autoPublishDebounce      = 3 * time.Second
	autoPublishInterval      = time.Minute
	autoPublishRetryDelay    = 10 * time.Minute
	autoPublishMaxRetryDelay = 6 * time.Hour
)

// AutoPublishActor 是自动发布在任务和审计日志中的操作者。
var AutoPublishActor = Actor{Name: "自动发布"}

type autoPublishState struct {
	once sync.Once
	kick chan struct{}
	mu   sync.Mutex
	// lastKey 是最近一次自动发布的内容和目标组合，lastAt 是发起时间，attempts 是同一组合连续发起的次数。
	lastKey  string
	lastAt   time.Time
	attempts int
	// blocked 是最近一次记入日志的暂停原因，原因不变时不重复记录。
	blocked string
}

// retryDelay 返回同一组合已连续发起 attempts 次后，下一次发起前的等待时间。
func retryDelay(attempts int) time.Duration {
	delay := autoPublishRetryDelay
	for i := 1; i < attempts && delay < autoPublishMaxRetryDelay; i++ {
		delay *= 2
	}
	return min(delay, autoPublishMaxRetryDelay)
}

func (a *autoPublishState) channel() chan struct{} {
	a.once.Do(func() { a.kick = make(chan struct{}, 1) })
	return a.kick
}

// kickAutoPublish 通知后台循环尽快检查是否需要自动发布，不阻塞调用方。
func (s *Service) kickAutoPublish() {
	select {
	case s.autoPublish.channel() <- struct{}{}:
	default:
	}
}

// runAutoPublish 是自动发布的后台循环：收到通知后合并一小段时间内的改动再检查，并定期兜底检查。
func (s *Service) runAutoPublish(ctx context.Context) {
	ticker := time.NewTicker(autoPublishInterval)
	defer ticker.Stop()
	kick := s.autoPublish.channel()
	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(autoPublishDebounce):
			}
		case <-ticker.C:
		}
		if _, err := s.AutoPublish(ctx); err != nil {
			slog.Warn("auto publish subscription", "err", err)
		}
	}
}

// autoPublishTargetCount 返回自动发布的目标数，供状态展示；读取失败时按 0 处理并记日志，不影响状态接口。
func (s *Service) autoPublishTargetCount(ctx context.Context) int {
	ids, err := s.AutoPublishTargets(ctx)
	if err != nil {
		slog.Warn("auto publish targets", "err", err)
		return 0
	}
	return len(ids)
}

// AutoPublishTargets 返回自动发布的目标：启用、属于当前来源、已部署并在部署后测试通过的订阅端。
// 关闭自动发布或没有 SSH 密钥时返回空。
func (s *Service) AutoPublishTargets(ctx context.Context) ([]int64, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if doc.Settings.Subscription.ManualPublish {
		return nil, nil
	}
	ids := []int64{}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		identity, err := s.loadSSHIdentity(ctx, q)
		if err != nil || len(identity.Seed) == 0 {
			return err
		}
		targets, err := loadPublishTargets(ctx, q)
		if err != nil {
			return err
		}
		for _, target := range targets {
			if target.Enabled && target.Source == doc.Settings.Subscription.SourceName && targetReadyForPublish(target) {
				ids = append(ids, target.ID)
			}
		}
		return nil
	})
	return ids, err
}

// targetReadyForPublish 判断订阅端是否已部署且在最近一次部署之后测试通过，与发布页面的「可签发」一致。
func targetReadyForPublish(target publishTargetState) bool {
	return target.DeployState == "installed" && target.LastTestAt > 0 && target.LastTestError == "" &&
		target.LastTestAt >= target.LastDeployAt
}

// autoPublishBlocked 说明当前订阅数据为什么不能自动发布：有 stack 的已应用版本在构建时被整体跳过，
// 发出去会让所有用户缺少这些节点，需要人工确认后在「发布订阅包」页面手动签发。可以自动发布时返回空。
func (snapshot *subSnapshot) autoPublishBlocked() string {
	if len(snapshot.skipped) == 0 {
		return ""
	}
	revisions := make([]int64, 0, len(snapshot.skipped))
	for revision := range snapshot.skipped {
		revisions = append(revisions, revision)
	}
	slices.Sort(revisions)
	names := []string{}
	for _, revision := range revisions {
		for stack, applied := range snapshot.applied {
			if applied == revision {
				names = append(names, stack)
			}
		}
	}
	slices.Sort(names)
	return "stack " + strings.Join(names, "、") + " 的已应用版本在构建订阅时被跳过，订阅包会缺少这些节点"
}

// AutoPublish 在订阅内容变化或有就绪订阅端落后于最近一次签发时，签发订阅包并在后台发布到全部就绪订阅端。
// 没有需要发布的内容、没有就绪订阅端、构建时有版本被跳过或处于重试间隔内时返回 0。
func (s *Service) AutoPublish(ctx context.Context) (int64, error) {
	targetIDs, err := s.AutoPublishTargets(ctx)
	if err != nil || len(targetIDs) == 0 {
		return 0, err
	}
	snapshot, err := s.subscription(ctx)
	if err != nil {
		return 0, err
	}
	reason := snapshot.autoPublishBlocked()
	s.autoPublish.mu.Lock()
	if reason != s.autoPublish.blocked {
		s.autoPublish.blocked = reason
		if reason != "" {
			slog.Info("auto publish paused", "reason", reason)
		} else {
			slog.Info("auto publish resumed")
		}
	}
	s.autoPublish.mu.Unlock()
	if reason != "" {
		return 0, nil
	}
	hash, err := subscription.ContentHash(bundleSnapshot(snapshot.data))
	if err != nil {
		return 0, err
	}
	var state publishState
	behind := false
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		if state, err = s.publishState(ctx, q); err != nil {
			return err
		}
		targets, err := loadPublishTargets(ctx, q)
		if err != nil {
			return err
		}
		for _, target := range targets {
			if slices.Contains(targetIDs, target.ID) && target.LastSequence < state.Sequence {
				behind = true
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if state.ContentHash == hash && !behind {
		return 0, nil
	}
	// 每次都发往全部就绪订阅端：签发会递增序号，只发给落后的订阅端会让其余订阅端随之落后。
	parts := make([]string, 0, len(targetIDs)+1)
	parts = append(parts, hash)
	for _, id := range targetIDs {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	key := strings.Join(parts, "|")
	s.autoPublish.mu.Lock()
	defer s.autoPublish.mu.Unlock()
	attempts := 0
	if key == s.autoPublish.lastKey {
		attempts = s.autoPublish.attempts
		if s.Now().Sub(s.autoPublish.lastAt) < retryDelay(attempts) {
			return 0, nil
		}
	}
	started, err := s.PublishSubscriptionBundleOnline(ctx, AutoPublishActor, targetIDs)
	if err != nil {
		// 订阅端正在部署或发布时下一轮再试，不计入重试间隔。
		return 0, err
	}
	s.autoPublish.lastKey, s.autoPublish.lastAt, s.autoPublish.attempts = key, s.Now(), attempts+1
	return started.TaskID, nil
}
