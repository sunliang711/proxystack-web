package version

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	const revision = "abc1234567890abcdef"
	vcs := func(modified bool) *debug.BuildInfo {
		flag := "false"
		if modified {
			flag = "true"
		}
		return &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.modified", Value: flag},
			{Key: "vcs.time", Value: "2026-10-01T00:00:00Z"},
		}}
	}
	for _, tc := range []struct {
		name  string
		input Info
		build *debug.BuildInfo
		want  Info
	}{
		{
			name:  "release injection takes priority",
			input: Info{Version: "v0.0.7", Commit: "1234567", Date: "2026-10-31T00:00:00Z"}, build: vcs(true),
			want: Info{Version: "v0.0.7", Commit: "1234567", Date: "2026-10-31T00:00:00Z"},
		},
		{
			name:  "local make build retains git describe",
			input: Info{Version: "v0.0.7-2-g1234567-dirty", Commit: "1234567", Date: "2026-10-31T00:00:00Z"}, build: vcs(true),
			want: Info{Version: "v0.0.7-2-g1234567-dirty", Commit: "1234567", Date: "2026-10-31T00:00:00Z"},
		},
		{
			name: "plain build clean", input: Info{Version: "dev", Commit: "unknown", Date: "unknown"}, build: vcs(false),
			want: Info{Version: "dev+abc1234", Commit: revision, Date: "unknown"},
		},
		{
			name: "plain build dirty", input: Info{Version: "dev", Commit: "unknown", Date: "unknown"}, build: vcs(true),
			want: Info{Version: "dev+abc1234.dirty", Commit: revision, Date: "unknown"},
		},
		{
			name: "missing build info", input: Info{Version: "dev", Commit: "unknown", Date: "unknown"},
			want: Info{Version: "dev", Commit: "unknown", Date: "unknown"},
		},
		{
			name: "empty injected fields", input: Info{}, build: vcs(false),
			want: Info{Version: "dev+abc1234", Commit: revision, Date: "unknown"},
		},
		{
			name: "unknown version", input: Info{Version: "unknown", Commit: "unknown"}, build: vcs(false),
			want: Info{Version: "dev+abc1234", Commit: revision, Date: "unknown"},
		},
		{
			name: "injected commit without vcs", input: Info{Version: "dev", Commit: "123456789"},
			want: Info{Version: "dev+1234567", Commit: "123456789", Date: "unknown"},
		},
		{
			name: "short commit", input: Info{Version: "dev", Commit: "abc"},
			want: Info{Version: "dev+abc", Commit: "abc", Date: "unknown"},
		},
		{
			name: "no vcs settings", input: Info{}, build: &debug.BuildInfo{},
			want: Info{Version: "dev", Commit: "unknown", Date: "unknown"},
		},
		{
			name: "dirty without revision", input: Info{}, build: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}},
			want: Info{Version: "dev+dirty", Commit: "unknown", Date: "unknown"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, resolve(tc.input, tc.build))
		})
	}
}
