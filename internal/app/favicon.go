package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"proxystack-web/internal/access"
	"proxystack-web/internal/favicon"
	"proxystack-web/internal/store/dbq"
)

const (
	// faviconTTL 是取到的图标的缓存时长，到期后访问时重新获取。
	faviconTTL = 30 * 24 * time.Hour
	// faviconMissTTL 是「网站没有图标」或「不允许访问」的结论的缓存时长，期间不再重试。
	faviconMissTTL = 7 * 24 * time.Hour
	// faviconRetryTTL 是暂时失败（连不上、超时）后的重试间隔。
	faviconRetryTTL = time.Hour
	// faviconKeep 是缓存的保留时长：超过这么久没有获取过的记录由流量数据清理任务删除。
	faviconKeep = 90 * 24 * time.Hour
	// faviconTimeout 是获取一个图标的总时长上限（含跟随跳转和读取首页），从拿到获取名额时开始计算。
	faviconTimeout = 25 * time.Second
	// faviconQueueWait 是等待获取名额的时长上限。等不到时本次返回没有图标、不写缓存，避免请求长时间占住浏览器连接。
	faviconQueueWait = 3 * time.Second
	// faviconConcurrency 是同时获取图标的个数上限。
	faviconConcurrency = 4
)

// errFaviconBusy 表示等不到获取名额。
var errFaviconBusy = errors.New("favicon fetch queue is full")

// faviconState 合并同一域名的并发获取，并限制同时获取的个数。
type faviconState struct {
	mu       sync.Mutex
	inflight map[string]*faviconCall
	slots    chan struct{}
}

type faviconCall struct {
	done chan struct{}
	icon *favicon.Icon
}

// AccessFavicon 返回主域名的图标：有手动设置的图标时用它；否则缓存未到期时直接使用缓存，再否则经下载代理获取。
// 没有图标、域名不是公网域名、下载代理不可用或获取排队过久时返回 ErrNotFound。
func (s *Service) AccessFavicon(ctx context.Context, name string) (*favicon.Icon, error) {
	domain := access.Domain(name)
	if domain == "" {
		return nil, ErrNotFound
	}
	// 手动设置的图标优先，包括不会自动获取的内部域名。
	if manual, err := s.manualIcon(ctx, IconRef{Kind: IconKindDomain, Name: domain}); err != nil || manual != nil {
		return manual, err
	}
	if !favicon.PublicDomain(domain) {
		return nil, ErrNotFound
	}
	cached, err := s.cachedFavicon(ctx, domain)
	if err != nil {
		return nil, err
	}
	if cached != nil && s.Now().Unix() < cached.ExpiresAt {
		if cached.Ok == 0 {
			return nil, ErrNotFound
		}
		return &favicon.Icon{ContentType: cached.ContentType, Data: cached.Data}, nil
	}
	if icon := s.refreshFavicon(ctx, domain); icon != nil {
		return icon, nil
	}
	return nil, ErrNotFound
}

func (s *Service) cachedFavicon(ctx context.Context, domain string) (*dbq.Favicon, error) {
	var cached *dbq.Favicon
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		row, err := q.GetFavicon(ctx, domain)
		if err == nil {
			cached = &row
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return nil
	})
	return cached, err
}

// refreshFavicon 获取图标并写入缓存，返回可用的图标（取到的，或保留的旧图标），没有时返回 nil。
// 同一域名同时只获取一次：第一个请求负责获取和写缓存，其他请求等待它的结果。获取不随请求取消而中断。
func (s *Service) refreshFavicon(ctx context.Context, domain string) *favicon.Icon {
	s.favicons.mu.Lock()
	if s.favicons.inflight == nil {
		s.favicons.inflight = map[string]*faviconCall{}
		s.favicons.slots = make(chan struct{}, faviconConcurrency)
	}
	if call, ok := s.favicons.inflight[domain]; ok {
		s.favicons.mu.Unlock()
		select {
		case <-call.done:
			return call.icon
		case <-ctx.Done():
			return nil
		}
	}
	call := &faviconCall{done: make(chan struct{})}
	s.favicons.inflight[domain] = call
	slots := s.favicons.slots
	s.favicons.mu.Unlock()
	defer func() {
		s.favicons.mu.Lock()
		delete(s.favicons.inflight, domain)
		s.favicons.mu.Unlock()
		close(call.done)
	}()

	work := context.WithoutCancel(ctx)
	// 进入获取前再读一次缓存：上一个获取者可能刚写完。
	cached, err := s.cachedFavicon(work, domain)
	if err != nil {
		slog.Warn("favicon cache", "domain", domain, "err", err)
		return nil
	}
	now := s.Now()
	var stale *favicon.Icon
	if cached != nil {
		if cached.Ok != 0 {
			stale = &favicon.Icon{ContentType: cached.ContentType, Data: cached.Data}
		}
		if now.Unix() < cached.ExpiresAt {
			call.icon = stale
			return call.icon
		}
	}

	icon, err := s.fetchFaviconSlot(work, slots, domain)
	switch {
	case err == nil:
		s.storeFavicon(work, domain, icon, now, faviconTTL)
		call.icon = icon
	case errors.Is(err, favicon.ErrProxy), errors.Is(err, errFaviconBusy):
		// 与网站无关的失败：不改缓存，有旧图标时继续使用。
		call.icon = stale
	default:
		ttl := faviconRetryTTL
		if errors.Is(err, favicon.ErrNoIcon) || errors.Is(err, favicon.ErrForbidden) {
			ttl = faviconMissTTL
		}
		slog.Debug("favicon", "domain", domain, "err", err)
		// 有旧图标时保留它，按失败类型推迟下次获取。
		s.storeFavicon(work, domain, stale, now, ttl)
		call.icon = stale
	}
	return call.icon
}

// fetchFaviconSlot 等待获取名额后获取图标；等待超过 faviconQueueWait 返回 errFaviconBusy。
func (s *Service) fetchFaviconSlot(ctx context.Context, slots chan struct{}, domain string) (*favicon.Icon, error) {
	timer := time.NewTimer(faviconQueueWait)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-timer.C:
		return nil, errFaviconBusy
	}
	fetchCtx, cancel := context.WithTimeout(ctx, faviconTimeout)
	defer cancel()
	return s.fetchFavicon(fetchCtx, domain)
}

// fetchFavicon 按下载代理设置获取图标。下载代理配置不可用（例如所选 stack 未运行）时返回 favicon.ErrProxy。
func (s *Service) fetchFavicon(ctx context.Context, domain string) (*favicon.Icon, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, errors.Join(favicon.ErrProxy, err)
	}
	cfg, err := s.downloadProxyConfig(ctx, doc, doc.Settings.DownloadProxy)
	if err != nil {
		return nil, errors.Join(favicon.ErrProxy, err)
	}
	if s.FetchFavicon != nil {
		return s.FetchFavicon(ctx, cfg, domain)
	}
	fetcher := favicon.New(cfg)
	defer fetcher.Close()
	return fetcher.Fetch(ctx, domain)
}

// storeFavicon 写入缓存；icon 为 nil 表示没有图标。
func (s *Service) storeFavicon(ctx context.Context, domain string, icon *favicon.Icon, now time.Time, ttl time.Duration) {
	params := dbq.PutFaviconParams{Domain: domain, Data: []byte{}, FetchedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix()}
	if icon != nil {
		params.Ok, params.ContentType, params.Data = 1, icon.ContentType, icon.Data
	}
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error { return q.PutFavicon(ctx, params) }); err != nil {
		slog.Warn("favicon cache", "domain", domain, "err", err)
	}
}

// pruneFavicons 删除长期没有获取过的图标缓存。
func (s *Service) pruneFavicons(ctx context.Context, now time.Time) {
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error { return q.DeleteFaviconsBefore(ctx, now.Add(-faviconKeep).Unix()) }); err != nil {
		slog.Warn("favicon prune", "err", err)
	}
}
