package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProviderCacheIsolation rejects path traversal, absolute paths and escaping symlinks.
func TestProviderCacheIsolation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "providers"), 0o750))
	outside := filepath.Join(t.TempDir(), "private.yaml")
	require.NoError(t, os.WriteFile(outside, []byte("proxies: [{name: private, password: outside-secret}]"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "providers", "escape.yaml")))
	for _, path := range []string{outside, "../private.yaml", "providers/../../private.yaml", "providers/escape.yaml"} {
		_, err := readProviderNodeCache(root, map[string]any{"path": path})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "outside-secret")
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "providers", "invalid.yaml"), []byte("proxies: [broken secret-value"), 0o600))
	_, err := readProviderNodeCache(root, map[string]any{"path": "providers/invalid.yaml"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-value")
}
