package configdoc

import (
	"encoding/json"
	"fmt"
	"sort"
)

// DiffEntry 是两个版本之间一个实体的变化。
type DiffEntry struct {
	Kind   string        `json:"kind" enum:"settings,user,stack"`
	Name   string        `json:"name"`
	Change string        `json:"change" enum:"added,removed,modified"`
	Fields []FieldChange `json:"fields"`
}

// FieldChange 是一个字段的旧值和新值（JSON 文本），凭据字段已遮蔽。
type FieldChange struct {
	Path string `json:"path"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
}

// Diff 按实体比较两个版本：用户和 stack 按 ID 对应（快照中带 ID），改名显示为 name 字段的修改。
func Diff(from, to *Document) []DiffEntry {
	from, to = from.Clone(), to.Clone()
	MaskSecrets(from)
	MaskSecrets(to)
	entries := []DiffEntry{}
	if from.Settings != nil || to.Settings != nil {
		if fields := diffValues(from.Settings, to.Settings); len(fields) > 0 {
			entries = append(entries, DiffEntry{Kind: "settings", Name: "settings", Change: "modified", Fields: fields})
		}
	}
	entries = append(entries, diffEntities("user", len(from.Users), len(to.Users),
		func(i int) (int64, string, any) {
			return from.Users[i].ID, from.Users[i].Name, withoutUserIDs(from.Users[i])
		},
		func(i int) (int64, string, any) { return to.Users[i].ID, to.Users[i].Name, withoutUserIDs(to.Users[i]) })...)
	entries = append(entries, diffEntities("stack", len(from.Stacks), len(to.Stacks),
		func(i int) (int64, string, any) {
			return from.Stacks[i].ID, from.Stacks[i].Name, withoutStackIDs(from.Stacks[i])
		},
		func(i int) (int64, string, any) {
			return to.Stacks[i].ID, to.Stacks[i].Name, withoutStackIDs(to.Stacks[i])
		})...)
	return entries
}

func withoutUserIDs(user User) User {
	clearUserIDs(&user)
	return user
}

func withoutStackIDs(stack Stack) Stack {
	clearStackIDs(&stack)
	return stack
}

func diffEntities(kind string, fromLen, toLen int, from, to func(int) (int64, string, any)) []DiffEntry {
	entries := []DiffEntry{}
	matched := map[int]bool{}
	for i := 0; i < fromLen; i++ {
		id, name, value := from(i)
		found := -1
		for j := 0; j < toLen; j++ {
			otherID, otherName, _ := to(j)
			if matched[j] {
				continue
			}
			if (id != 0 && otherID == id) || (id == 0 && otherName == name) {
				found = j
				break
			}
		}
		if found < 0 {
			entries = append(entries, DiffEntry{Kind: kind, Name: name, Change: "removed", Fields: []FieldChange{}})
			continue
		}
		matched[found] = true
		_, newName, newValue := to(found)
		if fields := diffValues(value, newValue); len(fields) > 0 {
			entries = append(entries, DiffEntry{Kind: kind, Name: newName, Change: "modified", Fields: fields})
		}
	}
	for j := 0; j < toLen; j++ {
		if !matched[j] {
			_, name, _ := to(j)
			entries = append(entries, DiffEntry{Kind: kind, Name: name, Change: "added", Fields: []FieldChange{}})
		}
	}
	return entries
}

func diffValues(from, to any) []FieldChange {
	left, right := map[string]string{}, map[string]string{}
	flatten("", toGeneric(from), left)
	flatten("", toGeneric(to), right)
	paths := map[string]bool{}
	for path := range left {
		paths[path] = true
	}
	for path := range right {
		paths[path] = true
	}
	sorted := make([]string, 0, len(paths))
	for path := range paths {
		sorted = append(sorted, path)
	}
	sort.Strings(sorted)
	changes := []FieldChange{}
	for _, path := range sorted {
		if left[path] != right[path] {
			changes = append(changes, FieldChange{Path: path, Old: left[path], New: right[path]})
		}
	}
	return changes
}

func toGeneric(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}

func flatten(prefix string, value any, out map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 && prefix != "" {
			out[prefix] = "{}"
		}
		for key, child := range typed {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			flatten(path, child, out)
		}
	case []any:
		if len(typed) == 0 {
			out[prefix] = "[]"
		}
		for index, child := range typed {
			flatten(fmt.Sprintf("%s[%d]", prefix, index), child, out)
		}
	case nil:
		if prefix != "" {
			out[prefix] = "null"
		}
	default:
		data, _ := json.Marshal(typed)
		out[prefix] = string(data)
	}
}
