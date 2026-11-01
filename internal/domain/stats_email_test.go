package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUserStatsEmail 验证日志标识包含用户名，同时保留用户和档案的稳定 ID。
func TestUserStatsEmail(t *testing.T) {
	for _, tc := range []struct {
		name      string
		user      string
		userID    int64
		profileID int64
		want      string
	}{
		{name: "default", user: "eagle", userID: 7, want: "eagle.u7"},
		{name: "profile", user: "eagle", userID: 7, profileID: 3, want: "eagle.u7.p3"},
		{name: "legacy", userID: 7, want: "u7"},
		{name: "legacy profile", userID: 7, profileID: 3, want: "u7.p3"},
		{name: "unsaved", user: "eagle", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, UserStatsEmail(tc.user, tc.userID, tc.profileID))
		})
	}
}

// TestParseUserStatsEmail 验证新旧格式、改名和包含分隔符的用户名都能正确归属。
func TestParseUserStatsEmail(t *testing.T) {
	for _, tc := range []struct {
		email     string
		userID    int64
		profileID int64
		ok        bool
	}{
		{email: "u7", userID: 7, ok: true},
		{email: "u7.p3", userID: 7, profileID: 3, ok: true},
		{email: "eagle.u7", userID: 7, ok: true},
		{email: "renamed.u7", userID: 7, ok: true},
		{email: "eagle.u7.p3", userID: 7, profileID: 3, ok: true},
		{email: "alice.u9.p2.u7.p3", userID: 7, profileID: 3, ok: true},
		{email: "psw-placeholder"},
		{email: "eagle"},
		{email: "eagle.u0"},
		{email: "eagle.u-7"},
		{email: "eagle.u7.p0"},
		{email: "eagle.u7.pbad"},
		{email: "eagle.u7.extra"},
	} {
		t.Run(tc.email, func(t *testing.T) {
			userID, profileID, ok := ParseUserStatsEmail(tc.email)
			require.Equal(t, tc.ok, ok)
			if tc.ok {
				require.Equal(t, tc.userID, userID)
				require.Equal(t, tc.profileID, profileID)
			}
		})
	}
}
