package configdoc

import (
	"fmt"
	"strings"
	"time"
)

// 用户的每日停服时段：跟随全局设置、使用自己的时段或不限制。
const (
	PauseWindowInherit = Inherit
	PauseWindowCustom  = "custom"
	PauseWindowOff     = Off
)

// ParsePauseWindow 解析 HH:MM-HH:MM，返回起止时刻距零点的分钟数；结束早于开始表示跨零点。
func ParsePauseWindow(text string) (start, end int, err error) {
	from, to, ok := strings.Cut(text, "-")
	if !ok {
		return 0, 0, fmt.Errorf("must use HH:MM-HH:MM")
	}
	parse := func(value string) (int, error) {
		at, err := time.Parse("15:04", value)
		if err != nil {
			return 0, fmt.Errorf("must use HH:MM-HH:MM")
		}
		return at.Hour()*60 + at.Minute(), nil
	}
	if start, err = parse(from); err != nil {
		return 0, 0, err
	}
	if end, err = parse(to); err != nil {
		return 0, 0, err
	}
	if start == end {
		return 0, 0, fmt.Errorf("start and end must differ")
	}
	return start, end, nil
}

// normalizePauseWindows 把可解析的时段改写为补零的 HH:MM-HH:MM，nil 改为空列表。
func normalizePauseWindows(windows []string) []string {
	out := make([]string, 0, len(windows))
	for _, window := range windows {
		if start, end, err := ParsePauseWindow(window); err == nil {
			window = fmt.Sprintf("%02d:%02d-%02d:%02d", start/60, start%60, end/60, end%60)
		}
		out = append(out, window)
	}
	return out
}

// EffectivePauseWindows 返回用户实际生效的停服时段。
func EffectivePauseWindows(mode string, own, global []string) []string {
	switch mode {
	case PauseWindowCustom:
		return own
	case PauseWindowOff:
		return nil
	default:
		return global
	}
}

// JoinPauseWindows 和 SplitPauseWindows 在时段列表与数据库中逗号分隔的文本之间转换。
func JoinPauseWindows(windows []string) string { return strings.Join(windows, ",") }

func SplitPauseWindows(text string) []string {
	if text == "" {
		return []string{}
	}
	return strings.Split(text, ",")
}

// checkPauseWindows 把错误记在列表本身的路径上，表单在整个时段编辑器下方显示。
func checkPauseWindows(issues *Issues, path string, windows []string) {
	for index, window := range windows {
		if _, _, err := ParsePauseWindow(window); err != nil {
			issues.add(path, "window %d (%q): %s", index+1, window, err.Error())
		}
	}
}
