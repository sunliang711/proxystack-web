package app

import (
	"fmt"
	"sync"
	"time"
)

// 按来源 IP 的登录限速参数。账号维度的锁定见 maxLoginFailures；IP 维度覆盖不存在的用户名、
// 错误的两步验证码和重置链接，阈值高于账号维度，避免同一出口下的正常使用被误伤。
const (
	ipMaxFailures   = 10
	ipFailureWindow = 15 * time.Minute
	ipBlockDuration = 15 * time.Minute
	// ipGuardMaxEntries 超过后清理过期记录，限制内存占用。
	ipGuardMaxEntries = 10000
)

// TooManyAttemptsError 表示来源 IP 失败次数过多，暂时拒绝认证请求。
type TooManyAttemptsError struct {
	RetryAfter time.Duration
}

func (e *TooManyAttemptsError) Error() string {
	return fmt.Sprintf("too many failed attempts from this address; try again in %d seconds", int(e.RetryAfter.Seconds()))
}

// loginGuard 在内存中按来源 IP 统计认证失败。管理服务重启后计数清零，账号锁定仍保存在数据库中。
type loginGuard struct {
	mu      sync.Mutex
	entries map[string]*guardEntry
}

type guardEntry struct {
	windowStart  time.Time
	failures     int
	blockedUntil time.Time
}

// check 返回来源 IP 是否处于封禁期。
func (g *loginGuard) check(ip string, now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry := g.entries[ip]
	if entry != nil && now.Before(entry.blockedUntil) {
		return &TooManyAttemptsError{RetryAfter: entry.blockedUntil.Sub(now).Round(time.Second)}
	}
	return nil
}

// fail 记录一次失败；窗口内达到阈值时开始封禁。
func (g *loginGuard) fail(ip string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.entries == nil {
		g.entries = map[string]*guardEntry{}
	}
	entry := g.entries[ip]
	if entry == nil || now.Sub(entry.windowStart) >= ipFailureWindow {
		if len(g.entries) >= ipGuardMaxEntries {
			g.prune(now)
		}
		entry = &guardEntry{windowStart: now}
		g.entries[ip] = entry
	}
	entry.failures++
	if entry.failures >= ipMaxFailures {
		entry.blockedUntil = now.Add(ipBlockDuration)
		entry.windowStart, entry.failures = now, 0
	}
}

// succeed 在认证成功后清除该 IP 的失败计数。
func (g *loginGuard) succeed(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.entries, ip)
}

func (g *loginGuard) prune(now time.Time) {
	for ip, entry := range g.entries {
		if now.Sub(entry.windowStart) >= ipFailureWindow && !now.Before(entry.blockedUntil) {
			delete(g.entries, ip)
		}
	}
}
