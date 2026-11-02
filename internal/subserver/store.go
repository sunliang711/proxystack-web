package subserver

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"

	"proxystack-web/internal/subscription"
)

// ErrStale 表示订阅包比已导入的序号旧。
var ErrStale = errors.New("bundle is not newer than the imported one")

// ErrSequenceConflict 表示相同序号对应的订阅包内容不同。
var ErrSequenceConflict = errors.New("bundle sequence conflicts with the imported one")

// ErrSourceMismatch 表示订阅包来源与发布 Token 绑定的来源不同。
var ErrSourceMismatch = errors.New("bundle source does not match the authenticated source")

// ErrInvalidBundle 表示订阅包格式、签名或内容校验失败。
var ErrInvalidBundle = errors.New("subscription bundle is invalid")

// ImportResult 是一次导入的结果。
type ImportResult struct {
	Snapshot *subscription.Snapshot
	// Previous 是被替换的序号，首次导入为 0。
	Previous int64
	// Already 表示相同的原始订阅包已经导入，无需再次写盘。
	Already bool
}

// Import 验签并校验订阅包，通过后原子替换该来源的当前订阅包。
// 更旧序号或相同序号的不同内容会被拒绝（force 为真时允许，用于管理端重装后序号重新开始）。
func Import(layout Layout, config Config, data []byte, force bool) (*ImportResult, error) {
	return importBundle(layout, config, data, force, "")
}

// ImportForSource 导入在线发布的订阅包，并要求包内来源与发布 Token 绑定的来源一致。
func ImportForSource(layout Layout, config Config, data []byte, expectedSource string) (*ImportResult, error) {
	return importBundle(layout, config, data, false, expectedSource)
}

func importBundle(layout Layout, config Config, data []byte, force bool, expectedSource string) (*ImportResult, error) {
	snapshot, err := subscription.Open(data, config.TrustedKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBundle, err)
	}
	if expectedSource != "" && snapshot.Source != expectedSource {
		return nil, fmt.Errorf("%w: got %s", ErrSourceMismatch, snapshot.Source)
	}
	if err := os.MkdirAll(layout.DataDir(), 0o750); err != nil {
		return nil, err
	}
	unlock, err := acquireImportLock(layout)
	if err != nil {
		return nil, err
	}
	defer unlock()

	result := &ImportResult{Snapshot: snapshot}
	currentData, readErr := os.ReadFile(layout.BundlePath(snapshot.Source))
	if readErr == nil {
		current, openErr := subscription.Open(currentData, config.TrustedKey)
		if openErr != nil {
			return nil, openErr
		}
		result.Previous = current.Sequence
		if !force && snapshot.Sequence == current.Sequence {
			if bytes.Equal(data, currentData) {
				result.Already = true
				return result, nil
			}
			return nil, fmt.Errorf("%w: source %s has sequence %d", ErrSequenceConflict, snapshot.Source, current.Sequence)
		}
		if snapshot.Sequence < current.Sequence && !force {
			return nil, fmt.Errorf("%w: source %s has sequence %d, the bundle has %d", ErrStale, snapshot.Source, current.Sequence, snapshot.Sequence)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, readErr
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
