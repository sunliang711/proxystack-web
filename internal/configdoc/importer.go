package configdoc

import (
	"fmt"
	"slices"
	"strconv"
)

// 导入模式、同名冲突处理和端口处理选项。
const (
	ImportMerge   = "merge"
	ImportReplace = "replace"

	ConflictSkip      = "skip"
	ConflictOverwrite = "overwrite"
	ConflictRename    = "rename"

	PortsKeep             = "keep"
	PortsReallocConflicts = "realloc-conflicts"
	PortsReallocAll       = "realloc-all"
)

// 预览条目的动作。
const (
	ActionCreate    = "create"
	ActionOverwrite = "overwrite"
	ActionSkip      = "skip"
	ActionRename    = "rename"
	ActionExcluded  = "excluded"
)

// ImportOptions 是导入选项。
type ImportOptions struct {
	Mode     string
	Conflict string
	Ports    string
	// Exclude 是不导入的条目 key（settings、user:<name>、stack:<name>）。
	Exclude []string
	// ReservedPorts 是文档之外已占用的端口。
	ReservedPorts []int
}

// PortChange 是一处端口调整。
type PortChange struct {
	Field string `json:"field"`
	From  int    `json:"from"`
	To    int    `json:"to"`
}

// ImportEntry 是预览中的一个实体。
type ImportEntry struct {
	Key                string       `json:"key"`
	Kind               string       `json:"kind" enum:"settings,user,stack,manual_node,remote_source,templates"`
	Name               string       `json:"name"`
	Action             string       `json:"action" enum:"create,overwrite,skip,rename,excluded"`
	NewName            string       `json:"new_name,omitempty"`
	PortChanges        []PortChange `json:"port_changes"`
	RegeneratedSecrets bool         `json:"regenerated_secrets"`
}

// ImportPlan 是导入的计算结果：合并后的完整文档和逐条预览。
type ImportPlan struct {
	Result  *Document
	Entries []ImportEntry
}

// PlanImport 把 incoming 按选项合并进 current，返回合并结果（尚未校验）。incoming 必须已解密。
func PlanImport(current, incoming *Document, opts ImportOptions) (*ImportPlan, error) {
	if opts.Mode == ImportReplace && incoming.Scope != "" && incoming.Scope != "all" {
		return nil, fmt.Errorf("replace mode requires a full configuration file")
	}
	incoming = incoming.Clone()
	omitted := incoming.Secrets == SecretsOmitted
	excluded := func(key string) bool { return slices.Contains(opts.Exclude, key) }

	result := current.Clone()
	if opts.Mode == ImportReplace {
		result.Users = []User{}
		result.Stacks = []Stack{}
		result.ManualNodes = []ManualNode{}
		result.RemoteSources = []RemoteSource{}
	}
	plan := &ImportPlan{Result: result, Entries: []ImportEntry{}}

	if incoming.Settings != nil {
		entry := ImportEntry{Key: "settings", Kind: "settings", Name: "settings", Action: ActionOverwrite, PortChanges: []PortChange{}}
		if excluded(entry.Key) {
			entry.Action = ActionExcluded
		} else {
			settings := *incoming.Settings
			result.Settings = &settings
		}
		plan.Entries = append(plan.Entries, entry)
	}

	// 用户：先决定每个用户的去向，改名要同步到 incoming 内部的引用。
	type userMove struct {
		index  int
		action string
	}
	userMoves := []userMove{}
	for index := range incoming.Users {
		user := &incoming.Users[index]
		entry := ImportEntry{Key: "user:" + user.Name, Kind: "user", Name: user.Name, PortChanges: []PortChange{}}
		existing := result.UserByName(user.Name)
		switch {
		case excluded(entry.Key):
			entry.Action = ActionExcluded
		case existing < 0:
			entry.Action = ActionCreate
		case opts.Conflict == ConflictOverwrite:
			entry.Action = ActionOverwrite
		case opts.Conflict == ConflictRename:
			entry.Action = ActionRename
			newName := uniqueName(user.Name, func(name string) bool {
				return result.UserByName(name) >= 0 || incoming.UserByName(name) >= 0
			})
			entry.NewName = newName
			RenameUser(incoming, user.Name, newName)
			user.Name = newName
			// 改名导入的是另一个用户，订阅 token 不能与原用户共用。
			user.SubToken = RandomToken()
		default:
			entry.Action = ActionSkip
		}
		plan.Entries = append(plan.Entries, entry)
		userMoves = append(userMoves, userMove{index, entry.Action})
	}
	stackMoves := []userMove{}
	for index := range incoming.Stacks {
		stack := &incoming.Stacks[index]
		entry := ImportEntry{Key: "stack:" + stack.Name, Kind: "stack", Name: stack.Name, PortChanges: []PortChange{}}
		existing := result.StackByName(stack.Name)
		switch {
		case excluded(entry.Key):
			entry.Action = ActionExcluded
		case existing < 0:
			entry.Action = ActionCreate
		case opts.Conflict == ConflictOverwrite:
			entry.Action = ActionOverwrite
		case opts.Conflict == ConflictRename:
			entry.Action = ActionRename
			newName := uniqueName(stack.Name, func(name string) bool {
				return result.StackByName(name) >= 0 || incoming.StackByName(name) >= 0
			})
			entry.NewName = newName
			RenameStack(incoming, stack.Name, newName)
			RenameStackRemarks(stack, stack.Name, newName)
			stack.Name = newName
		default:
			entry.Action = ActionSkip
		}
		plan.Entries = append(plan.Entries, entry)
		stackMoves = append(stackMoves, userMove{index, entry.Action})
	}

	// 写入用户。
	for _, move := range userMoves {
		if move.action == ActionSkip || move.action == ActionExcluded {
			continue
		}
		user := incoming.Users[move.index]
		clearUserIDs(&user)
		target := result.UserByName(user.Name)
		if target >= 0 {
			fillMissingUserSecrets(&user, &result.Users[target])
			user.ID = result.Users[target].ID
			result.Users[target] = user
			continue
		}
		if opts.Mode == ImportReplace {
			// 替换模式下同名实体沿用原 ID，停用状态、流量等运行数据保持关联。
			if index := current.UserByName(user.Name); index >= 0 {
				user.ID = current.Users[index].ID
				fillMissingUserSecrets(&user, &current.Users[index])
			}
		}
		result.Users = append(result.Users, user)
	}
	// 写入 stack，记录端口调整。stack 条目排在预览列表末尾。
	for position, move := range stackMoves {
		if move.action == ActionSkip || move.action == ActionExcluded {
			continue
		}
		stack := incoming.Stacks[move.index]
		clearStackIDs(&stack)
		target := result.StackByName(stack.Name)
		if target >= 0 {
			fillMissingStackSecrets(&stack, &result.Stacks[target])
			stack.ID = result.Stacks[target].ID
			result.Stacks[target] = stack
		} else {
			if opts.Mode == ImportReplace {
				if index := current.StackByName(stack.Name); index >= 0 {
					stack.ID = current.Stacks[index].ID
					fillMissingStackSecrets(&stack, &current.Stacks[index])
				}
			}
			result.Stacks = append(result.Stacks, stack)
			target = len(result.Stacks) - 1
		}
		entry := &plan.Entries[len(plan.Entries)-len(stackMoves)+position]
		changes, err := reallocatePorts(result, &result.Stacks[target], opts)
		if err != nil {
			return nil, err
		}
		entry.PortChanges = changes
	}

	// 手工节点：同一用户下同名（remark）的节点按冲突选项处理；远端节点源按名字；模板整体覆盖。
	for _, node := range incoming.ManualNodes {
		node.ID = 0
		key := "manual_node:" + node.User + "/" + node.Remark
		entry := ImportEntry{Key: key, Kind: "manual_node", Name: node.User + " / " + node.Remark, PortChanges: []PortChange{}}
		existing := slices.IndexFunc(result.ManualNodes, func(n ManualNode) bool { return n.User == node.User && n.Remark == node.Remark })
		switch {
		case excluded(key):
			entry.Action = ActionExcluded
		case existing < 0:
			entry.Action = ActionCreate
			result.ManualNodes = append(result.ManualNodes, node)
		case opts.Conflict == ConflictOverwrite:
			entry.Action = ActionOverwrite
			node.ID = result.ManualNodes[existing].ID
			result.ManualNodes[existing] = node
		case opts.Conflict == ConflictRename:
			entry.Action = ActionRename
			node.Remark = uniqueName(node.Remark, func(name string) bool {
				return slices.ContainsFunc(result.ManualNodes, func(n ManualNode) bool { return n.User == node.User && n.Remark == name })
			})
			node.Tag = ""
			entry.NewName = node.User + " / " + node.Remark
			result.ManualNodes = append(result.ManualNodes, node)
		default:
			entry.Action = ActionSkip
		}
		plan.Entries = append(plan.Entries, entry)
	}
	for _, source := range incoming.RemoteSources {
		source.ID = 0
		key := "remote_source:" + source.Name
		entry := ImportEntry{Key: key, Kind: "remote_source", Name: source.Name, PortChanges: []PortChange{}}
		existing := slices.IndexFunc(result.RemoteSources, func(r RemoteSource) bool { return r.Name == source.Name })
		switch {
		case excluded(key):
			entry.Action = ActionExcluded
		case existing < 0:
			entry.Action = ActionCreate
			result.RemoteSources = append(result.RemoteSources, source)
		case opts.Conflict == ConflictOverwrite:
			entry.Action = ActionOverwrite
			source.ID = result.RemoteSources[existing].ID
			result.RemoteSources[existing] = source
		case opts.Conflict == ConflictRename:
			entry.Action = ActionRename
			source.Name = uniqueName(source.Name, func(name string) bool {
				return slices.ContainsFunc(result.RemoteSources, func(r RemoteSource) bool { return r.Name == name })
			})
			entry.NewName = source.Name
			result.RemoteSources = append(result.RemoteSources, source)
		default:
			entry.Action = ActionSkip
		}
		plan.Entries = append(plan.Entries, entry)
	}
	if incoming.Templates != nil && *incoming.Templates != (Templates{}) {
		entry := ImportEntry{Key: "templates", Kind: "templates", Name: "templates", Action: ActionOverwrite, PortChanges: []PortChange{}}
		if excluded(entry.Key) {
			entry.Action = ActionExcluded
		} else {
			templates := *incoming.Templates
			result.Templates = &templates
		}
		plan.Entries = append(plan.Entries, entry)
	}

	if omitted {
		// 覆盖的实体已用本机凭据补齐，剩下的空凭据只属于新建的实体。
		RegenerateMissingSecrets(result)
		for index := range plan.Entries {
			entry := &plan.Entries[index]
			if entry.Kind != "settings" && (entry.Action == ActionCreate || entry.Action == ActionRename) {
				entry.RegeneratedSecrets = true
			}
		}
	}
	// 导入的用户可能手动暂停了本实例不存在的 stack，这些条目没有意义，直接去掉。
	for index := range result.Users {
		result.Users[index].PausedStacks = slices.DeleteFunc(result.Users[index].PausedStacks, func(name string) bool {
			return result.StackByName(name) < 0
		})
	}
	result.Normalize()
	return plan, nil
}

// reallocatePorts 按端口选项调整 stack 的端口，返回调整明细。
func reallocatePorts(doc *Document, stack *Stack, opts ImportOptions) ([]PortChange, error) {
	settings := DefaultSettings()
	if doc.Settings != nil {
		settings = *doc.Settings
	}
	changes := []PortChange{}
	if opts.Ports == PortsKeep || opts.Ports == "" {
		return changes, nil
	}
	before := stackPortFields(stack)
	if opts.Ports == PortsReallocAll {
		if err := AllocateStackPorts(doc, settings, stack, opts.ReservedPorts); err != nil {
			return nil, err
		}
	} else {
		used := UsedPorts(doc, stack, opts.ReservedPorts)
		for _, field := range stackPortFields(stack) {
			if !used[*field.port] {
				used[*field.port] = true
				continue
			}
			pool := settings.PortPools.XrayInbound
			switch field.pool {
			case "xray_api":
				pool = settings.PortPools.XrayAPI
			case "mihomo_socks":
				pool = settings.PortPools.MihomoSocks
			case "mihomo_http":
				pool = settings.PortPools.MihomoHTTP
			case "mihomo_controller":
				pool = settings.PortPools.MihomoController
			}
			port, err := allocateFrom(pool, used)
			if err != nil {
				return nil, fmt.Errorf("stack %s: %w", stack.Name, err)
			}
			*field.port = port
		}
	}
	for index, field := range stackPortFields(stack) {
		if before[index].value != *field.port {
			changes = append(changes, PortChange{Field: field.label, From: before[index].value, To: *field.port})
		}
	}
	return changes, nil
}

type portField struct {
	label string
	pool  string
	port  *int
	value int
}

func stackPortFields(stack *Stack) []portField {
	fields := []portField{}
	for index := range stack.Xray.Inbounds {
		inbound := &stack.Xray.Inbounds[index]
		fields = append(fields, portField{"xray.inbounds." + inbound.Name + ".port", "xray_inbound", &inbound.Port, inbound.Port})
	}
	fields = append(fields,
		portField{"xray.api_port", "xray_api", &stack.Xray.APIPort, stack.Xray.APIPort},
		portField{"mihomo.socks.port", "mihomo_socks", &stack.Mihomo.Socks.Port, stack.Mihomo.Socks.Port},
	)
	if stack.Mihomo.HTTP.Enabled {
		fields = append(fields, portField{"mihomo.http.port", "mihomo_http", &stack.Mihomo.HTTP.Port, stack.Mihomo.HTTP.Port})
	}
	fields = append(fields, portField{"mihomo.controller_port", "mihomo_controller", &stack.Mihomo.ControllerPort, stack.Mihomo.ControllerPort})
	return fields
}

func allocateFrom(pool PortRange, used map[int]bool) (int, error) {
	for port := pool.Start; port <= pool.End; port++ {
		if !used[port] {
			used[port] = true
			return port, nil
		}
	}
	return 0, fmt.Errorf("port pool %d-%d is exhausted", pool.Start, pool.End)
}

func uniqueName(base string, taken func(string) bool) string {
	for suffix := 2; ; suffix++ {
		candidate := base + "-" + strconv.Itoa(suffix)
		if !taken(candidate) {
			return candidate
		}
	}
}

func clearUserIDs(user *User) {
	user.ID = 0
	for index := range user.Profiles {
		user.Profiles[index].ID = 0
	}
}

func clearStackIDs(stack *Stack) {
	stack.ID = 0
	for index := range stack.Xray.Inbounds {
		stack.Xray.Inbounds[index].ID = 0
	}
	for index := range stack.Mihomo.Upstreams {
		stack.Mihomo.Upstreams[index].ID = 0
	}
	for index := range stack.Mihomo.Groups {
		stack.Mihomo.Groups[index].ID = 0
	}
}

// fillMissingUserSecrets 用已有用户的凭据补齐导入文件中省略的凭据，覆盖导入不会让客户端失效。
func fillMissingUserSecrets(user *User, existing *User) {
	fill := func(target *string, value string) {
		if *target == "" {
			*target = value
		}
	}
	fill(&user.UUID, existing.UUID)
	fill(&user.SSPassword, existing.SSPassword)
	fill(&user.SubToken, existing.SubToken)
	for index := range user.Profiles {
		for _, old := range existing.Profiles {
			if old.Name == user.Profiles[index].Name {
				fill(&user.Profiles[index].UUID, old.UUID)
				fill(&user.Profiles[index].SSPassword, old.SSPassword)
			}
		}
	}
}

func fillMissingStackSecrets(stack *Stack, existing *Stack) {
	if stack.Mihomo.ControllerSecret == "" {
		stack.Mihomo.ControllerSecret = existing.Mihomo.ControllerSecret
	}
	for index := range stack.Xray.Inbounds {
		inbound := &stack.Xray.Inbounds[index]
		old := existing.InboundByName(inbound.Name)
		if old < 0 {
			continue
		}
		if inbound.Password == "" {
			inbound.Password = existing.Xray.Inbounds[old].Password
		}
		if inbound.AuthPassword == "" {
			inbound.AuthPassword = existing.Xray.Inbounds[old].AuthPassword
		}
	}
}
