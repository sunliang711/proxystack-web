package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
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
