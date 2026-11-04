package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/deps"
	"proxystack-web/internal/version"
)

func TestSelfUpgradeScheduleLifecycle(t *testing.T) {
	h := newRuntimeHarness(t)
	ctx := context.Background()

	state, err := h.svc.ScheduleSelfUpgrade(ctx, h.actor, app.SelfUpgradeRequest{Target: "v0.3.7", TimeoutSeconds: 60, IntervalSeconds: 5})
	require.NoError(t, err)
	require.Equal(t, app.SelfUpgradeWaiting, state.Status)
	require.Equal(t, "v0.3.7", state.Target)
	require.Equal(t, 60, state.TimeoutSeconds)
	require.Equal(t, 5, state.IntervalSeconds)

	_, err = h.svc.ScheduleSelfUpgrade(ctx, h.actor, app.SelfUpgradeRequest{Target: "latest"})
	require.ErrorContains(t, err, "already active")

	state, err = h.svc.CancelSelfUpgrade(ctx, h.actor)
	require.NoError(t, err)
	require.Equal(t, app.SelfUpgradeCancelled, state.Status)
}

func TestSelfUpgradeScheduleValidation(t *testing.T) {
	h := newRuntimeHarness(t)
	ctx := context.Background()
	for _, request := range []app.SelfUpgradeRequest{
		{Target: "release/latest"},
		{Target: "v0.3.7", TimeoutSeconds: 10},
		{Target: "v0.3.7", IntervalSeconds: 2},
	} {
		_, err := h.svc.ScheduleSelfUpgrade(ctx, h.actor, request)
		require.Error(t, err)
	}
}

// 目标为 latest 且当前已是最新版本时，预约在整个等待时长内持续检查，到截止时间才结束，结束后可以清除结果。
func TestSelfUpgradeLatestKeepsWaitingUntilDeadline(t *testing.T) {
	h := newRuntimeHarness(t)
	fakeSelfGitHub(t, "v0.4.2")
	previousVersion := version.Version
	version.Version = "v0.4.2"
	t.Cleanup(func() { version.Version = previousVersion })

	start := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(start.UnixNano())
	h.svc.Now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	advance := func(d time.Duration) { clock.Store(start.Add(d).UnixNano()) }

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		h.svc.WaitBackground()
	}()

	state, err := h.svc.ScheduleSelfUpgrade(ctx, h.actor, app.SelfUpgradeRequest{Target: "latest", TimeoutSeconds: 60, IntervalSeconds: 5})
	require.NoError(t, err)
	require.NoError(t, h.svc.StartBackground(ctx))

	require.Eventually(t, func() bool {
		state, err = h.svc.SelfUpgradeSchedule(ctx)
		return err == nil && state.Status == app.SelfUpgradeWaiting && state.ResolvedVersion == "v0.4.2"
	}, 5*time.Second, 20*time.Millisecond)

	// 多个检查周期之后仍在等待，没有提前标记完成。
	for _, offset := range []time.Duration{10 * time.Second, 30 * time.Second} {
		advance(offset)
		require.Eventually(t, func() bool {
			state, err = h.svc.SelfUpgradeSchedule(ctx)
			return err == nil && state.LastCheckAt.Equal(start.Add(offset))
		}, 5*time.Second, 20*time.Millisecond)
		require.Equal(t, app.SelfUpgradeWaiting, state.Status)
	}

	advance(61 * time.Second)
	require.Eventually(t, func() bool {
		state, err = h.svc.SelfUpgradeSchedule(ctx)
		return err == nil && state.Status == app.SelfUpgradeTimeout
	}, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, "等待期内没有发布比当前更新的版本", state.LastError)

	state, err = h.svc.CancelSelfUpgrade(ctx, h.actor)
	require.NoError(t, err)
	require.Equal(t, app.SelfUpgradeIdle, state.Status)
	state, err = h.svc.SelfUpgradeSchedule(ctx)
	require.NoError(t, err)
	require.Equal(t, app.SelfUpgradeIdle, state.Status)
	require.Empty(t, state.Target)
}

func TestSelfUpgradeSpecificCurrentCompletesImmediately(t *testing.T) {
	h := newRuntimeHarness(t)
	fakeSelfGitHub(t, "v0.4.2")
	previousVersion := version.Version
	version.Version = "v0.4.2"
	t.Cleanup(func() { version.Version = previousVersion })

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		h.svc.WaitBackground()
	}()

	state, err := h.svc.ScheduleSelfUpgrade(ctx, h.actor, app.SelfUpgradeRequest{Target: "v0.4.2", TimeoutSeconds: 60, IntervalSeconds: 5})
	require.NoError(t, err)
	require.NoError(t, h.svc.StartBackground(ctx))

	require.Eventually(t, func() bool {
		state, err = h.svc.SelfUpgradeSchedule(ctx)
		return err == nil && state.Status == app.SelfUpgradeSucceeded
	}, 3*time.Second, 20*time.Millisecond)
}

func fakeSelfGitHub(t *testing.T, tag string) {
	t.Helper()
	assetName := fmt.Sprintf("proxystack-web-%s-%s", runtime.GOOS, runtime.GOARCH)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag,
			"assets": []map[string]any{
				{"name": assetName, "browser_download_url": "http://example.invalid/" + assetName},
				{"name": assetName + ".sha256", "browser_download_url": "http://example.invalid/" + assetName + ".sha256"},
			},
		})
	}))
	previous := deps.GitHubAPI
	deps.GitHubAPI = server.URL
	t.Cleanup(func() {
		deps.GitHubAPI = previous
		server.Close()
	})
}
