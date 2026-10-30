package units

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Fake 是内存实现：只记录状态，不启动进程。FailStart 中的 unit 启动时报错并进入 failed。
type Fake struct {
	mu        sync.Mutex
	states    map[string]State
	logs      []LogEntry
	FailStart map[string]bool
	Calls     []string
}

// NewFake 创建内存实现。
func NewFake() *Fake {
	return &Fake{states: map[string]State{}, FailStart: map[string]bool{}}
}

func (f *Fake) Close() {}

func (f *Fake) set(unit, active, message string) {
	f.states[unit] = State{Active: active, Sub: map[string]string{"active": "running", "inactive": "dead", "failed": "failed"}[active], Since: time.Now()}
	f.logs = append(f.logs, LogEntry{Time: time.Now(), Unit: unit, Priority: 6, Message: message})
}

func (f *Fake) Start(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "start "+unit)
	if f.FailStart[unit] {
		f.set(unit, "failed", "Failed to start "+unit+" (simulated)")
		return fmt.Errorf("start %s: job failed", unit)
	}
	f.set(unit, "active", "Started "+unit+" (simulated)")
	return nil
}

func (f *Fake) Stop(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "stop "+unit)
	f.set(unit, "inactive", "Stopped "+unit+" (simulated)")
	return nil
}

func (f *Fake) Restart(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "restart "+unit)
	if f.FailStart[unit] {
		f.set(unit, "failed", "Failed to restart "+unit+" (simulated)")
		return fmt.Errorf("restart %s: job failed", unit)
	}
	f.set(unit, "active", "Restarted "+unit+" (simulated)")
	return nil
}

func (f *Fake) States(_ context.Context, unitNames []string) (map[string]State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]State, len(unitNames))
	for _, unit := range unitNames {
		state, ok := f.states[unit]
		if !ok {
			state = State{Active: "inactive", Sub: "dead"}
		}
		out[unit] = state
	}
	return out, nil
}

// Read 输出记录过的启停事件；follow 时每秒检查一次新事件。
func (f *Fake) Read(ctx context.Context, unitNames []string, lines int, follow bool, emit func(LogEntry)) error {
	wanted := map[string]bool{}
	for _, unit := range unitNames {
		wanted[unit] = true
	}
	sent := 0
	flush := func(limit int) {
		f.mu.Lock()
		matched := []LogEntry{}
		for _, entry := range f.logs[sent:] {
			if wanted[entry.Unit] {
				matched = append(matched, entry)
			}
		}
		sent = len(f.logs)
		f.mu.Unlock()
		if limit > 0 && len(matched) > limit {
			matched = matched[len(matched)-limit:]
		}
		for _, entry := range matched {
			emit(entry)
		}
	}
	flush(lines)
	if !follow {
		return nil
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			flush(0)
		}
	}
}
