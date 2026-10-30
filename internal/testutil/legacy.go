package testutil

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/eagle/proxystack-web/internal/domain"
)

// 参考项目 YAML 的镜像结构。指针字段表示「YAML 中是否写了该字段」，
// 翻译时只覆盖写了的字段，其余沿用参考项目的默认值。

type legacyGlobal struct {
	ExternalHost string              `yaml:"external_host"`
	Users        []legacyUserProfile `yaml:"users"`
	PortRanges   legacyPortRanges    `yaml:"port_ranges"`
	Defaults     legacyDefaults      `yaml:"defaults"`
	Security     *legacySecurity     `yaml:"security"`
}

type legacyUserProfile struct {
	User            string `yaml:"user"`
	Profile         string `yaml:"profile"`
	UUID            string `yaml:"uuid"`
	Password        string `yaml:"password"`
	Remark          string `yaml:"remark"`
	DisplayTemplate string `yaml:"display_template"`
	Tag             string `yaml:"tag"`
}

type legacyPortRange domain.PortRange

func (p *legacyPortRange) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		parts := strings.SplitN(value.Value, "-", 2)
		if len(parts) != 2 {
			return fmt.Errorf("port range must use start-end format")
		}
		start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return fmt.Errorf("port range start must be an integer: %w", err)
		}
		end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return fmt.Errorf("port range end must be an integer: %w", err)
		}
		*p = legacyPortRange{Start: start, End: end}
		return nil
	}
	var raw struct {
		Start int `yaml:"start"`
		End   int `yaml:"end"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*p = legacyPortRange{Start: raw.Start, End: raw.End}
	return nil
}

type legacyPortRanges struct {
	XrayInbound     legacyPortRange `yaml:"xray_inbound"`
	ClashSocks      legacyPortRange `yaml:"clash_socks"`
	ClashHTTP       legacyPortRange `yaml:"clash_http"`
	XrayAPIRange    legacyPortRange `yaml:"xray_api_range"`
	ClashController legacyPortRange `yaml:"clash_controller"`
}

type legacyDefaults struct {
	Clash struct {
		Mode        *string `yaml:"mode"`
		LogLevel    *string `yaml:"loglevel"`
		RuleProfile *string `yaml:"rule_profile"`
	} `yaml:"clash"`
	Xray struct {
		LogLevel *string       `yaml:"loglevel"`
		API      *legacyAPI    `yaml:"api"`
		Stats    *legacyStats  `yaml:"stats"`
		Policy   *legacyPolicy `yaml:"policy"`
	} `yaml:"xray"`
}

type legacyAPI struct {
	Enabled  *bool    `yaml:"enabled"`
	Tag      *string  `yaml:"tag"`
	Listen   *string  `yaml:"listen"`
	Services []string `yaml:"services"`
}

type legacyStats struct {
	Enabled *bool `yaml:"enabled"`
}

type legacyPolicyLevel struct {
	StatsUserUplink   *bool `yaml:"statsUserUplink"`
	StatsUserDownlink *bool `yaml:"statsUserDownlink"`
}

type legacyPolicySystem struct {
	StatsInboundUplink    *bool `yaml:"statsInboundUplink"`
	StatsInboundDownlink  *bool `yaml:"statsInboundDownlink"`
	StatsOutboundUplink   *bool `yaml:"statsOutboundUplink"`
	StatsOutboundDownlink *bool `yaml:"statsOutboundDownlink"`
}

type legacyPolicy struct {
	Enabled *bool                        `yaml:"enabled"`
	Levels  map[string]legacyPolicyLevel `yaml:"levels"`
	System  *legacyPolicySystem          `yaml:"system"`
}

type legacySecurity struct {
	RequireAuthForPublicSocksHTTP *bool `yaml:"require_auth_for_public_socks_http"`
	AllowNoAuthPublic             *bool `yaml:"allow_noauth_public"`
}

func (g legacyGlobal) toDomain() domain.GlobalConfig {
	config := domain.GlobalConfig{
		ExternalHost: g.ExternalHost,
		PortRanges: domain.PortRanges{
			XrayInbound:    domain.PortRange(g.PortRanges.XrayInbound),
			ClashSocks:     domain.PortRange(g.PortRanges.ClashSocks),
			ClashHTTP:      domain.PortRange(g.PortRanges.ClashHTTP),
			XrayAPIRange:   domain.PortRange(g.PortRanges.XrayAPIRange),
			ClashControler: domain.PortRange(g.PortRanges.ClashController),
		},
		Defaults: domain.DefaultDefaultsConfig(),
		Security: domain.DefaultSecurityConfig(),
	}
	for _, user := range g.Users {
		config.Users = append(config.Users, domain.UserProfile(user))
	}
	clash := g.Defaults.Clash
	setString(&config.Defaults.Clash.Mode, clash.Mode)
	setString(&config.Defaults.Clash.LogLevel, clash.LogLevel)
	setString(&config.Defaults.Clash.RuleProfile, clash.RuleProfile)
	xray := g.Defaults.Xray
	setString(&config.Defaults.Xray.LogLevel, xray.LogLevel)
	if xray.API != nil {
		config.Defaults.Xray.API = domain.ResolveXrayAPIConfig(config.Defaults.Xray, domain.XrayConfig{API: xray.API.toOverride()})
	}
	if xray.Stats != nil && xray.Stats.Enabled != nil {
		config.Defaults.Xray.Stats.Enabled = *xray.Stats.Enabled
	}
	if xray.Policy != nil {
		policy := &config.Defaults.Xray.Policy
		if xray.Policy.Enabled != nil {
			policy.Enabled = *xray.Policy.Enabled
		}
		for level, value := range xray.Policy.Levels {
			// 参考项目解码 levels 时每个 level 整体替换默认值。
			policy.Levels[level] = domain.XrayPolicyLevelConfig(value)
		}
		if xray.Policy.System != nil {
			policy.System = domain.XrayPolicySystemConfig(*xray.Policy.System)
		}
	}
	if g.Security != nil {
		setBool(&config.Security.RequireAuthForPublicSocksHTTP, g.Security.RequireAuthForPublicSocksHTTP)
		setBool(&config.Security.AllowNoAuthPublic, g.Security.AllowNoAuthPublic)
	}
	return config
}

func (a *legacyAPI) toOverride() *domain.XrayAPIOverride {
	if a == nil {
		return nil
	}
	return &domain.XrayAPIOverride{Enabled: a.Enabled, Tag: a.Tag, Listen: a.Listen, Services: a.Services}
}

func (p *legacyPolicy) toOverride() *domain.XrayPolicyOverride {
	if p == nil {
		return nil
	}
	override := &domain.XrayPolicyOverride{Enabled: p.Enabled}
	if p.Levels != nil {
		override.Levels = make(map[string]domain.XrayPolicyLevelConfig, len(p.Levels))
		for level, value := range p.Levels {
			override.Levels[level] = domain.XrayPolicyLevelConfig(value)
		}
	}
	if p.System != nil {
		system := domain.XrayPolicySystemConfig(*p.System)
		override.System = &system
	}
	return override
}

type legacyStack struct {
	Name    string      `yaml:"name"`
	Enabled *bool       `yaml:"enabled"`
	Role    string      `yaml:"role"`
	Labels  []string    `yaml:"labels"`
	Xray    legacyXray  `yaml:"xray"`
	Clash   legacyClash `yaml:"clash"`
}

type legacyXray struct {
	Enabled  *bool           `yaml:"enabled"`
	LogLevel string          `yaml:"loglevel"`
	Outbound legacyOutbound  `yaml:"outbound"`
	Inbounds []legacyInbound `yaml:"inbounds"`
	API      *legacyAPI      `yaml:"api"`
	Stats    *legacyStats    `yaml:"stats"`
	Policy   *legacyPolicy   `yaml:"policy"`
}

type legacyOutbound struct {
	Type          string `yaml:"type"`
	Ref           string `yaml:"ref"`
	Server        string `yaml:"server"`
	Port          int    `yaml:"port"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	PrivateDirect bool   `yaml:"private_direct"`
}

type legacyAuth struct {
	Type     string `yaml:"type"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type legacyInboundUser struct {
	User            string  `yaml:"user"`
	Profile         string  `yaml:"profile"`
	UUID            string  `yaml:"uuid"`
	Password        string  `yaml:"password"`
	Remark          string  `yaml:"remark"`
	DisplayTemplate string  `yaml:"display_template"`
	Tag             string  `yaml:"tag"`
	Region          *string `yaml:"region"`
}

type legacyUserRef domain.InboundUserRef

func (r *legacyUserRef) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*r = legacyUserRef{User: value.Value, Profile: domain.DefaultUserProfile}
		return nil
	}
	var raw struct {
		User            string `yaml:"user"`
		Profile         string `yaml:"profile"`
		Remark          string `yaml:"remark"`
		DisplayTemplate string `yaml:"display_template"`
		Tag             string `yaml:"tag"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*r = legacyUserRef(raw)
	return nil
}

type legacyGRPC struct {
	ServiceName string
}

func (o *legacyGRPC) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("grpc_opts must be a mapping")
	}
	for index := 0; index+1 < len(value.Content); index += 2 {
		switch value.Content[index].Value {
		case "service_name", "grpc_service_name", "serviceName":
			o.ServiceName = value.Content[index+1].Value
		}
	}
	return nil
}

type legacyInbound struct {
	Name            string              `yaml:"name"`
	Protocol        string              `yaml:"protocol"`
	Listen          *string             `yaml:"listen"`
	Port            int                 `yaml:"port"`
	UDP             *bool               `yaml:"udp"`
	Auth            *legacyAuth         `yaml:"auth"`
	User            string              `yaml:"user"`
	Server          string              `yaml:"server"`
	Remark          string              `yaml:"remark"`
	DisplayTemplate string              `yaml:"display_template"`
	Region          string              `yaml:"region"`
	Tag             string              `yaml:"tag"`
	Sub             *bool               `yaml:"sub"`
	Users           []legacyInboundUser `yaml:"users"`
	UserRefs        []legacyUserRef     `yaml:"user_refs"`
	Network         string              `yaml:"network"`
	Password        string              `yaml:"password"`
	Method          string              `yaml:"method"`
	Cipher          string              `yaml:"cipher"`
	WSOpts          *struct {
		Path    string            `yaml:"path"`
		Headers map[string]string `yaml:"headers"`
	} `yaml:"ws_opts"`
	GRPCOpts *legacyGRPC `yaml:"grpc_opts"`
}

type legacyListener struct {
	Name   string                     `yaml:"name"`
	Listen *string                    `yaml:"listen"`
	Port   int                        `yaml:"port"`
	Users  []domain.ClashListenerUser `yaml:"users"`
}

type legacyUpstream struct {
	Name   string    `yaml:"name"`
	Type   string    `yaml:"type"`
	Ref    string    `yaml:"ref"`
	Config yaml.Node `yaml:"config"`
}

type legacyGroup struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Proxies  []string `yaml:"proxies"`
	URL      string   `yaml:"url"`
	Interval int      `yaml:"interval"`
	Strategy string   `yaml:"strategy"`
}

type legacyClash struct {
	Enabled    *bool   `yaml:"enabled"`
	Mode       *string `yaml:"mode"`
	LogLevel   string  `yaml:"loglevel"`
	Controller struct {
		Listen string `yaml:"listen"`
		Secret string `yaml:"secret"`
	} `yaml:"controller"`
	Listeners struct {
		Socks []legacyListener `yaml:"socks"`
		HTTP  []legacyListener `yaml:"http"`
		Mixed *yaml.Node       `yaml:"mixed"`
	} `yaml:"listeners"`
	Upstreams []legacyUpstream `yaml:"upstreams"`
	Groups    []legacyGroup    `yaml:"groups"`
	Rules     struct {
		Profile *string  `yaml:"profile"`
		Final   *string  `yaml:"final"`
		Extra   []string `yaml:"extra"`
	} `yaml:"rules"`
}

func (s legacyStack) toDomain() (domain.Stack, error) {
	stack := domain.Stack{
		Name:    s.Name,
		Enabled: boolOr(s.Enabled, true),
		Role:    s.Role,
		Labels:  s.Labels,
	}
	if stack.Role == "" {
		stack.Role = "edge"
	}
	xray, err := s.Xray.toDomain()
	if err != nil {
		return domain.Stack{}, err
	}
	stack.Xray = xray
	clash, err := s.Clash.toDomain()
	if err != nil {
		return domain.Stack{}, err
	}
	stack.Clash = clash
	return stack, nil
}

func (x legacyXray) toDomain() (domain.XrayConfig, error) {
	config := domain.XrayConfig{
		Enabled:  boolOr(x.Enabled, true),
		LogLevel: x.LogLevel,
		Outbound: domain.XrayOutbound(x.Outbound),
		API:      x.API.toOverride(),
		Policy:   x.Policy.toOverride(),
	}
	if x.Stats != nil {
		config.Stats = &domain.XrayStatsOverride{Enabled: x.Stats.Enabled}
	}
	for _, raw := range x.Inbounds {
		inbound, err := raw.toDomain()
		if err != nil {
			return domain.XrayConfig{}, err
		}
		config.Inbounds = append(config.Inbounds, inbound)
	}
	return config, nil
}

func (i legacyInbound) toDomain() (domain.Inbound, error) {
	if i.Sub == nil {
		return domain.Inbound{}, fmt.Errorf("sub is required for inbound")
	}
	inbound := domain.Inbound{
		Name:            i.Name,
		Protocol:        i.Protocol,
		Listen:          stringOr(i.Listen, "0.0.0.0"),
		Port:            i.Port,
		UDP:             i.UDP,
		User:            i.User,
		Server:          i.Server,
		Remark:          i.Remark,
		DisplayTemplate: i.DisplayTemplate,
		Region:          i.Region,
		Tag:             i.Tag,
		Sub:             *i.Sub,
		Network:         i.Network,
		Password:        i.Password,
		Method:          i.Method,
	}
	if inbound.Method == "" {
		inbound.Method = i.Cipher
	}
	if i.Auth != nil {
		inbound.Auth = &domain.InboundAuth{Type: i.Auth.Type, Username: i.Auth.Username, Password: i.Auth.Password}
	}
	for _, user := range i.Users {
		if user.Region != nil {
			return domain.Inbound{}, fmt.Errorf("users[].region is not supported; configure inbound.region instead")
		}
		inbound.Users = append(inbound.Users, domain.InboundUser{
			User:            user.User,
			Profile:         user.Profile,
			UUID:            user.UUID,
			Password:        user.Password,
			Remark:          user.Remark,
			DisplayTemplate: user.DisplayTemplate,
			Tag:             user.Tag,
		})
	}
	for _, ref := range i.UserRefs {
		inbound.UserRefs = append(inbound.UserRefs, domain.InboundUserRef(ref))
	}
	if i.WSOpts != nil {
		inbound.WSOpts = &domain.WebSocketOptions{Path: i.WSOpts.Path, Headers: i.WSOpts.Headers}
	}
	if i.GRPCOpts != nil {
		inbound.GRPCOpts = &domain.GRPCOptions{ServiceName: i.GRPCOpts.ServiceName}
	}
	return inbound, nil
}

func (c legacyClash) toDomain() (domain.ClashConfig, error) {
	if c.Listeners.Mixed != nil {
		return domain.ClashConfig{}, fmt.Errorf("listeners.mixed is not supported in P0")
	}
	config := domain.ClashConfig{
		Enabled:    boolOr(c.Enabled, true),
		Mode:       stringOr(c.Mode, "Rule"),
		LogLevel:   c.LogLevel,
		Controller: domain.ClashController{Listen: c.Controller.Listen, Secret: c.Controller.Secret},
		Rules: domain.ClashRules{
			Profile: stringOr(c.Rules.Profile, "default"),
			Final:   stringOr(c.Rules.Final, "AllProxy"),
			Extra:   c.Rules.Extra,
		},
	}
	for _, listener := range c.Listeners.Socks {
		config.Listeners.Socks = append(config.Listeners.Socks, domain.SocksListener{
			Name: listener.Name, Listen: stringOr(listener.Listen, "127.0.0.1"), Port: listener.Port, Users: listener.Users,
		})
	}
	for _, listener := range c.Listeners.HTTP {
		config.Listeners.HTTP = append(config.Listeners.HTTP, domain.HTTPListener{
			Name: listener.Name, Listen: stringOr(listener.Listen, "127.0.0.1"), Port: listener.Port, Users: listener.Users,
		})
	}
	for _, raw := range c.Upstreams {
		upstream := domain.ClashUpstream{Name: raw.Name, Type: raw.Type, Ref: raw.Ref}
		if raw.Config.Kind != 0 {
			if err := upstream.SetRawConfig(&raw.Config); err != nil {
				return domain.ClashConfig{}, err
			}
		}
		config.Upstreams = append(config.Upstreams, upstream)
	}
	for _, group := range c.Groups {
		config.Groups = append(config.Groups, domain.ClashGroup(group))
	}
	return config, nil
}

func setString(target *string, value *string) {
	if value != nil {
		*target = *value
	}
}

func setBool(target *bool, value *bool) {
	if value != nil {
		*target = *value
	}
}

func boolOr(value *bool, fallback bool) bool {
	if value != nil {
		return *value
	}
	return fallback
}

func stringOr(value *string, fallback string) string {
	if value != nil {
		return *value
	}
	return fallback
}
