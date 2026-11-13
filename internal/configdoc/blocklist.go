package configdoc

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// BlockFullPrefix 标记只拦截域名本身、不含子域名的条目；不带前缀的条目拦截该域名及其全部子域名。
const BlockFullPrefix = "full:"

// blockDomainPrefix 是 xray 的子域名匹配前缀，输入时可以带上，保存时去掉。
const blockDomainPrefix = "domain:"

// NormalizeBlockedDomain 把一条拦截条目改写为规范形式：小写、去掉首尾空白和末尾的点、去掉多余的 domain: 前缀。
// 国际化域名需要写成 punycode（xn--）形式。无法识别的条目原样返回 false，由校验报告错误。
func NormalizeBlockedDomain(entry string) (string, bool) {
	text := strings.TrimSpace(entry)
	prefix := ""
	lower := strings.ToLower(text)
	switch {
	case strings.HasPrefix(lower, BlockFullPrefix):
		prefix, text = BlockFullPrefix, text[len(BlockFullPrefix):]
	case strings.HasPrefix(lower, blockDomainPrefix):
		text = text[len(blockDomainPrefix):]
	}
	host, err := blockedHost(text)
	if err != nil {
		return entry, false
	}
	return prefix + host, true
}

// blockedHost 校验并规范化条目中的域名部分。
func blockedHost(text string) (string, error) {
	text = strings.TrimSuffix(strings.TrimSpace(text), ".")
	if text == "" {
		return "", fmt.Errorf("must be a domain name")
	}
	if _, err := netip.ParseAddr(strings.Trim(text, "[]")); err == nil {
		return "", fmt.Errorf("must be a domain name, not an IP address")
	}
	host := strings.ToLower(text)
	if len(host) > 253 {
		return "", fmt.Errorf("must be a domain name")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("must be a domain name")
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return "", fmt.Errorf("must be a domain name")
			}
		}
	}
	return host, nil
}

// normalizeBlockedDomains 规范化并去重拦截条目，保持首次出现的顺序；nil 改为空列表。
func normalizeBlockedDomains(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		normalized, _ := NormalizeBlockedDomain(entry)
		if !slices.Contains(out, normalized) {
			out = append(out, normalized)
		}
	}
	return out
}

// checkBlockedDomains 把错误记在列表本身的路径上，表单在整个名单编辑器下方显示。
func checkBlockedDomains(issues *Issues, path string, entries []string) {
	for index, entry := range entries {
		if _, ok := NormalizeBlockedDomain(entry); !ok {
			issues.add(path, "entry %d (%q): must be a domain name such as example.com or full:www.example.com (internationalized names in xn-- form)", index+1, entry)
		}
	}
}

// XrayDomainRule 把规范化后的拦截条目翻译成 xray 路由的域名匹配写法。
func XrayDomainRule(entry string) string {
	if strings.HasPrefix(entry, BlockFullPrefix) {
		return entry
	}
	return blockDomainPrefix + entry
}

// BlockedDomainMatches 判断主机名是否命中规范化后的拦截条目。
func BlockedDomainMatches(entry, host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if full, ok := strings.CutPrefix(entry, BlockFullPrefix); ok {
		return host == full
	}
	return host == entry || strings.HasSuffix(host, "."+entry)
}

// JoinBlockedDomains 和 SplitBlockedDomains 在条目列表与数据库中逗号分隔的文本之间转换。
func JoinBlockedDomains(entries []string) string { return strings.Join(entries, ",") }

func SplitBlockedDomains(text string) []string {
	if text == "" {
		return []string{}
	}
	return strings.Split(text, ",")
}
