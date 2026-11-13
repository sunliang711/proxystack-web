package configdoc

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/publicsuffix"
)

// maxAccessGroupName 是手动归类中服务名的最大长度（字符数）。
const maxAccessGroupName = 40

// ParseAccessGroup 解析一条手动归类「域名=服务名」：域名必须是主域名（访问统计记录的那一级），匹配它本身及全部子域名，
// 规范为小写；服务名去掉首尾空白。
func ParseAccessGroup(entry string) (domain, service string, err error) {
	left, right, ok := strings.Cut(entry, "=")
	if !ok {
		return "", "", fmt.Errorf("must be domain=service")
	}
	if domain, err = blockedHost(left); err != nil {
		return "", "", err
	}
	// 访问统计按主域名记录，比主域名更具体的域名（例如 www.example.com）永远匹配不到，公共后缀本身也不行。
	registrable, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil {
		return "", "", fmt.Errorf("must be a registrable domain, not a public suffix")
	}
	if registrable != domain {
		return "", "", fmt.Errorf("must be a registrable domain such as %s; statistics are kept per registrable domain", registrable)
	}
	service = strings.TrimSpace(right)
	if service == "" || utf8.RuneCountInString(service) > maxAccessGroupName || strings.ContainsAny(service, "=\r\n\t") {
		return "", "", fmt.Errorf("service must be 1 to %d characters", maxAccessGroupName)
	}
	return domain, service, nil
}

// AccessGroupMap 把手动归类转为「域名 → 服务名」，无法解析的条目跳过；同一域名出现多次时以最后一条为准。
func AccessGroupMap(entries []string) map[string]string {
	out := map[string]string{}
	for _, entry := range entries {
		if domain, service, err := ParseAccessGroup(entry); err == nil {
			out[domain] = service
		}
	}
	return out
}

// normalizeAccessGroups 把可解析的条目改写为「域名=服务名」的规范形式，去掉空行和完全相同的条目；nil 改为空列表。
func normalizeAccessGroups(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		if domain, service, err := ParseAccessGroup(entry); err == nil {
			entry = domain + "=" + service
		}
		if !slices.Contains(out, entry) {
			out = append(out, entry)
		}
	}
	return out
}

func checkAccessGroups(issues *Issues, path string, entries []string) {
	for index, entry := range entries {
		if _, _, err := ParseAccessGroup(entry); err != nil {
			issues.add(path, "entry %d (%q): %s", index+1, entry, err.Error())
		}
	}
}
