package subdeploy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnitFileUsesRequestedDirectory(t *testing.T) {
	unit := unitFile("/opt/custom-sub")
	require.Contains(t, unit, "ExecStart=/opt/custom-sub/bin/proxystack-web sub --dir /opt/custom-sub serve")
	require.Contains(t, unit, "ReadWritePaths=/opt/custom-sub")
}

func TestNormalizeDir(t *testing.T) {
	require.Equal(t, DefaultDir, normalizeDir(""))
	require.Equal(t, "/opt/sub", normalizeDir("/opt/other/../sub"))
}

func TestStoppedServiceState(t *testing.T) {
	for _, state := range []string{"inactive", "failed", "unknown", "not-found"} {
		require.True(t, stoppedServiceState(state), state)
	}
	for _, state := range []string{"", "active", "activating", "deactivating", "reloading"} {
		require.False(t, stoppedServiceState(state), state)
	}
}
