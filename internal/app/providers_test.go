package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/units"
)

// TestProviderUpdates exercises applied credentials, scheduling, failed-refresh retention and stopped stacks.
func TestProviderUpdates(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	var now, count, updated atomic.Int64
	now.Store(time.Now().Unix())
	updated.Store(now.Load() - 7200)
	h.svc.Now = func() time.Time { return time.Unix(now.Load(), 0) }
	var fail atomic.Bool
	name, secret := "", ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{name: map[string]any{"updatedAt": time.Unix(updated.Load(), 0), "proxies": []map[string]string{{"name": "HK", "type": "Shadowsocks"}}}}})
			return
		}
		require.Equal(t, "/providers/proxies/"+name, r.URL.Path)
		count.Add(1)
		if fail.Load() {
			http.Error(w, "https://example.com/sub?token=do-not-leak password=do-not-leak", http.StatusServiceUnavailable)
			return
		}
		updated.Store(now.Load())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stack := doc.Stacks[1]
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	stack.Mihomo.ControllerPort, err = strconv.Atoi(port)
	require.NoError(t, err)
	stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub?token=do-not-leak", FetchMode: "direct", AutoUpdate: false, Interval: 60}}
	stack.Mihomo.Groups[0].Use = []string{"airport"}
	saved, err := h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	source := saved.Mihomo.Providers[0]
	name, secret = source.RuntimeName(), saved.Mihomo.ControllerSecret
	_, err = h.svc.ListJobs(ctx)
	require.NoError(t, err)
	taskID, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, taskID).Status)
	preview, err := h.svc.PreviewStack(ctx, stack.ID)
	require.NoError(t, err)
	require.NotContains(t, preview.Mihomo, "do-not-leak")
	views, err := h.svc.ListStackProviders(ctx, stack.ID)
	require.NoError(t, err)
	require.False(t, views[0].Pending)
	require.Equal(t, 1, views[0].NodeCount)
	h.svc.SchedulerTick(ctx)
	h.svc.WaitJobs()
	require.Zero(t, count.Load(), "automatic refresh is disabled")
	require.NoError(t, h.svc.UpdateStackProvider(ctx, h.actor, stack.ID, source.ID))
	require.Equal(t, int64(1), count.Load(), "manual refresh is independent of automatic refresh")
	views, err = h.svc.ListStackProviders(ctx, stack.ID)
	require.NoError(t, err)
	lastSuccess := views[0].LastSuccessAt
	fail.Store(true)
	now.Add(10)
	err = h.svc.UpdateStackProvider(ctx, h.actor, stack.ID, source.ID)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "do-not-leak")
	views, err = h.svc.ListStackProviders(ctx, stack.ID)
	require.NoError(t, err)
	require.Equal(t, lastSuccess, views[0].LastSuccessAt)
	require.Equal(t, 1, views[0].NodeCount)
	require.NotEmpty(t, views[0].LastError)
	require.NotContains(t, views[0].LastError, "do-not-leak")

	// Pending URL and controller credentials must not replace those used by the running core.
	saved.Mihomo.ControllerSecret = "pending-controller-secret"
	saved.Mihomo.Providers[0].URL = "https://example.com/new?token=pending-secret"
	saved.Mihomo.Providers[0].AutoUpdate = true
	_, err = h.svc.UpdateStack(ctx, h.actor, stack.ID, *saved)
	require.NoError(t, err)
	pending, err := h.svc.Pending(ctx)
	require.NoError(t, err)
	for _, change := range pending.Changes {
		require.NotContains(t, change.Diff, "do-not-leak")
		require.NotContains(t, change.Diff, "pending-secret")
	}
	fail.Store(false)
	now.Add(61)
	h.svc.SchedulerTick(ctx)
	h.svc.WaitJobs()
	require.Equal(t, int64(3), count.Load(), "a due automatic refresh uses the applied credentials")
	views, err = h.svc.ListStackProviders(ctx, stack.ID)
	require.NoError(t, err)
	require.True(t, views[0].Pending)
	require.Empty(t, views[0].LastError)
	h.svc.SchedulerTick(ctx)
	h.svc.WaitJobs()
	require.Equal(t, int64(3), count.Load(), "a successful refresh resets the automatic deadline")
	require.NoError(t, h.units.Stop(ctx, units.Name(units.Mihomo, stack.Name)))
	require.Error(t, h.svc.UpdateStackProvider(ctx, h.actor, stack.ID, source.ID))
	require.Equal(t, int64(3), count.Load())
}

// TestProviderConcurrentUpdates verifies that overlapping clicks share a single native update.
func TestProviderConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var count atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"providers":{}}`))
			return
		}
		count.Add(1)
		once.Do(func() { close(started) })
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stack := doc.Stacks[1]
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	stack.Mihomo.ControllerPort, err = strconv.Atoi(port)
	require.NoError(t, err)
	stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600}}
	saved, err := h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	id := saved.Mihomo.Providers[0].ID
	task, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, task).Status)
	result := make(chan error, 2)
	go func() { result <- h.svc.UpdateStackProvider(ctx, h.actor, stack.ID, id) }()
	<-started
	joinCtx, cancel := context.WithCancel(ctx)
	secondStarted := make(chan struct{})
	go func() { close(secondStarted); result <- h.svc.UpdateStackProvider(joinCtx, h.actor, stack.ID, id) }()
	<-secondStarted
	// A canceled joiner must leave the original core operation running.
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	close(release)
	require.NoError(t, <-result)
	require.Equal(t, int64(1), count.Load())
	err = h.svc.UpdateStackProvider(ctx, h.actor, doc.Stacks[2].ID, id)
	require.ErrorIs(t, err, app.ErrNotFound, fmt.Sprint(err))
}

// TestProviderCloneKeepsOriginalOwnership prevents an upsert from moving the source collection to its clone.
func TestProviderCloneKeepsOriginalOwnership(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stack := doc.Stacks[1]
	stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600}}
	stack.Mihomo.Groups[0].Use = []string{"airport"}
	saved, err := h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	originalID := saved.Mihomo.Providers[0].ID
	clone, err := h.svc.CloneStack(ctx, h.actor, saved.ID, "provider-clone")
	require.NoError(t, err)
	require.NotEqual(t, originalID, clone.Mihomo.Providers[0].ID)
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, originalID, doc.Stacks[doc.StackByName(stack.Name)].Mihomo.Providers[0].ID)
	require.Empty(t, configdoc.Validate(doc))
}

// providerBlockingChecker holds apply's runtime lock until the test releases it.
type providerBlockingChecker struct {
	started, release chan struct{}
	once             sync.Once
}

// Check models a slow core validation during a configuration apply.
func (c *providerBlockingChecker) Check(ctx context.Context, _, _ string, _ []byte) error {
	c.once.Do(func() { close(c.started) })
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestProviderLockWaitRespectsCancellation prevents status and refresh requests from hanging behind apply.
func TestProviderLockWaitRespectsCancellation(t *testing.T) {
	h := newRuntimeHarness(t)
	doc, err := h.svc.LoadConfig(context.Background())
	require.NoError(t, err)
	stack := doc.Stacks[1]
	stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600}}
	saved, err := h.svc.UpdateStack(context.Background(), h.actor, stack.ID, stack)
	require.NoError(t, err)
	checker := &providerBlockingChecker{started: make(chan struct{}), release: make(chan struct{})}
	h.svc.Checker = checker
	task, err := h.svc.Apply(context.Background(), h.actor, nil)
	require.NoError(t, err)
	<-checker.started
	defer func() { close(checker.release); h.waitTask(t, task) }()
	for _, status := range []bool{true, false} {
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		start := time.Now()
		if status {
			_, err = h.svc.ListStackProviders(ctx, stack.ID)
		} else {
			err = h.svc.UpdateStackProvider(ctx, h.actor, stack.ID, saved.Mihomo.Providers[0].ID)
		}
		cancel()
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Less(t, time.Since(start), time.Second)
	}
}
