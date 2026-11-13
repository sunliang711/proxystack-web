package app_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/deps"
	"proxystack-web/internal/favicon"
)

func TestAccessFaviconCache(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	var calls atomic.Int32
	var mu sync.Mutex
	result := map[string]error{"missing.com": favicon.ErrNoIcon, "flaky.com": errors.New("i/o timeout"), "proxydown.com": favicon.ErrProxy}
	h.svc.FetchFavicon = func(_ context.Context, cfg deps.ProxyConfig, domain string) (*favicon.Icon, error) {
		calls.Add(1)
		require.Equal(t, deps.ProxyDirect, cfg.Kind, "uses the download proxy settings")
		mu.Lock()
		defer mu.Unlock()
		if err := result[domain]; err != nil {
			return nil, err
		}
		return &favicon.Icon{ContentType: "image/png", Data: []byte(domain)}, nil
	}

	// 子域名先归并为主域名；取到后缓存，再次请求不再获取。
	icon, err := h.svc.AccessFavicon(ctx, "www.Example.com")
	require.NoError(t, err)
	require.Equal(t, "example.com", string(icon.Data))
	_, err = h.svc.AccessFavicon(ctx, "example.com")
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())

	// 没有图标：缓存 7 天，期间不重试，过期后重试。
	_, err = h.svc.AccessFavicon(ctx, "missing.com")
	require.ErrorIs(t, err, app.ErrNotFound)
	_, err = h.svc.AccessFavicon(ctx, "missing.com")
	require.ErrorIs(t, err, app.ErrNotFound)
	require.Equal(t, int32(2), calls.Load())
	h.now = h.now.Add(8 * 24 * time.Hour)
	mu.Lock()
	delete(result, "missing.com")
	mu.Unlock()
	_, err = h.svc.AccessFavicon(ctx, "missing.com")
	require.NoError(t, err)
	require.Equal(t, int32(3), calls.Load())

	// 暂时失败（连不上、超时）：1 小时后重试。
	_, err = h.svc.AccessFavicon(ctx, "flaky.com")
	require.ErrorIs(t, err, app.ErrNotFound)
	_, err = h.svc.AccessFavicon(ctx, "flaky.com")
	require.ErrorIs(t, err, app.ErrNotFound)
	require.Equal(t, int32(4), calls.Load())
	h.now = h.now.Add(61 * time.Minute)
	mu.Lock()
	delete(result, "flaky.com")
	mu.Unlock()
	_, err = h.svc.AccessFavicon(ctx, "flaky.com")
	require.NoError(t, err)
	require.Equal(t, int32(5), calls.Load())

	// 下载代理不可用：不缓存，代理恢复后马上能取到。
	_, err = h.svc.AccessFavicon(ctx, "proxydown.com")
	require.ErrorIs(t, err, app.ErrNotFound)
	mu.Lock()
	delete(result, "proxydown.com")
	mu.Unlock()
	_, err = h.svc.AccessFavicon(ctx, "proxydown.com")
	require.NoError(t, err)

	// 缓存过期后网站暂时取不到：继续使用旧图标。
	h.now = h.now.Add(31 * 24 * time.Hour)
	mu.Lock()
	result["example.com"] = errors.New("timeout")
	mu.Unlock()
	icon, err = h.svc.AccessFavicon(ctx, "example.com")
	require.NoError(t, err)
	require.Equal(t, "example.com", string(icon.Data))
	// 旧图标继续使用，1 小时内不再重试。
	before := calls.Load()
	_, err = h.svc.AccessFavicon(ctx, "example.com")
	require.NoError(t, err)
	require.Equal(t, before, calls.Load())

	// 不是公网域名的不获取。
	for _, name := range []string{"printer.lan", "8.8.8.8", "co.uk", ""} {
		_, err = h.svc.AccessFavicon(ctx, name)
		require.ErrorIs(t, err, app.ErrNotFound, name)
	}
	require.Equal(t, before, calls.Load())
}

func TestAccessFaviconSharesConcurrentFetches(t *testing.T) {
	h := newTrafficHarness(t)
	release := make(chan struct{})
	var calls atomic.Int32
	h.svc.FetchFavicon = func(context.Context, deps.ProxyConfig, string) (*favicon.Icon, error) {
		calls.Add(1)
		<-release
		return &favicon.Icon{ContentType: "image/png", Data: []byte("x")}, nil
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.svc.AccessFavicon(context.Background(), "example.com")
			require.NoError(t, err)
		}()
	}
	require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
}
