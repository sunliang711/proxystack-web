package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPauseWindowEnd 验证停服时段的命中判断和结束时刻：跨零点、首尾相接合并、不在时段内和覆盖全天。
func TestPauseWindowEnd(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, loc) }
	cases := []struct {
		name    string
		now     time.Time
		windows []string
		end     time.Time
		in      bool
	}{
		{"inside", at(10, 3, 0), []string{"02:00-08:00"}, at(10, 8, 0), true},
		{"start is inclusive", at(10, 2, 0), []string{"02:00-08:00"}, at(10, 8, 0), true},
		{"end is exclusive", at(10, 8, 0), []string{"02:00-08:00"}, time.Time{}, false},
		{"before", at(10, 1, 59), []string{"02:00-08:00"}, time.Time{}, false},
		{"cross midnight evening", at(10, 23, 30), []string{"23:00-06:00"}, at(11, 6, 0), true},
		{"cross midnight morning", at(11, 1, 0), []string{"23:00-06:00"}, at(11, 6, 0), true},
		{"adjacent windows merge", at(10, 23, 0), []string{"02:00-06:00", "22:00-02:00"}, at(11, 6, 0), true},
		{"overlap takes later end", at(10, 3, 0), []string{"02:00-05:00", "04:00-07:00"}, at(10, 7, 0), true},
		{"second window", at(10, 13, 30), []string{"02:00-08:00", "13:00-14:00"}, at(10, 14, 0), true},
		{"no windows", at(10, 3, 0), nil, time.Time{}, false},
		{"invalid ignored", at(10, 3, 0), []string{"bad", "02:00-08:00"}, at(10, 8, 0), true},
	}
	for _, c := range cases {
		end, in := pauseWindowEnd(c.now, c.windows)
		require.Equal(t, c.in, in, c.name)
		if c.in {
			require.True(t, c.end.Equal(end), "%s: got %s", c.name, end)
		}
	}
	// 夏令时：跳过的 02:30 落在跳变之后（03:30 EDT），回拨日按第一次出现的时刻计算。
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	end, in := pauseWindowEnd(time.Date(2026, 3, 8, 1, 45, 0, 0, ny), []string{"02:30-06:00"})
	require.False(t, in, "the window does not start before the skipped hour")
	end, in = pauseWindowEnd(time.Date(2026, 3, 8, 3, 40, 0, 0, ny), []string{"02:30-06:00"})
	require.True(t, in)
	require.True(t, time.Date(2026, 3, 8, 6, 0, 0, 0, ny).Equal(end))
	end, in = pauseWindowEnd(time.Date(2026, 3, 8, 3, 10, 0, 0, ny), []string{"02:30-06:00"})
	require.False(t, in, "03:10 EDT is before the shifted start")

	// 覆盖全天时最多延伸两天，不会死循环。
	end, in = pauseWindowEnd(at(10, 3, 0), []string{"00:00-12:00", "12:00-00:00"})
	require.True(t, in)
	require.False(t, end.Before(at(12, 3, 0)))
}
