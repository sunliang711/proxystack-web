package app

import (
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store/dbq"
)

// userPauseWindows 返回用户实际生效的每日停服时段。
func userPauseWindows(user dbq.User, settings configdoc.Settings) []string {
	return configdoc.EffectivePauseWindows(user.PauseWindowMode, configdoc.SplitPauseWindows(user.PauseWindows), settings.Traffic.PauseWindows)
}

// pauseWindowEnd 判断 at 是否落在任一停服时段内，并返回这段连续停服的结束时刻：
// 首尾相接或重叠的时段视为一段，例如 22:00-02:00 和 02:00-06:00 在 06:00 结束。
// 时段按 at 所在时区的墙上时间计算，可以跨零点。
func pauseWindowEnd(at time.Time, windows []string) (time.Time, bool) {
	type span struct{ start, end int }
	spans := make([]span, 0, len(windows))
	for _, window := range windows {
		start, end, err := configdoc.ParsePauseWindow(window)
		if err == nil {
			spans = append(spans, span{start, end})
		}
	}
	// covering 返回包含时刻 t 的时段中最晚的结束时刻。每个时段只需检查从前一天和当天开始的两次。
	covering := func(t time.Time) (time.Time, bool) {
		var best time.Time
		found := false
		year, month, day := t.Date()
		for _, s := range spans {
			for _, offset := range []int{-1, 0} {
				begin := wallClock(year, month, day+offset, s.start, t.Location())
				finish := wallClock(year, month, day+offset, s.end, t.Location())
				if s.end < s.start {
					finish = wallClock(year, month, day+offset+1, s.end, t.Location())
				}
				if !t.Before(begin) && t.Before(finish) && finish.After(best) {
					best, found = finish, true
				}
			}
		}
		return best, found
	}
	end, ok := covering(at)
	if !ok {
		return time.Time{}, false
	}
	// 时段覆盖全天时不存在结束时刻，延伸到距 at 两天以上即停止。
	limit := at.Add(48 * time.Hour)
	for end.Before(limit) {
		next, ok := covering(end)
		if !ok {
			break
		}
		end = next
	}
	return end, true
}

// wallClock 返回某天距零点 minute 分钟的墙上时间。夏令时跳过的时刻不存在，
// time.Date 会把它归一到跳变之前，这里补回差值，使其落在跳变之后。
func wallClock(year int, month time.Month, day, minute int, loc *time.Location) time.Time {
	at := time.Date(year, month, day, minute/60, minute%60, 0, 0, loc)
	want := time.Date(year, month, day, minute/60, minute%60, 0, 0, time.UTC)
	got := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), 0, 0, time.UTC)
	if diff := want.Sub(got); diff > 0 {
		at = at.Add(diff)
	}
	return at
}
