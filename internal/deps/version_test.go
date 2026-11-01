package deps_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/deps"
)

func TestIsNewerVersion(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		want            bool
	}{
		{"v1.9.0", "v1.10.0", true},
		{"202610302203", "202610312203", true},
		{"202610312203", "202610312203", false},
		{"202611012203", "202610312203", false},
		{"202610312203", "latest", false},
		{"v26.9.9", "26.10.1", true},
		{"v1.10.0", "v1.9.0", false},
		{"1.2.3", "v1.2.3", false},
		{"1.2.3+custom", "1.2.3+release", false},
		{"1.2.3-rc.2", "1.2.3-rc.10", true},
		{"1.2.3-rc.10", "1.2.3-rc.2", false},
		{"1.2.3-rc.1", "1.2.3", true},
		{"1.2.3", "1.2.3-rc.1", false},
		{"1.2.3-alpha", "1.2.3-alpha.1", true},
		{"1.2.3-1", "1.2.3-alpha", true},
		{"1.2.3-alpha", "1.2.3-1", false},
		{"", "1.2.3", false},
		{"1.2.3", "", false},
		{"unknown", "1.2.3", false},
		{"v1.2.3-dirty", "Meta", false},
		{"1.2.3", "1.2.03", false},
		{"1.2.3-rc.01", "1.2.3", false},
		{"1.2.3", "1.2.3-rc.01", false},
		{"1.2.999999999999999999999", "1.2.1000000000000000000000", true},
	} {
		t.Run(tc.current+"/"+tc.latest, func(t *testing.T) {
			require.Equal(t, tc.want, deps.IsNewerVersion(tc.current, tc.latest))
		})
	}
}
