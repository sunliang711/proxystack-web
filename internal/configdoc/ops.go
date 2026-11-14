package configdoc

import (
	"cmp"
	"slices"
	"strings"
)

// 本文件实现跨实体的编辑操作：改名时同步引用方、auto 成员同步、克隆。

// RenameUser 把全部入站对 oldName 的引用改为 newName。
func RenameUser(doc *Document, oldName, newName string) {
	if oldName == newName {
		return
	}
	forEachUserRef(doc, func(ref *UserRef) {
		if ref.User == oldName {
			ref.User = newName
		}
	})
	for index := range doc.ManualNodes {
		if doc.ManualNodes[index].User == oldName {
			doc.ManualNodes[index].User = newName
		}
	}
	for index := range doc.Stacks {
		for position, user := range doc.Stacks[index].DirectUsers {
			if user == oldName {
				doc.Stacks[index].DirectUsers[position] = newName
			}
		}
		for position := range doc.Stacks[index].DirectOverrides {
			if doc.Stacks[index].DirectOverrides[position].User == oldName {
				doc.Stacks[index].DirectOverrides[position].User = newName
			}
		}
	}
}

// RemoveUserNodes 删除用户的全部手工节点。
func RemoveUserNodes(doc *Document, user string) {
	doc.ManualNodes = slices.DeleteFunc(doc.ManualNodes, func(node ManualNode) bool { return node.User == user })
}

// RenameProfile 把引用 user 的 oldName 档案改为 newName。
func RenameProfile(doc *Document, user, oldName, newName string) {
	if oldName == newName {
		return
	}
	forEachUserRef(doc, func(ref *UserRef) {
		if ref.User == user && ref.Profile == oldName {
			ref.Profile = newName
		}
	})
}

// RemoveProfileRefs 把引用已删除档案的入站改回 default 档案。
func RemoveProfileRefs(doc *Document, user, profile string) {
	forEachUserRef(doc, func(ref *UserRef) {
		if ref.User == user && ref.Profile == profile {
			ref.Profile = ""
		}
	})
}

// RemoveUserRefs 删除全部入站对 user 的引用。
func RemoveUserRefs(doc *Document, user string) {
	for stackIndex := range doc.Stacks {
		doc.Stacks[stackIndex].DirectUsers = slices.DeleteFunc(doc.Stacks[stackIndex].DirectUsers, func(name string) bool { return name == user })
		doc.Stacks[stackIndex].DirectOverrides = slices.DeleteFunc(doc.Stacks[stackIndex].DirectOverrides, func(override DirectOverride) bool {
			return override.User == user
		})
		inbounds := doc.Stacks[stackIndex].Xray.Inbounds
		for inboundIndex := range inbounds {
			inbounds[inboundIndex].Users = slices.DeleteFunc(inbounds[inboundIndex].Users, func(ref UserRef) bool {
				return ref.User == user
			})
		}
	}
}

// RenameStack 把其他 stack 中 xray-socks5 上游对 oldName 的引用改为 newName。
func RenameStack(doc *Document, oldName, newName string) {
	if oldName == newName {
		return
	}
	forEachUpstream(doc, func(upstream *Upstream) {
		if upstream.Type == "xray-socks5" && upstream.Stack == oldName {
			upstream.Stack = newName
		}
	})
	for index := range doc.Users {
		paused := doc.Users[index].PausedStacks
		for i := range paused {
			if paused[i] == oldName {
				paused[i] = newName
			}
		}
	}
	if doc.Settings != nil && doc.Settings.DownloadProxy.Stack == oldName {
		doc.Settings.DownloadProxy.Stack = newName
	}
}

// RemovePausedStack 从全部用户的手动暂停列表中删除已删除的 stack。
func RemovePausedStack(doc *Document, stack string) {
	for index := range doc.Users {
		doc.Users[index].PausedStacks = slices.DeleteFunc(doc.Users[index].PausedStacks, func(name string) bool { return name == stack })
	}
}

// RenameInbound 把 xray-socks5 上游对 stack.oldName 入站的引用改为 newName。
func RenameInbound(doc *Document, stack, oldName, newName string) {
	if oldName == newName {
		return
	}
	forEachUpstream(doc, func(upstream *Upstream) {
		if upstream.Type == "xray-socks5" && upstream.Stack == stack && upstream.Inbound == oldName {
			upstream.Inbound = newName
		}
	})
}

// RenameProxyTarget 在 stack 内把代理组成员、规则目标和 final 中的 oldName 改为 newName。
func RenameProxyTarget(stack *Stack, oldName, newName string) {
	if oldName == newName {
		return
	}
	for groupIndex := range stack.Mihomo.Groups {
		proxies := stack.Mihomo.Groups[groupIndex].Proxies
		for index := range proxies {
			if proxies[index] == oldName {
				proxies[index] = newName
			}
		}
	}
	if stack.Mihomo.Rules.Final == oldName {
		stack.Mihomo.Rules.Final = newName
	}
	for index := range stack.Mihomo.Rules.Extra {
		if stack.Mihomo.Rules.Extra[index].Target == oldName {
			stack.Mihomo.Rules.Extra[index].Target = newName
		}
	}
}

// RenameDirectOverrideUpstream 在 stack 内把直连覆盖对上游 oldName 的引用改为 newName。
func RenameDirectOverrideUpstream(stack *Stack, oldName, newName string) {
	if oldName == newName {
		return
	}
	for index := range stack.DirectOverrides {
		if stack.DirectOverrides[index].Upstream == oldName {
			stack.DirectOverrides[index].Upstream = newName
		}
	}
}

// Members 返回 auto stack 当前的成员 stack 名：xray-socks5 上游引用的 stack。
func Members(stack *Stack) []string {
	members := []string{}
	for _, upstream := range stack.Mihomo.Upstreams {
		if upstream.Type == "xray-socks5" && upstream.Stack != "" && !slices.Contains(members, upstream.Stack) {
			members = append(members, upstream.Stack)
		}
	}
	slices.Sort(members)
	return members
}

// SetMembers 把 auto stack 的成员同步为 members：删除多余成员的上游及其在代理组中的引用，
// 为新成员追加 `<member>-local` 上游并加入自动组和包含自动组的 select 组。
func SetMembers(stack *Stack, members []string) {
	current := Members(stack)
	for _, member := range current {
		if !slices.Contains(members, member) {
			RemoveMember(stack, member)
		}
	}
	for _, member := range members {
		if !slices.Contains(current, member) {
			AddMember(stack, member)
		}
	}
}

// RelaysOnly 判断 stack 是否只经其他 stack 的 relay 出网：xray 出站交给本 stack 的 Mihomo，
// Mihomo 至少有一个上游且全部引用其他 stack 的入站。这类聚合 stack 的流量会在被引用 stack 的
// 入站再计一次，多个 stack 加总时应排除。判断只看配置结构，不看角色标记。
func RelaysOnly(stack *Stack) bool {
	if stack.Xray.Outbound.Type != "mihomo" || !stack.Mihomo.Enabled || len(stack.Mihomo.Upstreams) == 0 {
		return false
	}
	for _, upstream := range stack.Mihomo.Upstreams {
		if upstream.Type != "xray-socks5" {
			return false
		}
	}
	return true
}

// AddMember 为 auto stack 追加一个成员。
func AddMember(stack *Stack, member string) {
	name := member + "-local"
	stack.Mihomo.Upstreams = append(stack.Mihomo.Upstreams, Upstream{
		Name: name, Type: "xray-socks5", Stack: member, Inbound: RelayInbound,
	})
	autoGroups := map[string]bool{}
	for _, group := range stack.Mihomo.Groups {
		if group.Type == "url-test" || group.Type == "load-balance" {
			autoGroups[group.Name] = true
		}
	}
	for index := range stack.Mihomo.Groups {
		group := &stack.Mihomo.Groups[index]
		if slices.Contains(group.Proxies, name) {
			continue
		}
		switch {
		case autoGroups[group.Name]:
			group.Proxies = append(group.Proxies, name)
		case group.Type == "select" && slices.ContainsFunc(group.Proxies, func(proxy string) bool { return autoGroups[proxy] }):
			position := slices.Index(group.Proxies, BuiltinDirect)
			if position < 0 {
				group.Proxies = append(group.Proxies, name)
			} else {
				group.Proxies = slices.Insert(group.Proxies, position, name)
			}
		}
	}
}

// RemoveMember 删除 auto stack 中引用 member 的上游以及代理组里对这些上游的引用。
func RemoveMember(stack *Stack, member string) {
	removed := map[string]bool{}
	stack.Mihomo.Upstreams = slices.DeleteFunc(stack.Mihomo.Upstreams, func(upstream Upstream) bool {
		if upstream.Type == "xray-socks5" && upstream.Stack == member {
			removed[upstream.Name] = true
			return true
		}
		return false
	})
	for index := range stack.Mihomo.Groups {
		stack.Mihomo.Groups[index].Proxies = slices.DeleteFunc(stack.Mihomo.Groups[index].Proxies, func(proxy string) bool {
			return removed[proxy]
		})
	}
}

// CloneStack 复制 source 为名为 target 的新 stack：清空 ID，只改写指向 source 自身入站的引用，
// 订阅备注中以 source 名开头的前缀改为 target，controller secret 重新生成。端口由调用方重新分配。
func CloneStack(source *Stack, target string) Stack {
	clone := source.Clone()
	clone.ID = 0
	clone.Name = target
	clone.DesiredRunning = false
	clone.Mihomo.ControllerSecret = RandomSecret()
	for index := range clone.Xray.Inbounds {
		clone.Xray.Inbounds[index].ID = 0
	}
	RenameStackRemarks(&clone, source.Name, target)
	for index := range clone.Mihomo.Upstreams {
		upstream := &clone.Mihomo.Upstreams[index]
		upstream.ID = 0
		if upstream.Type == "xray-socks5" && upstream.Stack == source.Name {
			upstream.Stack = target
		}
	}
	for index := range clone.Mihomo.Groups {
		clone.Mihomo.Groups[index].ID = 0
	}
	return clone
}

// RenameStackRemarks 把 stack 内入站和用户引用中以旧 stack 名开头的显示名换成新名字。
// 新建 stack 时生成的显示名都以 stack 名开头；手工改过、不以旧名开头的显示名保持不变。
func RenameStackRemarks(stack *Stack, oldName, newName string) {
	if oldName == newName {
		return
	}
	for index := range stack.Xray.Inbounds {
		inbound := &stack.Xray.Inbounds[index]
		inbound.Remark = replaceRemarkPrefix(inbound.Remark, oldName, newName)
		for refIndex := range inbound.Users {
			inbound.Users[refIndex].Remark = replaceRemarkPrefix(inbound.Users[refIndex].Remark, oldName, newName)
		}
	}
}

// RemarkConflict 是新 stack 的入站与已有 stack 的入站对同一用户使用了相同显示名。
type RemarkConflict struct {
	Remark  string
	User    string
	Stack   string
	Inbound string
}

// FindRemarkConflicts 找出 stack 的订阅入站与文档中其他 stack 的订阅入站对同一用户使用的相同显示名。
// 只比较显示名本身，不展开展示名模板；按模板渲染后的完整重名由整体校验兜底。
func FindRemarkConflicts(doc *Document, stack *Stack) []RemarkConflict {
	type owner struct{ stack, inbound string }
	used := map[[2]string]owner{}
	for index := range doc.Stacks {
		other := &doc.Stacks[index]
		if other.Name == stack.Name || !other.Enabled || !other.Xray.Enabled {
			continue
		}
		for _, inbound := range other.Xray.Inbounds {
			if !inbound.Sub {
				continue
			}
			for _, ref := range inbound.Users {
				key := [2]string{ref.User, cmp.Or(ref.Remark, inbound.Remark)}
				if _, ok := used[key]; !ok {
					used[key] = owner{other.Name, inbound.Name}
				}
			}
		}
	}
	var conflicts []RemarkConflict
	for _, inbound := range stack.Xray.Inbounds {
		if !inbound.Sub {
			continue
		}
		for _, ref := range inbound.Users {
			remark := cmp.Or(ref.Remark, inbound.Remark)
			if found, ok := used[[2]string{ref.User, remark}]; ok && remark != "" {
				conflicts = append(conflicts, RemarkConflict{Remark: remark, User: ref.User, Stack: found.stack, Inbound: found.inbound})
			}
		}
	}
	return conflicts
}

func replaceRemarkPrefix(remark, source, target string) string {
	if remark == source {
		return target
	}
	if strings.HasPrefix(remark, source+" ") {
		return target + strings.TrimPrefix(remark, source)
	}
	return remark
}

func forEachUserRef(doc *Document, fn func(*UserRef)) {
	for stackIndex := range doc.Stacks {
		inbounds := doc.Stacks[stackIndex].Xray.Inbounds
		for inboundIndex := range inbounds {
			for refIndex := range inbounds[inboundIndex].Users {
				fn(&inbounds[inboundIndex].Users[refIndex])
			}
		}
	}
}

func forEachUpstream(doc *Document, fn func(*Upstream)) {
	for stackIndex := range doc.Stacks {
		upstreams := doc.Stacks[stackIndex].Mihomo.Upstreams
		for index := range upstreams {
			fn(&upstreams[index])
		}
	}
}
