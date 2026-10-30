// Package events 是进程内事件总线：unit 状态、后台作业进度等事件由此推送给 SSE 订阅者。
package events

import "sync"

// Event 是一条事件。Type 对应 SSE 的 event 名。
type Event struct {
	Type string
	Data any
}

// Bus 把事件广播给全部订阅者。订阅者处理过慢时丢弃该订阅者的事件，不阻塞发布方。
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// New 创建事件总线。
func New() *Bus {
	return &Bus{subs: map[chan Event]struct{}{}}
}

// Subscribe 订阅事件，返回事件通道和取消函数。
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Publish 发布事件。
func (b *Bus) Publish(event Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- event:
		default:
		}
	}
}
