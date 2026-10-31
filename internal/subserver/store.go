package subserver

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"proxystack-web/internal/subscription"
)

// ErrStale 表示订阅包不比已导入的新：序号小于或等于当前序号。
var ErrStale = errors.New("bundle is not newer than the imported one")

// ImportResult 是一次导入的结果。
type ImportResult struct {
	Snapshot *subscription.Snapshot
	// Previous 是被替换的序号，首次导入为 0。
	Previous int64
}

// Import 验签并校验订阅包，通过后原子替换该来源的当前订阅包。
// 序号不大于当前序号时拒绝（force 为真时允许，用于管理端重装后序号重新开始）。
func Import(layout Layout, config Config, data []byte, force bool) (*ImportResult, error) {
	snapshot, err := subscription.Open(data, config.TrustedKey)
	if err != nil {
		return nil, err
	}
	result := &ImportResult{Snapshot: snapshot}
	if current, err := readBundle(layout, config, snapshot.Source); err == nil {
		result.Previous = current.Sequence
		if snapshot.Sequence <= current.Sequence && !force {
			return nil, fmt.Errorf("%w: source %s has sequence %d, the bundle has %d", ErrStale, snapshot.Source, current.Sequence, snapshot.Sequence)
		}
	}
	if err := os.MkdirAll(layout.DataDir(), 0o750); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(layout.BundlePath(snapshot.Source), data, 0o640); err != nil {
		return nil, err
	}
	return result, nil
}

func readBundle(layout Layout, config Config, source string) (*subscription.Snapshot, error) {
	data, err := os.ReadFile(layout.BundlePath(source))
	if err != nil {
		return nil, err
	}
	return subscription.Open(data, config.TrustedKey)
}

// SourceState 是一个来源当前导入的数据。
type SourceState struct {
	Name     string
	Primary  bool
	Snapshot *subscription.Snapshot
	// Error 是读取或验签失败的原因；未导入时为空且 Snapshot 为 nil。
	Error string
}

// LoadSources 按配置顺序读取每个来源已导入的订阅包。
func LoadSources(layout Layout, config Config) []SourceState {
	states := make([]SourceState, 0, len(config.Sources))
	for _, source := range config.Sources {
		state := SourceState{Name: source.Name, Primary: source.Primary}
		snapshot, err := readBundle(layout, config, source.Name)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			state.Error = err.Error()
		default:
			state.Snapshot = snapshot
		}
		states = append(states, state)
	}
	sort.SliceStable(states, func(i, j int) bool { return states[i].Primary && !states[j].Primary })
	return states
}

// BuildView 用主来源和其他来源的数据合并出可提供订阅的数据；主来源没有可用数据时返回错误。
func BuildView(states []SourceState) (*subscription.View, error) {
	var primary *subscription.Snapshot
	var extras []*subscription.Snapshot
	for _, state := range states {
		if state.Snapshot == nil {
			continue
		}
		if state.Primary {
			primary = state.Snapshot
		} else {
			extras = append(extras, state.Snapshot)
		}
	}
	if primary == nil {
		return nil, errors.New("the primary source has no imported bundle")
	}
	return subscription.Merge(primary, extras)
}
