package configdoc

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/eagle/proxystack-web/internal/domain"
)

// loopback 是 xray API 与 mihomo controller 固定的监听地址。
const loopback = "127.0.0.1"

// Compile 把文档翻译成领域模型（未展开 user_refs）。settings 为空时使用默认设置。
func Compile(doc *Document) (domain.StackSet, error) {
	settings := DefaultSettings()
	if doc.Settings != nil {
		settings = *doc.Settings
	}
	set := domain.StackSet{Config: compileGlobal(settings, doc.Users)}
	for index := range doc.Stacks {
		stack, err := compileStack(settings, &doc.Stacks[index])
		if err != nil {
			return domain.StackSet{}, fmt.Errorf("stacks[%d]: %w", index, err)
		}
		set.Stacks = append(set.Stacks, stack)
	}
	return set, nil
}

// DisabledUsers 把手动暂停展开为每个 stack 上的暂停集合：Enabled 为 false 的用户在全部 stack 上暂停，
// 其余用户在 PausedStacks 列出的 stack 上暂停。
func DisabledUsers(doc *Document) domain.DisabledUserSet {
	disabled := domain.DisabledUserSet{}
	for _, user := range doc.Users {
		for _, stack := range doc.Stacks {
			if !user.Enabled || slices.Contains(user.PausedStacks, stack.Name) {
				disabled.Add(stack.Name, user.Name)
			}
		}
	}
	return disabled
}

func compileGlobal(settings Settings, users []User) domain.GlobalConfig {
	yes := func(value bool) *bool { return &value }
	policy := settings.Defaults.Xray.Policy
	config := domain.GlobalConfig{
		ExternalHost: settings.Basic.ExternalHost,
		PortRanges: domain.PortRanges{
			XrayInbound:    domain.PortRange(settings.PortPools.XrayInbound),
			ClashSocks:     domain.PortRange(settings.PortPools.MihomoSocks),
			ClashHTTP:      domain.PortRange(settings.PortPools.MihomoHTTP),
			XrayAPIRange:   domain.PortRange(settings.PortPools.XrayAPI),
			ClashControler: domain.PortRange(settings.PortPools.MihomoController),
		},
		Defaults: domain.DefaultsConfig{
			Clash: domain.DefaultClashConfig{
				Mode:        settings.Defaults.Mihomo.Mode,
				LogLevel:    settings.Defaults.Mihomo.LogLevel,
				RuleProfile: domain.DefaultUserProfile,
			},
			Xray: domain.DefaultXrayConfig{
				LogLevel: settings.Defaults.Xray.LogLevel,
				API:      domain.DefaultXrayAPIConfig(),
				Stats:    domain.XrayStatsConfig{Enabled: settings.Defaults.Xray.Stats},
				Policy: domain.XrayPolicyConfig{
					Enabled: policy.Enabled,
					Levels: map[string]domain.XrayPolicyLevelConfig{
						"0": {StatsUserUplink: yes(policy.StatsUserUplink), StatsUserDownlink: yes(policy.StatsUserDownlink)},
					},
					System: domain.XrayPolicySystemConfig{
						StatsInboundUplink:    yes(policy.StatsInboundUplink),
						StatsInboundDownlink:  yes(policy.StatsInboundDownlink),
						StatsOutboundUplink:   yes(policy.StatsOutboundUplink),
						StatsOutboundDownlink: yes(policy.StatsOutboundDownlink),
					},
				},
			},
		},
		Security: domain.SecurityConfig{
			RequireAuthForPublicSocksHTTP: settings.Security.RequireAuthForPublicSocksHTTP,
			AllowNoAuthPublic:             settings.Security.AllowNoAuthPublic,
		},
	}
	config.Defaults.Xray.API.Enabled = settings.Defaults.Xray.APIEnabled
	config.Defaults.Xray.API.Services = append([]string(nil), settings.Defaults.Xray.APIServices...)
	if settings.Traffic.Enabled {
		// 流量监控依赖 stats、用户级计数和 StatsService，开启时不受默认值影响。
		config.Defaults.Xray.Stats.Enabled = true
		policy := &config.Defaults.Xray.Policy
		policy.Enabled = true
		policy.Levels["0"] = domain.XrayPolicyLevelConfig{StatsUserUplink: yes(true), StatsUserDownlink: yes(true)}
		policy.System = domain.XrayPolicySystemConfig{
			StatsInboundUplink: yes(true), StatsInboundDownlink: yes(true), StatsOutboundUplink: yes(true), StatsOutboundDownlink: yes(true),
		}
		config.Defaults.Xray.API.Enabled = true
		if !slices.Contains(config.Defaults.Xray.API.Services, "StatsService") {
			config.Defaults.Xray.API.Services = append(config.Defaults.Xray.API.Services, "StatsService")
		}
	}
	for _, user := range users {
		config.Users = append(config.Users, domain.UserProfile{
			User:            user.Name,
			Profile:         DefaultProfile,
			Email:           domain.UserStatsEmail(user.ID, 0),
			UUID:            user.UUID,
			Password:        user.SSPassword,
			Remark:          user.Remark,
			DisplayTemplate: user.DisplayTemplate,
			Tag:             user.Tag,
		})
		for _, profile := range user.Profiles {
			config.Users = append(config.Users, domain.UserProfile{
				User:            user.Name,
				Profile:         profile.Name,
				Email:           domain.UserStatsEmail(user.ID, profile.ID),
				UUID:            profile.UUID,
				Password:        profile.SSPassword,
				Remark:          profile.Remark,
				DisplayTemplate: profile.DisplayTemplate,
				Tag:             profile.Tag,
			})
		}
	}
	return config
}

func compileStack(settings Settings, stack *Stack) (domain.Stack, error) {
	result := domain.Stack{
		Name:    stack.Name,
		Enabled: stack.Enabled,
		Role:    stack.Role,
		Labels:  append([]string(nil), stack.Labels...),
	}
	xray := stack.Xray
	apiListen := hostPort(loopback, xray.APIPort)
	result.Xray = domain.XrayConfig{
		Enabled:  xray.Enabled,
		LogLevel: inheritToEmpty(xray.LogLevel),
		Outbound: compileOutbound(stack.Name, xray.Outbound),
		API:      &domain.XrayAPIOverride{Enabled: triState(xray.API), Listen: &apiListen},
	}
	if enabled := triState(xray.Stats); enabled != nil {
		result.Xray.Stats = &domain.XrayStatsOverride{Enabled: enabled}
	}
	if enabled := triState(xray.Policy); enabled != nil {
		result.Xray.Policy = &domain.XrayPolicyOverride{Enabled: enabled}
	}
	if settings.Traffic.Enabled {
		// 流量监控开启时 stack 级覆盖不能关闭统计所需的组件。
		on := true
		result.Xray.API.Enabled = &on
		result.Xray.Stats = &domain.XrayStatsOverride{Enabled: &on}
		result.Xray.Policy = &domain.XrayPolicyOverride{Enabled: &on}
	}
	for _, inbound := range xray.Inbounds {
		result.Xray.Inbounds = append(result.Xray.Inbounds, compileInbound(inbound))
	}

	mihomo := stack.Mihomo
	mode := mihomo.Mode
	if mode == Inherit || mode == "" {
		mode = settings.Defaults.Mihomo.Mode
	}
	result.Clash = domain.ClashConfig{
		Enabled:    mihomo.Enabled,
		Mode:       mode,
		LogLevel:   inheritToEmpty(mihomo.LogLevel),
		Controller: domain.ClashController{Listen: hostPort(loopback, mihomo.ControllerPort), Secret: mihomo.ControllerSecret},
		Rules: domain.ClashRules{
			Profile: mihomo.Rules.Profile,
			Final:   mihomo.Rules.Final,
		},
	}
	result.Clash.Listeners.Socks = []domain.SocksListener{{
		Name:   mihomo.Socks.Name,
		Listen: mihomo.Socks.Listen,
		Port:   mihomo.Socks.Port,
		Users:  listenerUsers(mihomo.Socks),
	}}
	if mihomo.HTTP.Enabled {
		result.Clash.Listeners.HTTP = []domain.HTTPListener{{
			Name:   mihomo.HTTP.Name,
			Listen: mihomo.HTTP.Listen,
			Port:   mihomo.HTTP.Port,
			Users:  listenerUsers(mihomo.HTTP),
		}}
	}
	for index, upstream := range mihomo.Upstreams {
		compiled := domain.ClashUpstream{Name: upstream.Name, Type: upstream.Type}
		switch upstream.Type {
		case "xray-socks5":
			compiled.Ref = upstream.Stack + "." + upstream.Inbound
		case "raw":
			if err := compiled.SetRawConfig(upstream.Config.Node); err != nil {
				return domain.Stack{}, fmt.Errorf("mihomo.upstreams[%d].config: %w", index, err)
			}
		}
		result.Clash.Upstreams = append(result.Clash.Upstreams, compiled)
	}
	for _, group := range mihomo.Groups {
		result.Clash.Groups = append(result.Clash.Groups, domain.ClashGroup{
			Name:     group.Name,
			Type:     group.Type,
			Proxies:  append([]string(nil), group.Proxies...),
			URL:      group.URL,
			Interval: group.Interval,
			Strategy: group.Strategy,
		})
	}
	for _, rule := range mihomo.Rules.Extra {
		result.Clash.Rules.Extra = append(result.Clash.Rules.Extra, rule.String())
	}
	return result, nil
}

func compileOutbound(stackName string, outbound Outbound) domain.XrayOutbound {
	switch outbound.Type {
	case "mihomo":
		return domain.XrayOutbound{Type: "clash", Ref: stackName + ".clash.socks"}
	case "socks5", "http":
		return domain.XrayOutbound{
			Type:          outbound.Type,
			Server:        outbound.Server,
			Port:          outbound.Port,
			Username:      outbound.Username,
			Password:      outbound.Password,
			PrivateDirect: outbound.PrivateDirect,
		}
	default:
		return domain.XrayOutbound{Type: outbound.Type}
	}
}

func compileInbound(inbound Inbound) domain.Inbound {
	result := domain.Inbound{
		Name:            inbound.Name,
		Protocol:        inbound.Protocol,
		Listen:          inbound.Listen,
		Port:            inbound.Port,
		Sub:             inbound.Sub,
		Server:          inbound.Server,
		Remark:          inbound.Remark,
		DisplayTemplate: inbound.DisplayTemplate,
		Region:          inbound.Region,
		Tag:             inbound.Tag,
	}
	if domain.SupportsInboundUDPProtocol(inbound.Protocol) {
		udp := inbound.UDP
		result.UDP = &udp
	}
	switch inbound.Protocol {
	case "vmess":
		result.Network = inbound.Network
		switch inbound.Network {
		case "ws":
			result.WSOpts = &domain.WebSocketOptions{Path: inbound.WSPath, Headers: inbound.WSHeaders}
		case "grpc":
			result.GRPCOpts = &domain.GRPCOptions{ServiceName: inbound.GRPCServiceName}
		}
	case "shadowsocks":
		result.Method = inbound.Method
		result.Password = inbound.Password
	case "socks5", "http":
		authType := inbound.AuthType
		if authType == "" {
			authType = "noauth"
		}
		result.Auth = &domain.InboundAuth{Type: authType}
		if authType == "password" {
			result.Auth.Username = inbound.AuthUsername
			result.Auth.Password = inbound.AuthPassword
		}
	}
	for _, ref := range inbound.Users {
		result.UserRefs = append(result.UserRefs, domain.InboundUserRef{
			User:            ref.User,
			Profile:         domain.NormalizeUserProfile(ref.Profile),
			Remark:          ref.Remark,
			DisplayTemplate: ref.DisplayTemplate,
			Tag:             ref.Tag,
		})
	}
	return result
}

// listenerUsers 把认证方式翻译为 mihomo 的 users 字段：nil 跟随全局认证，空列表表示不认证。
func listenerUsers(listener Listener) []domain.ClashListenerUser {
	switch listener.Auth {
	case ListenerAuthNone:
		return []domain.ClashListenerUser{}
	case ListenerAuthAccounts:
		users := make([]domain.ClashListenerUser, 0, len(listener.Accounts))
		for _, account := range listener.Accounts {
			users = append(users, domain.ClashListenerUser{Username: account.Username, Password: account.Password})
		}
		return users
	default:
		return nil
	}
}

// String 输出 mihomo 规则文本。
func (r Rule) String() string {
	parts := []string{r.Type}
	if r.Value != "" {
		parts = append(parts, r.Value)
	}
	parts = append(parts, r.Target)
	if r.NoResolve {
		parts = append(parts, "no-resolve")
	}
	return strings.Join(parts, ",")
}

// ParseRule 解析一条 mihomo 规则文本：首段为类型，末段为目标（可带 no-resolve），中间为匹配值。
func ParseRule(text string) (Rule, error) {
	parts := make([]string, 0)
	for _, part := range strings.Split(text, ",") {
		parts = append(parts, strings.TrimSpace(part))
	}
	rule := Rule{}
	if len(parts) > 0 && parts[len(parts)-1] == "no-resolve" {
		rule.NoResolve = true
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 3 || parts[0] == "" || parts[len(parts)-1] == "" {
		return Rule{}, fmt.Errorf("rule must use TYPE,VALUE,TARGET format: %s", text)
	}
	rule.Type = parts[0]
	rule.Target = parts[len(parts)-1]
	rule.Value = strings.Join(parts[1:len(parts)-1], ",")
	return rule, nil
}

func triState(value string) *bool {
	switch value {
	case On:
		result := true
		return &result
	case Off:
		result := false
		return &result
	default:
		return nil
	}
}

func inheritToEmpty(value string) string {
	if value == Inherit {
		return ""
	}
	return value
}

func hostPort(host string, port int) string {
	return host + ":" + strconv.Itoa(port)
}
