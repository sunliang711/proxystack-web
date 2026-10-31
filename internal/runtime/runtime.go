// Package runtime 负责生成物：从配置文档渲染每个 stack 的 xray/mihomo 配置文件，
// 与磁盘上的文件比较得出待应用变更，并以原子方式写盘。
package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/domain"
	"proxystack-web/internal/generator/mihomo"
	"proxystack-web/internal/generator/xray"
	"proxystack-web/internal/graph"
	"proxystack-web/internal/paths"
	"proxystack-web/internal/units"
)

// Files 是一个 stack 的生成物；某个组件未启用时对应字段为 nil。
type Files struct {
	Xray   []byte
	Mihomo []byte
}

// Get 返回指定组件的内容。
func (f Files) Get(component string) []byte {
	if component == units.Xray {
		return f.Xray
	}
	return f.Mihomo
}

// Components 是生成物涉及的两个组件，mihomo 在前：xray 出站依赖本 stack 的 mihomo。
var Components = []string{units.Mihomo, units.Xray}

// Rendered 是一次渲染的结果。
type Rendered struct {
	Files map[string]Files
	// Order 是按引用依赖排好的服务启动顺序（被依赖的在前）。
	Order []graph.ServiceNode
	// Set 是渲染所用的领域模型，供热增删用户时取入站定义。
	Set domain.StackSet
}

// Render 渲染文档中全部启用 stack 的生成物。extra 是配置之外的暂停集合（自动暂停）。
func Render(doc *configdoc.Document, extra domain.DisabledUserSet) (*Rendered, error) {
	set, err := configdoc.Compile(doc)
	if err != nil {
		return nil, err
	}
	set, err = domain.ResolveStackSetUserRefs(set)
	if err != nil {
		return nil, err
	}
	disabled := configdoc.DisabledUsers(doc)
	for stack, users := range extra {
		for user := range users {
			disabled.Add(stack, user)
		}
	}
	set.DisabledUsers = disabled
	out := &Rendered{Files: map[string]Files{}, Set: set}
	for _, stack := range set.Stacks {
		if !stack.Enabled {
			continue
		}
		var files Files
		if stack.Xray.Enabled {
			text, err := xray.DumpsConfig(set, stack.Name)
			if err != nil {
				return nil, fmt.Errorf("stack %s xray: %w", stack.Name, err)
			}
			files.Xray = []byte(text)
		}
		if stack.Clash.Enabled {
			text, err := mihomo.DumpsConfig(set, stack.Name)
			if err != nil {
				return nil, fmt.Errorf("stack %s mihomo: %w", stack.Name, err)
			}
			files.Mihomo = []byte(text)
		}
		out.Files[stack.Name] = files
	}
	refGraph, err := graph.BuildReferenceGraph(set)
	if err != nil {
		return nil, err
	}
	out.Order = refGraph.TopologicalOrder(nil)
	return out, nil
}

// Disk 读写 runtime/generated 下的生成物。
type Disk struct {
	Layout paths.Layout
}

func (d Disk) path(component, stack string) string {
	if component == units.Xray {
		return d.Layout.GeneratedXray(stack)
	}
	return d.Layout.GeneratedMihomo(stack)
}

// Read 读取 stack 当前在磁盘上的生成物，文件不存在时对应字段为 nil。
func (d Disk) Read(stack string) (Files, error) {
	var files Files
	for _, component := range Components {
		data, err := os.ReadFile(d.path(component, stack))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Files{}, err
		}
		if component == units.Xray {
			files.Xray = data
		} else {
			files.Mihomo = data
		}
	}
	return files, nil
}

// Write 原子写入生成物；字段为 nil 的组件删除对应文件。写入 mihomo 配置时一并创建它的工作目录，
// psw-mihomo@ unit 把该目录列为可写路径，目录不存在时 unit 无法启动。
func (d Disk) Write(stack string, files Files) error {
	if files.Mihomo != nil {
		if err := os.MkdirAll(d.Layout.MihomoWorkDir(stack), 0o750); err != nil {
			return err
		}
	}
	for _, component := range Components {
		path := d.path(component, stack)
		content := files.Get(component)
		if content == nil {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if err := writeFileAtomic(path, content, 0o640); err != nil {
			return err
		}
	}
	return nil
}

// Stacks 列出磁盘上存在生成物的 stack 名。
func (d Disk) Stacks() ([]string, error) {
	names := map[string]bool{}
	for _, dir := range []struct{ dir, ext string }{
		{filepath.Dir(d.Layout.GeneratedXray("x")), ".json"},
		{filepath.Dir(d.Layout.GeneratedMihomo("x")), ".yaml"},
	} {
		entries, err := os.ReadDir(dir.dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), dir.ext) && !strings.HasPrefix(entry.Name(), ".") {
				names[strings.TrimSuffix(entry.Name(), dir.ext)] = true
			}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// 变更类型。
const (
	ChangeCreate = "create"
	ChangeUpdate = "update"
	ChangeDelete = "delete"
)

// Change 是一个生成物文件的变化。
type Change struct {
	Stack     string `json:"stack"`
	Component string `json:"component" enum:"xray,mihomo"`
	Kind      string `json:"kind" enum:"create,update,delete"`
	Diff      string `json:"diff"`
}

// Diff 比较期望生成物与当前生成物，返回按 stack、组件排序的变化。
func Diff(desired, current map[string]Files) []Change {
	stacks := map[string]bool{}
	for name := range desired {
		stacks[name] = true
	}
	for name := range current {
		stacks[name] = true
	}
	names := make([]string, 0, len(stacks))
	for name := range stacks {
		names = append(names, name)
	}
	sort.Strings(names)
	changes := []Change{}
	for _, name := range names {
		for _, component := range []string{units.Xray, units.Mihomo} {
			want, have := desired[name].Get(component), current[name].Get(component)
			change := Change{Stack: name, Component: component}
			switch {
			case want == nil && have == nil:
				continue
			case have == nil:
				change.Kind = ChangeCreate
			case want == nil:
				change.Kind = ChangeDelete
			case string(want) == string(have):
				continue
			default:
				change.Kind = ChangeUpdate
			}
			change.Diff, _ = difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
				A: difflib.SplitLines(string(have)), B: difflib.SplitLines(string(want)),
				FromFile: "当前", ToFile: "应用后", Context: 3,
			})
			changes = append(changes, change)
		}
	}
	return changes
}

// Hash 返回内容的 sha256，内容为 nil 时返回空字符串。
func Hash(content []byte) string {
	if content == nil {
		return ""
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
