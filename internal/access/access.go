// Package access 解析 xray 访问日志，把每条已接受的连接归并成「用户 + 主域名」。
package access

import (
	"net"
	"net/netip"
	"strings"

	"golang.org/x/net/publicsuffix"

	"proxystack-web/internal/domain"
)

// maxHostLength 是域名的最大长度，超出的记录视为无效。
const maxHostLength = 253

// Record 是一条访问日志解析出的用户和目标主域名。
type Record struct {
	UserID int64
	Domain string
}

// ParseLine 解析 xray 访问日志的一行，形如
//
//	2026/11/11 12:00:00.123456 from 1.2.3.4:5678 accepted tcp:www.google.com:443 [in -> out] email: alice.u7
//
// 只接受带用户标识、目标是域名的 accepted 记录；relay 入站、共享账号等没有用户标识的记录，以及目标是 IP 地址的记录
// （直连 IP、UDP 和 P2P 的对端地址，数量多且不对应网站）返回 false。
func ParseLine(message string) (Record, bool) {
	index := strings.Index(message, " accepted ")
	if index < 0 {
		return Record{}, false
	}
	rest := message[index+len(" accepted "):]
	marker := strings.LastIndex(rest, " email: ")
	if marker < 0 {
		return Record{}, false
	}
	email := strings.TrimSpace(rest[marker+len(" email: "):])
	userID, _, ok := domain.ParseUserStatsEmail(email)
	if !ok || userID <= 0 {
		return Record{}, false
	}
	target, _, _ := strings.Cut(strings.TrimSpace(rest[:marker]), " ")
	host, ok := targetHost(target)
	if !ok {
		return Record{}, false
	}
	name := Domain(host)
	if name == "" {
		return Record{}, false
	}
	return Record{UserID: userID, Domain: name}, true
}

// targetHost 从 tcp:host:port、udp:[v6]:port 或 host:port 中取出主机部分。
func targetHost(target string) (string, bool) {
	if network, rest, ok := strings.Cut(target, ":"); ok && (network == "tcp" || network == "udp") {
		target = rest
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		return "", false
	}
	return host, true
}

// Domain 把主机名归并为主域名（可注册的那一级，例如 www.example.co.uk 归为 example.co.uk）。
// 主域名按公共后缀列表计算；无法归并的主机名（单段名称、本身就是公共后缀）返回主机名本身；IP 地址和无效主机名返回空串。
func Domain(host string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || len(host) > maxHostLength {
		return ""
	}
	if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return ""
	}
	if strings.ContainsAny(host, " /\\\t") {
		return ""
	}
	if registrable, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return registrable
	}
	return host
}
