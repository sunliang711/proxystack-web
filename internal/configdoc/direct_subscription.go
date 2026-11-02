package configdoc

import (
	"fmt"
	"slices"

	"proxystack-web/internal/domain"
	"proxystack-web/internal/generator/sub"
)

// StackDirectNodes 按当前保存的授权生成外部上游节点，不依赖本机进程的应用状态。
func StackDirectNodes(doc *Document) ([]sub.Node, error) {
	nodes := []sub.Node{}
	for _, stack := range doc.Stacks {
		if !stack.Enabled || len(stack.DirectUsers) == 0 {
			continue
		}
		suffix := stack.DirectSuffix
		if suffix == "" {
			suffix = DefaultDirectSuffix
		}
		externalCount := 0
		for _, upstream := range stack.Mihomo.Upstreams {
			if upstream.Type == "raw" {
				externalCount++
			}
		}
		for _, name := range stack.DirectUsers {
			index := doc.UserByName(name)
			if index < 0 {
				return nil, fmt.Errorf("stack %s references a missing direct user: %s", stack.Name, name)
			}
			user := doc.Users[index]
			if slices.Contains(user.PausedStacks, stack.Name) {
				continue
			}
			for _, upstream := range stack.Mihomo.Upstreams {
				if upstream.Type != "raw" {
					continue
				}
				protocol, _ := upstream.Config.Get("type")
				remark, region, err := directSubscriptionName(doc, stack, user, protocol)
				if err != nil {
					return nil, err
				}
				if externalCount > 1 {
					remark += " · " + upstream.Name
				}
				remark += suffix
				id := stack.Name + ":direct:" + upstream.Name + ":" + name
				config := RawMapping{Node: cloneNode(upstream.Config.Node)}
				// 服务器上的拨号链、网卡和路由标记不能带到用户设备，直连只使用上游自身参数。
				config.Delete("dialer-proxy")
				config.Delete("interface-name")
				config.Delete("routing-mark")
				node, err := (ManualNode{User: name, Remark: remark, Region: region, Config: config}).directNode(id, id)
				if err != nil {
					return nil, fmt.Errorf("stack %s upstream %s: %w", stack.Name, upstream.Name, err)
				}
				nodes = append(nodes, node)
			}
		}
	}
	return nodes, nil
}

// directSubscriptionName 使用包含该用户的首个订阅入站；仅授权直连时不借用无关入站模板。
func directSubscriptionName(doc *Document, stack Stack, user User, protocol string) (string, string, error) {
	inbound := Inbound{Name: "direct", Protocol: protocol}
	var ref *UserRef
	for candidateIndex := range stack.Xray.Inbounds {
		candidate := &stack.Xray.Inbounds[candidateIndex]
		if !candidate.Sub {
			continue
		}
		for refIndex := range candidate.Users {
			if candidate.Users[refIndex].User == user.Name {
				inbound = *candidate
				ref = &candidate.Users[refIndex]
				break
			}
		}
		if ref != nil {
			break
		}
	}
	remark, display, profile := inbound.Remark, user.DisplayTemplate, DefaultProfile
	if ref != nil {
		if ref.Profile != "" && ref.Profile != DefaultProfile {
			profile = ref.Profile
			for _, candidate := range user.Profiles {
				if candidate.Name == profile {
					if candidate.Remark != "" {
						remark = candidate.Remark
					}
					display = candidate.DisplayTemplate
					break
				}
			}
		}
		if ref.Remark != "" {
			remark = ref.Remark
		}
		if ref.DisplayTemplate != "" {
			display = ref.DisplayTemplate
		}
	}
	globalDisplay := ""
	if doc.Settings != nil {
		globalDisplay = doc.Settings.Subscription.DisplayTemplate
	}
	name, err := sub.SubscriptionRemark(stack.Name, compileInbound(inbound), user.Name, domain.NormalizeUserProfile(profile), remark,
		display, inbound.DisplayTemplate, globalDisplay)
	return name, inbound.Region, err
}
