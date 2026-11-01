package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/deps"
)

type egressFamilyFetcher struct {
	deps.Fetcher
	successURL string
	ip         string
	started    chan struct{}
	cancelled  chan struct{}
	startOnce  sync.Once
	cancelOnce sync.Once
}

func (f *egressFamilyFetcher) Get(ctx context.Context, url string) ([]byte, error) {
	if url == f.successURL {
		<-f.started
		return []byte(fmt.Sprintf(`{"ip":%q}`, f.ip)), nil
	}
	f.startOnce.Do(func() { close(f.started) })
	<-ctx.Done()
	f.cancelOnce.Do(func() { close(f.cancelled) })
	return nil, ctx.Err()
}

// TestEgressFamiliesReturnOnFirstSuccess 验证任一地址族成功即返回并取消另一族，不等待其超时。
func TestEgressFamiliesReturnOnFirstSuccess(t *testing.T) {
	for _, tc := range []struct {
		family string
		ip     string
	}{
		{family: "ipv4", ip: "203.0.113.12"},
		{family: "ipv6", ip: "2001:db8::12"},
	} {
		t.Run(tc.family, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			fetcher := &egressFamilyFetcher{successURL: egressSources[tc.family][0], ip: tc.ip,
				started: make(chan struct{}), cancelled: make(chan struct{})}
			completed := make(chan []EgressResult, 1)
			go func() { completed <- probeEgressFamilies(ctx, fetcher, 5*time.Second) }()
			select {
			case results := <-completed:
				require.NotEmpty(t, results)
				require.Equal(t, tc.ip, results[len(results)-1].IP)
			case <-time.After(time.Second):
				t.Fatal("successful family waited for the other family")
			}
			select {
			case <-fetcher.cancelled:
			case <-time.After(time.Second):
				t.Fatal("other family was not cancelled")
			}
		})
	}
}
