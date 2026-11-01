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
				remark, region, err := directSubscriptionName(stack, user, protocol)
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

// directSubscriptionName 优先沿用该用户第一个订阅入站的名称；仅授权直连时回退到首个订阅入站。
func directSubscriptionName(stack Stack, user User, protocol string) (string, string, error) {
	inbound := Inbound{Name: "direct", Protocol: protocol}
	for _, candidate := range stack.Xray.Inbounds {
		if !candidate.Sub {
			continue
		}
		if !inbound.Sub {
			inbound = candidate
		}
		if slices.ContainsFunc(candidate.Users, func(ref UserRef) bool { return ref.User == user.Name }) {
			inbound = candidate
			break
		}
	}
	remark, display, profile := inbound.Remark, user.DisplayTemplate, DefaultProfile
	for _, ref := range inbound.Users {
		if ref.User != user.Name {
			continue
		}
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
		break
	}
	if display == "" {
		display = inbound.DisplayTemplate
	}
	name, err := sub.SubscriptionRemark(stack.Name, compileInbound(inbound), user.Name, domain.NormalizeUserProfile(profile), remark, display)
	return name, inbound.Region, err
}
