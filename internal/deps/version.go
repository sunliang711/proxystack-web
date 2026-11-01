package deps

import (
	"regexp"
	"strings"
)

var releaseVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// IsNewerVersion 仅在两个版本均可比较且 latest 严格较新时返回 true。
// 无法识别的自定义版本不推断升级关系，构建元数据不参与优先级比较。
func IsNewerVersion(current, latest string) bool {
	current, latest = strings.TrimSpace(current), strings.TrimSpace(latest)
	// Geo 数据使用按时间递增的纯数字发布标识。
	if versionNumeric(current) && versionNumeric(latest) {
		return compareVersionNumber(strings.TrimLeft(current, "0"), strings.TrimLeft(latest, "0")) < 0
	}
	a, b := parseReleaseVersion(current), parseReleaseVersion(latest)
	if a == nil || b == nil {
		return false
	}
	for index := 1; index <= 3; index++ {
		if order := compareVersionNumber(a[index], b[index]); order != 0 {
			return order < 0
		}
	}
	if a[4] == b[4] || b[4] != "" && a[4] == "" {
		return false
	}
	if b[4] == "" {
		return true
	}
	x, y := strings.Split(a[4], "."), strings.Split(b[4], ".")
	for index := 0; index < len(x) && index < len(y); index++ {
		left, right := x[index], y[index]
		order := strings.Compare(left, right)
		switch {
		case versionNumeric(left) && versionNumeric(right):
			order = compareVersionNumber(left, right)
		case versionNumeric(left):
			order = -1
		case versionNumeric(right):
			order = 1
		}
		if order != 0 {
			return order < 0
		}
	}
	return len(x) < len(y)
}

func parseReleaseVersion(value string) []string {
	parts := releaseVersion.FindStringSubmatch(strings.TrimSpace(value))
	if parts == nil {
		return nil
	}
	for _, identifier := range strings.Split(parts[4], ".") {
		if len(identifier) > 1 && identifier[0] == '0' && versionNumeric(identifier) {
			return nil
		}
	}
	return parts
}

func versionNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func compareVersionNumber(a, b string) int {
	// 按位数比较，避免超长数字发生整数溢出。
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}
