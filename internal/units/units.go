// Package units 管理 psw-xray@ / psw-mihomo@ 模板 unit 的启停、状态和日志。
//
// Linux 上通过 systemd D-Bus 操作 unit、用 journalctl 读取日志；其他平台和测试
// 使用内存实现，便于在开发机上演示和验证上层流程。
package units

import (
	"context"
	"time"
)

// 组件名。
const (
	Xray   = "xray"
	Mihomo = "mihomo"
)

// Name 返回某个 stack 组件的 unit 名。
func Name(component, stack string) string {
	return "psw-" + component + "@" + stack + ".service"
}

// State 是 unit 的运行状态。
type State struct {
	// Active 为 systemd 的 ActiveState：active / inactive / failed / activating / deactivating。
	Active string
	Sub    string
	// Since 是进入当前 ActiveState 的时间。
	Since time.Time
}

// Running 判断 unit 是否处于运行或正在启动。
func (s State) Running() bool {
	return s.Active == "active" || s.Active == "activating" || s.Active == "reloading"
}

// Manager 启停 unit 并查询状态。
type Manager interface {
	Start(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Restart(ctx context.Context, unit string) error
	States(ctx context.Context, units []string) (map[string]State, error)
	Close()
}

// LogEntry 是一行 unit 日志。
type LogEntry struct {
	Time     time.Time `json:"time"`
	Unit     string    `json:"unit"`
	Priority int       `json:"priority"`
	Message  string    `json:"message"`
	// Cursor 是该条日志在 journald 中的位置，从它之后继续读取不会重复也不会遗漏。
	Cursor string `json:"-"`
}

// Logs 读取 unit 日志。follow 为 true 时持续推送新日志，直到 ctx 结束。
type Logs interface {
	Read(ctx context.Context, units []string, lines int, follow bool, emit func(LogEntry)) error
	// Follow 从 after 之后持续读取日志，直到 ctx 结束；after 为空时只读取之后新写入的日志。
	Follow(ctx context.Context, units []string, after string, emit func(LogEntry)) error
}

// LogRateLimits 判断 journald 是否会对 unit 的日志限速：结合 unit 自身的 LogRateLimitIntervalSec / LogRateLimitBurst
// 和 journald 的全局设置，突发上限低于 RateLimitBurstEnough 时为 true。只有 systemd 实现提供；查询失败的 unit 不出现在结果中。
type LogRateLimits interface {
	LogRateLimited(ctx context.Context, units []string) map[string]bool
}

// RateLimitBurstEnough 是认为 journald 限速已放开的突发上限：访问日志每条连接一行，低于它时连接多的时候会被丢弃。
const RateLimitBurstEnough = 100000
