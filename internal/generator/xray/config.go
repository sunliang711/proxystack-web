package xray

import (
	"encoding/json"
	"fmt"
	"slices"

	"proxystack-web/internal/domain"
	"proxystack-web/internal/graph"
)

const outboundTagPrefix = "egress"
const privateDirectOutboundTagPrefix = "private-direct"

var privateDirectDomains = []string{
	"localhost",
	"domain:local",
}

var privateDirectIPs = []string{
	"127.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"100.64.0.0/10",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
}

// GeneratorError 表示 Xray 配置生成失败。
type GeneratorError struct {
	Message string
}

// Error 返回生成失败原因。
func (e GeneratorError) Error() string {
	return e.Message
}

// Config 是稳定 JSON 输出的顶层结构。
type Config struct {
	Log       LogConfig      `json:"log"`
	API       *APIConfig     `json:"api,omitempty"`
	Stats     *emptyObject   `json:"stats,omitempty"`
	Policy    *PolicyConfig  `json:"policy,omitempty"`
	Routing   *RoutingConfig `json:"routing,omitempty"`
	Inbounds  []any          `json:"inbounds"`
	Outbounds []any          `json:"outbounds"`
}

// LogConfig 保存 Xray 错误日志级别和独立的访问日志开关。
type LogConfig struct {
	LogLevel string `json:"loglevel"`
	Access   string `json:"access,omitempty"`
}

// APIConfig 保存 Xray API 输出配置。
type APIConfig struct {
	Tag      string   `json:"tag"`
	Listen   string   `json:"listen"`
	Services []string `json:"services"`
}

// PolicyConfig 保存 Xray policy 输出配置。
type PolicyConfig struct {
	Levels map[string]PolicyLevel `json:"levels,omitempty"`
	System PolicySystem           `json:"system"`
}

// PolicyLevel 保存单个 level 的用户统计开关。
type PolicyLevel struct {
	StatsUserUplink   *bool `json:"statsUserUplink,omitempty"`
	StatsUserDownlink *bool `json:"statsUserDownlink,omitempty"`
}

// PolicySystem 保存 system policy 的全局统计开关。
type PolicySystem struct {
	StatsInboundUplink    bool `json:"statsInboundUplink"`
	StatsInboundDownlink  bool `json:"statsInboundDownlink"`
	StatsOutboundUplink   bool `json:"statsOutboundUplink"`
	StatsOutboundDownlink bool `json:"statsOutboundDownlink"`
}

type emptyObject struct{}

type vmessInbound struct {
	Tag            string         `json:"tag"`
	Listen         string         `json:"listen"`
	Port           int            `json:"port"`
	Protocol       string         `json:"protocol"`
	Settings       vmessSettings  `json:"settings"`
	StreamSettings streamSettings `json:"streamSettings"`
	Sniffing       *Sniffing      `json:"sniffing,omitempty"`
}

type vmessSettings struct {
	Clients []vmessClient `json:"clients"`
}

type vmessClient struct {
	ID      string `json:"id"`
	AlterID int    `json:"alterId"`
	Email   string `json:"email"`
}

type streamSettings struct {
	Network      string              `json:"network"`
	WSSettings   *websocketSettings  `json:"wsSettings,omitempty"`
	GRPCSettings *grpcStreamSettings `json:"grpcSettings,omitempty"`
}

type websocketSettings struct {
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type grpcStreamSettings struct {
	ServiceName string `json:"serviceName,omitempty"`
}

type shadowsocksInbound struct {
	Tag      string              `json:"tag"`
	Listen   string              `json:"listen"`
	Port     int                 `json:"port"`
	Protocol string              `json:"protocol"`
	Settings shadowsocksSettings `json:"settings"`
	Sniffing *Sniffing           `json:"sniffing,omitempty"`
}

type shadowsocksSettings struct {
	Method   string              `json:"method"`
	Password string              `json:"password"`
	Network  string              `json:"network"`
	Clients  []shadowsocksClient `json:"clients,omitempty"`
}

type shadowsocksClient struct {
	Password string `json:"password"`
	Email    string `json:"email"`
	Method   string `json:"method,omitempty"`
}

type socksInbound struct {
	Tag      string        `json:"tag"`
	Listen   string        `json:"listen"`
	Port     int           `json:"port"`
	Protocol string        `json:"protocol"`
	Settings socksSettings `json:"settings"`
	Sniffing *Sniffing     `json:"sniffing,omitempty"`
}

type socksSettings struct {
	Auth     string    `json:"auth"`
	UDP      bool      `json:"udp"`
	Accounts []account `json:"accounts,omitempty"`
}

type httpInbound struct {
	Tag      string       `json:"tag"`
	Listen   string       `json:"listen"`
	Port     int          `json:"port"`
	Protocol string       `json:"protocol"`
	Settings httpSettings `json:"settings"`
	Sniffing *Sniffing    `json:"sniffing,omitempty"`
}

type httpSettings struct {
	Accounts []account `json:"accounts,omitempty"`
}

type account struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type proxyOutbound struct {
	Tag      string                `json:"tag"`
	Protocol string                `json:"protocol"`
	Settings proxyOutboundSettings `json:"settings"`
}

type proxyOutboundSettings struct {
	Servers []proxyServer `json:"servers"`
}

type proxyServer struct {
	Address string    `json:"address"`
	Port    int       `json:"port"`
	Users   []account `json:"users,omitempty"`
}

type directOutbound struct {
	Tag      string      `json:"tag"`
	Protocol string      `json:"protocol"`
	Settings emptyObject `json:"settings"`
}

// Sniffing 是入站的流量嗅探：从 HTTP Host、TLS SNI 和 QUIC 中识别目标域名，只用于路由匹配，不改写连接目标。
type Sniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
	RouteOnly    bool     `json:"routeOnly"`
}

// RoutingConfig 保存 Xray 路由规则，仅在需要本地分流或域名拦截时生成。
type RoutingConfig struct {
	Rules []RoutingRule `json:"rules"`
}

// RoutingRule 保存单条 Xray field 路由规则。
type RoutingRule struct {
	Type        string   `json:"type"`
	User        []string `json:"user,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

// RenderConfig 生成指定启用 stack 的 Xray 配置结构。
func RenderConfig(stackSet domain.StackSet, stackName string) (Config, error) {
	stackSet, err := domain.ResolveStackSetUserRefs(stackSet)
	if err != nil {
		return Config{}, err
	}
	stack, err := enabledXrayStack(stackSet, stackName)
	if err != nil {
		return Config{}, err
	}
	defaults := stackSet.Config.Defaults.Xray
	apiConfig := domain.ResolveXrayAPIConfig(defaults, stack.Xray)
	statsConfig := domain.ResolveXrayStatsConfig(defaults, stack.Xray)
	policyConfig := domain.ResolveXrayPolicyConfig(defaults, stack.Xray)
	referenceGraph, err := graph.BuildReferenceGraph(stackSet)
	if err != nil {
		return Config{}, err
	}
	config := Config{
		Log: LogConfig{LogLevel: domain.ResolveXrayLogLevel(defaults, stack.Xray)},
	}
	if !domain.ResolveXrayAccessLog(defaults, stack.Xray) {
		config.Log.Access = "none"
	}
	if apiConfig.Enabled {
		config.API = RenderAPI(apiConfig)
	}
	if statsConfig.Enabled {
		config.Stats = &emptyObject{}
	}
	if policyConfig.Enabled || statsConfig.Enabled {
		config.Policy = RenderPolicy(policyConfig, statsConfig.Enabled)
	}
	// 拦截规则按入站引用的全部用户生成，不受暂停影响：暂停经 API 热移除和热恢复用户，不重新生成配置。
	blockRules := RenderBlockRules(stackSet.Config, stack.Xray.Inbounds, BlockOutboundTag(stack.Name))
	config.Inbounds = make([]any, 0, len(stack.Xray.Inbounds))
	for _, inbound := range stack.Xray.Inbounds {
		inbound, err := applyDisabledUsers(inbound, stackSet.DisabledUsers, stack.Name, stack.Clash.Controller.Secret)
		if err != nil {
			return Config{}, err
		}
		rendered, err := RenderInbound(inbound)
		if err != nil {
			return Config{}, err
		}
		if len(blockRules) > 0 {
			rendered = withSniffing(rendered)
		}
		config.Inbounds = append(config.Inbounds, rendered)
	}
	outbounds, routing, err := RenderOutbounds(stack.Xray.Outbound, referenceGraph, stack.Name)
	if err != nil {
		return Config{}, err
	}
	if len(blockRules) > 0 {
		// 第一个 outbound 是默认出口，黑洞只能排在后面。
		outbounds = append(outbounds, directOutbound{Tag: BlockOutboundTag(stack.Name), Protocol: "blackhole", Settings: emptyObject{}})
		if routing == nil {
			routing = &RoutingConfig{}
		}
		routing.Rules = append(blockRules, routing.Rules...)
	}
	config.Routing = routing
	config.Outbounds = outbounds
	return config, nil
}

// BlockOutboundTag 生成域名拦截使用的黑洞 outbound tag。
func BlockOutboundTag(stackName string) string {
	return domain.BlockOutboundTagPrefix + stackName
}

// RenderBlockRules 生成域名拦截的路由规则：每个有个人名单的用户一条（匹配该用户在本 stack 入站中的全部 email），
// 全局名单一条（匹配全部连接）。没有任何名单时返回空。
func RenderBlockRules(global domain.GlobalConfig, inbounds []domain.Inbound, outboundTag string) []RoutingRule {
	var rules []RoutingRule
	emails := map[string][]string{}
	for _, inbound := range inbounds {
		for _, user := range inbound.Users {
			if len(global.UserBlockedDomains[user.User]) == 0 {
				continue
			}
			if email := user.StatsEmail(); !slices.Contains(emails[user.User], email) {
				emails[user.User] = append(emails[user.User], email)
			}
		}
	}
	names := make([]string, 0, len(emails))
	for name := range emails {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		users := append([]string(nil), emails[name]...)
		slices.Sort(users)
		rules = append(rules, RoutingRule{
			Type: "field", User: users, Domain: append([]string(nil), global.UserBlockedDomains[name]...), OutboundTag: outboundTag,
		})
	}
	if len(global.BlockedDomains) > 0 {
		rules = append(rules, RoutingRule{Type: "field", Domain: append([]string(nil), global.BlockedDomains...), OutboundTag: outboundTag})
	}
	return rules
}

// withSniffing 给入站开启只用于路由的流量嗅探，让客户端以 IP 发起的连接也能按域名匹配拦截规则。
func withSniffing(inbound any) any {
	sniffing := &Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}, RouteOnly: true}
	switch value := inbound.(type) {
	case vmessInbound:
		value.Sniffing = sniffing
		return value
	case shadowsocksInbound:
		value.Sniffing = sniffing
		return value
	case socksInbound:
		value.Sniffing = sniffing
		return value
	case httpInbound:
		value.Sniffing = sniffing
		return value
	}
	return inbound
}

// DumpsConfig 将指定 stack 的 Xray 配置编码为稳定、可读的 JSON 文本。
func DumpsConfig(stackSet domain.StackSet, stackName string) (string, error) {
	config, err := RenderConfig(stackSet, stackName)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

// RenderAPI 生成 Xray API 配置。
func RenderAPI(apiConfig domain.XrayAPIConfig) *APIConfig {
	return &APIConfig{
		Tag:      apiConfig.Tag,
		Listen:   apiConfig.Listen,
		Services: append([]string(nil), apiConfig.Services...),
	}
}

// RenderPolicy 生成 Xray policy 配置，stats 开启时默认启用全局统计项。
func RenderPolicy(policyConfig domain.XrayPolicyConfig, statsEnabled bool) *PolicyConfig {
	policy := &PolicyConfig{
		Levels: RenderPolicyLevels(policyConfig.Levels),
		System: RenderPolicySystem(policyConfig.System, statsEnabled),
	}
	return policy
}

// RenderPolicyLevels 生成 policy levels 的用户流量统计开关。
func RenderPolicyLevels(levelsConfig map[string]domain.XrayPolicyLevelConfig) map[string]PolicyLevel {
	levels := make(map[string]PolicyLevel)
	for level, levelConfig := range levelsConfig {
		rendered := PolicyLevel{
			StatsUserUplink:   levelConfig.StatsUserUplink,
			StatsUserDownlink: levelConfig.StatsUserDownlink,
		}
		if rendered.StatsUserUplink != nil || rendered.StatsUserDownlink != nil {
			levels[level] = rendered
		}
	}
	if len(levels) == 0 {
		return nil
	}
	return levels
}

// RenderPolicySystem 生成 system policy 的四个全局流量统计开关。
func RenderPolicySystem(systemConfig domain.XrayPolicySystemConfig, statsEnabled bool) PolicySystem {
	return PolicySystem{
		StatsInboundUplink:    boolOrDefault(systemConfig.StatsInboundUplink, statsEnabled),
		StatsInboundDownlink:  boolOrDefault(systemConfig.StatsInboundDownlink, statsEnabled),
		StatsOutboundUplink:   boolOrDefault(systemConfig.StatsOutboundUplink, statsEnabled),
		StatsOutboundDownlink: boolOrDefault(systemConfig.StatsOutboundDownlink, statsEnabled),
	}
}

// RenderInbound 按 inbound 协议分发生成 Xray inbound 配置。
func RenderInbound(inbound domain.Inbound) (any, error) {
	switch inbound.Protocol {
	case "vmess":
		return renderVmessInbound(inbound), nil
	case "shadowsocks":
		return renderShadowsocksInbound(inbound), nil
	case "socks5":
		return renderSocksInbound(inbound)
	case "http":
		return renderHTTPInbound(inbound)
	default:
		return nil, GeneratorError{Message: "unsupported xray inbound protocol: " + inbound.Protocol}
	}
}

// RenderOutbound 按 xray outbound 类型生成 Xray outbound 配置。
func RenderOutbound(outbound domain.XrayOutbound, referenceGraph graph.ReferenceGraph, stackName string) (any, error) {
	outboundTag := OutboundTag(stackName)
	switch outbound.Type {
	case "clash":
		return renderClashOutbound(outbound, referenceGraph, outboundTag)
	case "socks5":
		return renderProxyOutbound("socks", outbound.Server, outbound.Port, outbound.Username, outbound.Password, outboundTag)
	case "http":
		return renderProxyOutbound("http", outbound.Server, outbound.Port, outbound.Username, outbound.Password, outboundTag)
	case "direct":
		return directOutbound{Tag: outboundTag, Protocol: "freedom", Settings: emptyObject{}}, nil
	default:
		return nil, GeneratorError{Message: "unsupported xray outbound type: " + outbound.Type}
	}
}

// RenderOutbounds 生成 Xray outbound 列表，并在 Xray-only 代理出口启用私网直连时追加 routing。
func RenderOutbounds(outbound domain.XrayOutbound, referenceGraph graph.ReferenceGraph, stackName string) ([]any, *RoutingConfig, error) {
	rendered, err := RenderOutbound(outbound, referenceGraph, stackName)
	if err != nil {
		return nil, nil, err
	}
	if !outbound.PrivateDirect || (outbound.Type != "socks5" && outbound.Type != "http") {
		return []any{rendered}, nil, nil
	}
	privateDirectTag := PrivateDirectOutboundTag(stackName)
	return []any{
		rendered,
		directOutbound{Tag: privateDirectTag, Protocol: "freedom", Settings: emptyObject{}},
	}, RenderPrivateDirectRouting(privateDirectTag), nil
}

// OutboundTag 生成包含 stack 名的 Xray 出口 tag。
func OutboundTag(stackName string) string {
	return outboundTagPrefix + "-" + stackName
}

// PrivateDirectOutboundTag 生成私网直连 outbound tag。
func PrivateDirectOutboundTag(stackName string) string {
	return privateDirectOutboundTagPrefix + "-" + stackName
}

// RenderPrivateDirectRouting 生成本机、私网和 CGNAT 网段直连的 Xray 路由。
func RenderPrivateDirectRouting(outboundTag string) *RoutingConfig {
	return &RoutingConfig{
		Rules: []RoutingRule{
			{
				Type:        "field",
				Domain:      append([]string(nil), privateDirectDomains...),
				OutboundTag: outboundTag,
			},
			{
				Type:        "field",
				IP:          append([]string(nil), privateDirectIPs...),
				OutboundTag: outboundTag,
			},
		},
	}
}

// NormalizeInternalEndpointAddress 把本机 wildcard listener 地址归一为 loopback。
func NormalizeInternalEndpointAddress(address string) string {
	return domain.NormalizeInternalEndpointAddress(address)
}

func enabledXrayStack(stackSet domain.StackSet, stackName string) (domain.Stack, error) {
	stack, ok := stackSet.ByName()[stackName]
	if !ok {
		return domain.Stack{}, GeneratorError{Message: "stack does not exist: " + stackName}
	}
	if !stack.Enabled {
		return domain.Stack{}, GeneratorError{Message: "stack is disabled: " + stackName}
	}
	if !stack.Xray.Enabled {
		return domain.Stack{}, GeneratorError{Message: "xray is disabled: " + stackName}
	}
	return *stack, nil
}

func renderVmessInbound(inbound domain.Inbound) vmessInbound {
	clients := make([]vmessClient, 0, len(inbound.Users))
	for _, user := range inbound.Users {
		clients = append(clients, vmessClient{ID: user.UUID, AlterID: 0, Email: user.StatsEmail()})
	}
	stream := streamSettings{Network: inbound.Network}
	if inbound.WSOpts != nil {
		stream.WSSettings = &websocketSettings{
			Path:    inbound.WSOpts.Path,
			Headers: inbound.WSOpts.Headers,
		}
	}
	if inbound.GRPCOpts != nil {
		stream.GRPCSettings = &grpcStreamSettings{ServiceName: inbound.GRPCOpts.ServiceName}
	}
	return vmessInbound{
		Tag:      inbound.TagOrDefault(),
		Listen:   inbound.Listen,
		Port:     inbound.Port,
		Protocol: "vmess",
		Settings: vmessSettings{
			Clients: clients,
		},
		StreamSettings: stream,
	}
}

func renderShadowsocksInbound(inbound domain.Inbound) shadowsocksInbound {
	network := "tcp"
	if inbound.UDPEnabled() {
		network = "tcp,udp"
	}
	settings := shadowsocksSettings{
		Method:   inbound.Method,
		Password: inbound.Password,
		Network:  network,
	}
	if len(inbound.Users) > 0 {
		settings.Clients = make([]shadowsocksClient, 0, len(inbound.Users))
		for _, user := range inbound.Users {
			client := shadowsocksClient{
				Password: user.Password,
				Email:    user.StatsEmail(),
			}
			if !domain.IsShadowsocks2022Method(inbound.Method) {
				client.Method = inbound.Method
			}
			settings.Clients = append(settings.Clients, client)
		}
	}
	return shadowsocksInbound{
		Tag:      inbound.TagOrDefault(),
		Listen:   inbound.Listen,
		Port:     inbound.Port,
		Protocol: "shadowsocks",
		Settings: settings,
	}
}

func renderSocksInbound(inbound domain.Inbound) (socksInbound, error) {
	authType := inboundAuthType(inbound)
	settings := socksSettings{Auth: authType, UDP: inbound.UDPEnabled()}
	if authType == "password" {
		acct, err := inboundAccount(inbound)
		if err != nil {
			return socksInbound{}, err
		}
		settings.Accounts = []account{acct}
	}
	return socksInbound{
		Tag:      inbound.TagOrDefault(),
		Listen:   inbound.Listen,
		Port:     inbound.Port,
		Protocol: "socks",
		Settings: settings,
	}, nil
}

func renderHTTPInbound(inbound domain.Inbound) (httpInbound, error) {
	settings := httpSettings{}
	if inboundAuthType(inbound) == "password" {
		acct, err := inboundAccount(inbound)
		if err != nil {
			return httpInbound{}, err
		}
		settings.Accounts = []account{acct}
	}
	return httpInbound{
		Tag:      inbound.TagOrDefault(),
		Listen:   inbound.Listen,
		Port:     inbound.Port,
		Protocol: "http",
		Settings: settings,
	}, nil
}

func renderClashOutbound(outbound domain.XrayOutbound, referenceGraph graph.ReferenceGraph, outboundTag string) (proxyOutbound, error) {
	endpoint, ok := referenceGraph.Index.ResolveClashListener(outbound.Ref)
	if !ok {
		return proxyOutbound{}, GeneratorError{Message: "clash listener ref does not exist: " + outbound.Ref}
	}
	if endpoint.Kind != "socks" {
		return proxyOutbound{}, GeneratorError{Message: "clash listener ref must target socks listener: " + outbound.Ref}
	}
	username, password := firstEndpointUser(endpoint)
	return renderProxyOutbound("socks", domain.NormalizeInternalEndpointAddress(endpoint.Listen), endpoint.Port, username, password, outboundTag)
}

func renderProxyOutbound(protocol string, address string, port int, username string, password string, outboundTag string) (proxyOutbound, error) {
	if address == "" || port == 0 {
		return proxyOutbound{}, GeneratorError{Message: fmt.Sprintf("%s outbound requires address and port", protocol)}
	}
	server := proxyServer{Address: address, Port: port}
	if username != "" || password != "" {
		if username == "" || password == "" {
			return proxyOutbound{}, GeneratorError{Message: fmt.Sprintf("%s outbound username and password must be provided together", protocol)}
		}
		server.Users = []account{{User: username, Pass: password}}
	}
	return proxyOutbound{
		Tag:      outboundTag,
		Protocol: protocol,
		Settings: proxyOutboundSettings{
			Servers: []proxyServer{server},
		},
	}, nil
}

func inboundAuthType(inbound domain.Inbound) string {
	if inbound.Auth == nil {
		return "noauth"
	}
	return inbound.Auth.Type
}

func inboundAccount(inbound domain.Inbound) (account, error) {
	if inbound.Auth == nil || inbound.Auth.Username == "" || inbound.Auth.Password == "" {
		return account{}, GeneratorError{Message: "password auth account is incomplete: " + inbound.Name}
	}
	return account{User: inbound.Auth.Username, Pass: inbound.Auth.Password}, nil
}

func firstEndpointUser(endpoint graph.Endpoint) (string, string) {
	if len(endpoint.Users) == 0 {
		return "", ""
	}
	return endpoint.Users[0].Username, endpoint.Users[0].Password
}

func boolOrDefault(value *bool, defaultValue bool) bool {
	if value == nil {
		return defaultValue
	}
	return *value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
