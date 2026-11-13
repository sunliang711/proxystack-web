package domain

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultXrayLogLevel      = "warning"
	defaultClashLogLevel     = "info"
	defaultClashMode         = "Rule"
	defaultRuleProfile       = "default"
	defaultXrayAPITag        = "api"
	defaultXrayAPIListen     = "127.0.0.1:10085"
	defaultStackRole         = "edge"
	defaultClashFinal        = "AllProxy"
	defaultListenHost        = "0.0.0.0"
	defaultClashListenerHost = "127.0.0.1"
)

// DefaultUserProfile 是未显式配置 profile 时使用的默认档案名。
const DefaultUserProfile = "default"

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	regionPattern     = regexp.MustCompile(`^[A-Z]{2}$`)
	uuidPattern       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
)

var shadowsocks2022KeyLengths = map[string]int{
	"2022-blake3-aes-128-gcm":       16,
	"2022-blake3-aes-256-gcm":       32,
	"2022-blake3-chacha20-poly1305": 32,
}

// PortRange 表示一个闭区间端口池，支持 YAML 中的 start-end 字符串写法。
type PortRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Validate 校验端口范围边界和顺序。
func (p PortRange) Validate() error {
	if err := ValidatePort(p.Start, "port range start"); err != nil {
		return err
	}
	if err := ValidatePort(p.End, "port range end"); err != nil {
		return err
	}
	if p.Start > p.End {
		return fmt.Errorf("port range start must be less than or equal to end")
	}
	return nil
}

// Allocate 从端口池中稳定分配指定数量的未使用端口。
func (p PortRange) Allocate(usedPorts map[int]bool, count int) ([]int, error) {
	allocated := make([]int, 0, count)
	unavailable := make(map[int]bool, len(usedPorts)+count)
	for port, used := range usedPorts {
		unavailable[port] = used
	}
	for port := p.Start; port <= p.End; port++ {
		if unavailable[port] {
			continue
		}
		allocated = append(allocated, port)
		unavailable[port] = true
		if len(allocated) == count {
			return allocated, nil
		}
	}
	return nil, fmt.Errorf("not enough available ports in range")
}

// PortRanges 保存所有自动分配端口池。
type PortRanges struct {
	XrayInbound    PortRange `json:"xray_inbound"`
	ClashSocks     PortRange `json:"clash_socks"`
	ClashHTTP      PortRange `json:"clash_http"`
	XrayAPIRange   PortRange `json:"xray_api_range"`
	ClashControler PortRange `json:"clash_controller"`
}

// Validate 校验所有端口池均为合法范围。
func (p PortRanges) Validate() error {
	ranges := map[string]PortRange{
		"xray_inbound":     p.XrayInbound,
		"clash_socks":      p.ClashSocks,
		"clash_http":       p.ClashHTTP,
		"xray_api_range":   p.XrayAPIRange,
		"clash_controller": p.ClashControler,
	}
	for name, portRange := range ranges {
		if err := portRange.Validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// DefaultClashConfig 保存 mihomo 默认配置。
type DefaultClashConfig struct {
	Mode        string `json:"mode"`
	LogLevel    string `json:"loglevel"`
	RuleProfile string `json:"rule_profile"`
}

// XrayAPIConfig 保存 Xray API 配置，并记录 YAML 中显式写入的字段。
type XrayAPIConfig struct {
	Enabled  bool     `json:"enabled"`
	Tag      string   `json:"tag"`
	Listen   string   `json:"listen"`
	Services []string `json:"services"`
}

// DefaultXrayAPIConfig 返回 Xray API 默认配置。
func DefaultXrayAPIConfig() XrayAPIConfig {
	return XrayAPIConfig{
		Enabled:  true,
		Tag:      defaultXrayAPITag,
		Listen:   defaultXrayAPIListen,
		Services: []string{"HandlerService", "StatsService"},
	}
}

// HasService 判断 API 是否启用了指定服务。
func (x XrayAPIConfig) HasService(name string) bool {
	for _, service := range x.Services {
		if service == name {
			return true
		}
	}
	return false
}

// XrayStatsConfig 保存 Xray stats 开关。
type XrayStatsConfig struct {
	Enabled bool `json:"enabled"`
}

// XrayPolicySystemConfig 保存 Xray system policy 统计开关。
type XrayPolicySystemConfig struct {
	StatsInboundUplink    *bool `json:"statsInboundUplink"`
	StatsInboundDownlink  *bool `json:"statsInboundDownlink"`
	StatsOutboundUplink   *bool `json:"statsOutboundUplink"`
	StatsOutboundDownlink *bool `json:"statsOutboundDownlink"`
}

// XrayPolicyLevelConfig 保存 Xray level policy 用户统计开关。
type XrayPolicyLevelConfig struct {
	StatsUserUplink   *bool `json:"statsUserUplink"`
	StatsUserDownlink *bool `json:"statsUserDownlink"`
}

// XrayPolicyConfig 保存 Xray policy 配置。
type XrayPolicyConfig struct {
	Enabled bool                             `json:"enabled"`
	Levels  map[string]XrayPolicyLevelConfig `json:"levels"`
	System  XrayPolicySystemConfig           `json:"system"`
}

// DefaultXrayPolicyConfig 返回默认 Xray policy 配置。
func DefaultXrayPolicyConfig() XrayPolicyConfig {
	yes := true
	return XrayPolicyConfig{
		Enabled: true,
		Levels: map[string]XrayPolicyLevelConfig{
			"0": {
				StatsUserUplink:   &yes,
				StatsUserDownlink: &yes,
			},
		},
		System: XrayPolicySystemConfig{},
	}
}

// DefaultXrayConfig 保存 xray 默认值。
type DefaultXrayConfig struct {
	AccessLog *bool            `json:"access_log,omitempty"`
	LogLevel  string           `json:"loglevel"`
	API       XrayAPIConfig    `json:"api"`
	Stats     XrayStatsConfig  `json:"stats"`
	Policy    XrayPolicyConfig `json:"policy"`
}

// DefaultsConfig 保存全局默认值配置。
type DefaultsConfig struct {
	Clash DefaultClashConfig `json:"clash"`
	Xray  DefaultXrayConfig  `json:"xray"`
}

// DefaultDefaultsConfig 返回完整默认值配置。
func DefaultDefaultsConfig() DefaultsConfig {
	return DefaultsConfig{
		Clash: DefaultClashConfig{
			Mode:        defaultClashMode,
			LogLevel:    defaultClashLogLevel,
			RuleProfile: defaultRuleProfile,
		},
		Xray: DefaultXrayConfig{
			LogLevel: defaultXrayLogLevel,
			API:      DefaultXrayAPIConfig(),
			Stats:    XrayStatsConfig{Enabled: true},
			Policy:   DefaultXrayPolicyConfig(),
		},
	}
}

// SecurityConfig 保存安全校验开关。
type SecurityConfig struct {
	RequireAuthForPublicSocksHTTP bool `json:"require_auth_for_public_socks_http"`
	AllowNoAuthPublic             bool `json:"allow_noauth_public"`
}

// DefaultSecurityConfig 返回默认安全开关：公开的 socks/http 入站必须带密码认证。
func DefaultSecurityConfig() SecurityConfig {
	return SecurityConfig{RequireAuthForPublicSocksHTTP: true}
}

// GlobalConfig 保存生成器需要的全局设置和全部订阅用户档案。
type GlobalConfig struct {
	ExternalHost                string         `json:"external_host"`
	SubscriptionDisplayTemplate string         `json:"subscription_display_template"`
	Users                       []UserProfile  `json:"users"`
	PortRanges                  PortRanges     `json:"port_ranges"`
	Defaults                    DefaultsConfig `json:"defaults"`
	Security                    SecurityConfig `json:"security"`
	// BlockedDomains 是对全部连接生效的拦截规则，UserBlockedDomains 按用户名给出只对该用户生效的拦截规则；
	// 规则使用 xray 路由的域名匹配写法（domain:example.com、full:www.example.com）。
	BlockedDomains     []string            `json:"blocked_domains,omitempty"`
	UserBlockedDomains map[string][]string `json:"user_blocked_domains,omitempty"`
}

// Validate 校验全局配置字段和默认值枚举。
func (g *GlobalConfig) Validate() error {
	if err := normalizeAndValidateUserProfiles(g.Users); err != nil {
		return err
	}
	if err := g.PortRanges.Validate(); err != nil {
		return err
	}
	if err := validateDefaultClash(g.Defaults.Clash); err != nil {
		return err
	}
	if err := validateDefaultXray(g.Defaults.Xray); err != nil {
		return err
	}
	return nil
}

// UserProfile 保存 config.yaml 中可被 stack 引用的全局订阅用户档案。
type UserProfile struct {
	User            string `json:"user"`
	Profile         string `json:"profile"`
	Email           string `json:"email,omitempty"`
	UUID            string `json:"uuid"`
	Password        string `json:"password"`
	Remark          string `json:"remark"`
	DisplayTemplate string `json:"display_template"`
	Tag             string `json:"tag"`
}

// Validate 校验全局用户档案的唯一键和可选凭据格式。
func (u *UserProfile) Validate() error {
	u.Profile = NormalizeUserProfile(u.Profile)
	if err := ValidateIdentifier(u.User, "config user"); err != nil {
		return err
	}
	if err := ValidateIdentifier(u.Profile, "user profile"); err != nil {
		return err
	}
	if u.UUID != "" && !uuidPattern.MatchString(u.UUID) {
		return fmt.Errorf("uuid must be a valid UUID")
	}
	return nil
}

// UserProfileKey 返回全局用户档案的稳定唯一键。
func UserProfileKey(user string, profile string) string {
	return user + "\x00" + NormalizeUserProfile(profile)
}

// NormalizeUserProfile 把空 profile 归一为 default。
func NormalizeUserProfile(profile string) string {
	if profile == "" {
		return DefaultUserProfile
	}
	return profile
}

// normalizeAndValidateUserProfiles 校验全局用户档案并补齐默认 profile。
func normalizeAndValidateUserProfiles(users []UserProfile) error {
	seen := map[string]bool{}
	for index := range users {
		if err := users[index].Validate(); err != nil {
			return err
		}
		key := UserProfileKey(users[index].User, users[index].Profile)
		if seen[key] {
			return fmt.Errorf("duplicate config user profile: user=%s profile=%s", users[index].User, users[index].Profile)
		}
		seen[key] = true
	}
	return nil
}

// InboundAuth 保存 socks/http inbound 鉴权配置。
type InboundAuth struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// Validate 校验 password 模式必须同时配置用户名和密码。
func (a InboundAuth) Validate() error {
	if a.Type != "noauth" && a.Type != "password" {
		return fmt.Errorf("auth.type must be noauth or password")
	}
	if a.Type == "password" && (a.Username == "" || a.Password == "") {
		return fmt.Errorf("username and password are required for password auth")
	}
	return nil
}

// InboundUser 保存 vmess/shadowsocks 多用户凭据。
type InboundUser struct {
	User            string `json:"user"`
	Profile         string `json:"profile"`
	Email           string `json:"email,omitempty"`
	UUID            string `json:"uuid"`
	Password        string `json:"password"`
	Remark          string `json:"remark"`
	DisplayTemplate string `json:"display_template"`
	Tag             string `json:"tag"`
}

// StatsEmail 返回写入 xray 的 email，也是 xray 统计计数中的用户标识；未设置时使用用户名。
func (u InboundUser) StatsEmail() string {
	if u.Email != "" {
		return u.Email
	}
	return u.User
}

// BlockOutboundTagPrefix 是域名拦截使用的黑洞 outbound tag 前缀，后接 stack 名；访问日志按它识别被拦截的连接。
const BlockOutboundTagPrefix = "block-"

// UserStatsEmail 返回用户（或其档案）在 xray 中的 email。用户名方便查看日志，ID 后缀保证改名后统计仍然连续。
// userID 为 0（尚未入库）时返回空字符串；用户名为空时返回旧格式，供运行中实例兼容使用。
func UserStatsEmail(userName string, userID, profileID int64) string {
	if userID == 0 {
		return ""
	}
	email := "u" + strconv.FormatInt(userID, 10)
	if profileID != 0 {
		email += ".p" + strconv.FormatInt(profileID, 10)
	}
	if userName != "" {
		email = userName + "." + email
	}
	return email
}

// ParseUserStatsEmail 解析 UserStatsEmail 生成的 email；其他内容（例如占位用户）返回 false。
func ParseUserStatsEmail(email string) (userID, profileID int64, ok bool) {
	// 只解析最后的 ID 后缀，用户名中的点号不影响归属，并兼容旧的 u7 / u7.p3 格式。
	if index := strings.LastIndex(email, ".u"); index >= 0 {
		email = email[index+1:]
	}
	rest, ok := strings.CutPrefix(email, "u")
	if !ok {
		return 0, 0, false
	}
	userPart, profilePart, hasProfile := strings.Cut(rest, ".p")
	userID, err := strconv.ParseInt(userPart, 10, 64)
	if err != nil || userID <= 0 {
		return 0, 0, false
	}
	if hasProfile {
		if profileID, err = strconv.ParseInt(profilePart, 10, 64); err != nil || profileID <= 0 {
			return 0, 0, false
		}
	}
	return userID, profileID, true
}

// ProfileOrDefault 返回用户档案 profile，未配置时使用 default。
func (u InboundUser) ProfileOrDefault() string {
	return NormalizeUserProfile(u.Profile)
}

// InboundUserRef 保存 inbound 对用户档案的引用和展示字段覆盖。
type InboundUserRef struct {
	User            string `json:"user"`
	Profile         string `json:"profile"`
	Remark          string `json:"remark"`
	DisplayTemplate string `json:"display_template"`
	Tag             string `json:"tag"`
}

// Validate 校验 user_ref 的引用键和可选覆盖凭据格式。
func (r *InboundUserRef) Validate() error {
	r.Profile = NormalizeUserProfile(r.Profile)
	if err := ValidateIdentifier(r.User, "user ref"); err != nil {
		return err
	}
	return ValidateIdentifier(r.Profile, "user ref profile")
}

// Key 返回 user_ref 指向的全局用户档案键。
func (r InboundUserRef) Key() string {
	return UserProfileKey(r.User, r.Profile)
}

// WebSocketOptions 保存 vmess websocket 传输参数。
type WebSocketOptions struct {
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// GRPCOptions 保存 vmess grpc 传输参数。
type GRPCOptions struct {
	ServiceName string `json:"service_name,omitempty"`
}

// Inbound 保存 xray inbound 配置。
type Inbound struct {
	Name            string            `json:"name"`
	Protocol        string            `json:"protocol"`
	Listen          string            `json:"listen"`
	Port            int               `json:"port"`
	UDP             *bool             `json:"udp"`
	Auth            *InboundAuth      `json:"auth"`
	User            string            `json:"user"`
	Server          string            `json:"server"`
	Remark          string            `json:"remark"`
	DisplayTemplate string            `json:"display_template"`
	Region          string            `json:"region"`
	Tag             string            `json:"tag"`
	Sub             bool              `json:"sub"`
	Users           []InboundUser     `json:"users"`
	UserRefs        []InboundUserRef  `json:"user_refs"`
	Network         string            `json:"network"`
	Password        string            `json:"password"`
	Method          string            `json:"method"`
	WSOpts          *WebSocketOptions `json:"ws_opts"`
	GRPCOpts        *GRPCOptions      `json:"grpc_opts"`

	// FromUserRefs 标记 Users 由 UserRefs 展开而来，校验路径和提示按 user_refs 描述。
	FromUserRefs bool `json:"-"`
}

// TagOrDefault 返回显式 tag 或协议端口名称组成的默认 tag。
func (i Inbound) TagOrDefault() string {
	if i.Tag != "" {
		return i.Tag
	}
	return fmt.Sprintf("%s:%d:%s", i.Protocol, i.Port, i.Name)
}

// UDPConfigured 判断 inbound 是否显式配置了 udp 字段。
func (i Inbound) UDPConfigured() bool {
	return i.UDP != nil
}

// UDPEnabled 判断 inbound 是否开启 UDP。
func (i Inbound) UDPEnabled() bool {
	return i.UDP != nil && *i.UDP
}

// Validate 校验 inbound 的协议字段、凭据和订阅暴露约束。
func (i Inbound) Validate() error {
	if err := ValidateIdentifier(i.Name, "inbound name"); err != nil {
		return err
	}
	if !validInboundProtocol(i.Protocol) {
		return fmt.Errorf("unsupported inbound protocol: %s", i.Protocol)
	}
	if err := ValidatePort(i.Port, "inbound port"); err != nil {
		return err
	}
	if i.Region != "" && !regionPattern.MatchString(i.Region) {
		return fmt.Errorf("region must use two uppercase letters")
	}
	if i.UDPConfigured() && !SupportsInboundUDPProtocol(i.Protocol) {
		return fmt.Errorf("udp is not supported for %s inbound", i.Protocol)
	}
	if i.Auth != nil {
		if err := i.Auth.Validate(); err != nil {
			return err
		}
	}
	if len(i.UserRefs) > 0 {
		if i.User != "" || len(i.Users) > 0 {
			return fmt.Errorf("user_refs must not be combined with user or users")
		}
		if err := validateInboundUserRefs(i.UserRefs); err != nil {
			return err
		}
	}
	if len(i.Users) > 0 && !i.FromUserRefs && i.Protocol != "vmess" && i.Protocol != "shadowsocks" {
		return fmt.Errorf("users is only supported for vmess or shadowsocks inbound")
	}
	switch i.Protocol {
	case "vmess":
		return i.validateVmess()
	case "shadowsocks":
		return i.validateShadowsocks()
	case "socks5", "http":
		if len(i.Users) > 0 {
			if err := ensureUnique(userValues(i.Users, func(user InboundUser) string { return user.User }), "duplicate inbound user"); err != nil {
				return err
			}
		}
		if i.Sub && (i.Auth == nil || i.Auth.Type != "password") {
			return fmt.Errorf("password auth is required when socks/http inbound is published")
		}
	}
	return nil
}

// validateVmess 校验 vmess 多用户字段。
func (i Inbound) validateVmess() error {
	if i.User != "" || (i.Remark != "" && !i.UsesUserRefs()) {
		return fmt.Errorf("user and remark must be configured under vmess user_refs")
	}
	if len(i.Users) == 0 && len(i.UserRefs) == 0 {
		return fmt.Errorf("user_refs is required for vmess inbound")
	}
	if i.Network == "" {
		return fmt.Errorf("network is required for vmess inbound")
	}
	if err := ensureUnique(userValues(i.Users, func(user InboundUser) string { return user.User }), "duplicate vmess user"); err != nil {
		return err
	}
	uuids := make([]string, 0, len(i.Users))
	tags := make([]string, 0, len(i.Users))
	baseTag := i.TagOrDefault()
	for _, user := range i.Users {
		if err := ValidateIdentifier(user.User, "vmess user"); err != nil {
			return err
		}
		if user.UUID == "" {
			return fmt.Errorf("uuid is required for vmess user")
		}
		if !uuidPattern.MatchString(user.UUID) {
			return fmt.Errorf("uuid must be a valid UUID")
		}
		uuids = append(uuids, strings.ToLower(user.UUID))
		if user.Tag != "" {
			tags = append(tags, user.Tag)
		} else {
			tags = append(tags, baseTag+":"+user.User)
		}
	}
	if err := ensureUnique(uuids, "duplicate vmess uuid"); err != nil {
		return err
	}
	return ensureUnique(tags, "duplicate vmess user tag")
}

// validateShadowsocks 校验 shadowsocks 单用户或多用户字段。
func (i Inbound) validateShadowsocks() error {
	if i.Password == "" {
		return fmt.Errorf("password is required for shadowsocks inbound")
	}
	method := i.Method
	if method == "" {
		return fmt.Errorf("method is required for shadowsocks inbound")
	}
	if err := i.validateShadowsocks2022Passwords(method); err != nil {
		return err
	}
	if len(i.Users) == 0 {
		return nil
	}
	if i.User != "" || (i.Remark != "" && !i.UsesUserRefs()) {
		return fmt.Errorf("user and remark must be configured under shadowsocks user_refs")
	}
	if err := ensureUnique(userValues(i.Users, func(user InboundUser) string { return user.User }), "duplicate shadowsocks user"); err != nil {
		return err
	}
	tags := make([]string, 0, len(i.Users))
	baseTag := i.TagOrDefault()
	for _, user := range i.Users {
		if err := ValidateIdentifier(user.User, "shadowsocks user"); err != nil {
			return err
		}
		if user.Password == "" {
			return fmt.Errorf("password is required for shadowsocks user")
		}
		if user.Tag != "" {
			tags = append(tags, user.Tag)
		} else {
			tags = append(tags, baseTag+":"+user.User)
		}
	}
	return ensureUnique(tags, "duplicate shadowsocks user tag")
}

// UsesUserRefs 判断 inbound 当前或展开前是否使用了 user_refs。
func (i Inbound) UsesUserRefs() bool {
	return len(i.UserRefs) > 0 || i.FromUserRefs
}

// validateInboundUserRefs 校验单个 inbound 内 user_refs 的基本格式和重复引用。
func validateInboundUserRefs(refs []InboundUserRef) error {
	keys := make([]string, 0, len(refs))
	for index := range refs {
		if err := refs[index].Validate(); err != nil {
			return err
		}
		keys = append(keys, refs[index].Key())
	}
	return ensureUnique(keys, "duplicate user_ref")
}

// validateShadowsocks2022Passwords 校验 SS2022 server/user PSK。
func (i Inbound) validateShadowsocks2022Passwords(method string) error {
	if !IsShadowsocks2022Method(method) {
		return nil
	}
	if err := ValidateShadowsocks2022PSK(i.Password, method, "shadowsocks 2022 inbound password"); err != nil {
		return err
	}
	for _, user := range i.Users {
		if user.Password == "" {
			continue
		}
		if err := ValidateShadowsocks2022PSK(user.Password, method, "shadowsocks 2022 user password for "+user.User); err != nil {
			return err
		}
	}
	return nil
}

// XrayOutbound 保存 xray egress 配置。
type XrayOutbound struct {
	Type          string `json:"type"`
	Ref           string `json:"ref"`
	Server        string `json:"server"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	PrivateDirect bool   `json:"private_direct"`
}

// Validate 校验 outbound 类型和对应目标字段。
func (x XrayOutbound) Validate() error {
	if x.PrivateDirect && x.Type != "socks5" && x.Type != "http" {
		return fmt.Errorf("private_direct is only supported for socks5/http outbound")
	}
	switch x.Type {
	case "clash":
		return ValidateRef(x.Ref, 3, "clash outbound ref is required")
	case "socks5", "http":
		if x.Server == "" || x.Port == 0 {
			return fmt.Errorf("server and port are required for socks5/http outbound")
		}
		return ValidatePort(x.Port, "outbound port")
	case "direct":
		return nil
	default:
		return fmt.Errorf("unsupported outbound type: %s", x.Type)
	}
}

// XrayConfig 保存单个 stack 的 xray 配置。
type XrayConfig struct {
	AccessLog *bool               `json:"access_log,omitempty"`
	Enabled   bool                `json:"enabled"`
	LogLevel  string              `json:"loglevel"`
	Outbound  XrayOutbound        `json:"outbound"`
	Inbounds  []Inbound           `json:"inbounds"`
	API       *XrayAPIOverride    `json:"api"`
	Stats     *XrayStatsOverride  `json:"stats"`
	Policy    *XrayPolicyOverride `json:"policy"`
}

// XrayAPIOverride 保存 stack 级 API 覆盖，nil 字段表示沿用全局默认值。
type XrayAPIOverride struct {
	Enabled  *bool    `json:"enabled,omitempty"`
	Tag      *string  `json:"tag,omitempty"`
	Listen   *string  `json:"listen,omitempty"`
	Services []string `json:"services,omitempty"`
}

// XrayStatsOverride 保存 stack 级 stats 覆盖，nil 字段表示沿用全局默认值。
type XrayStatsOverride struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// XrayPolicyOverride 保存 stack 级 policy 覆盖；Levels 为 nil、System 为 nil 时沿用全局默认值，
// 非 nil 时逐个字段覆盖（字段本身为 nil 的保持默认）。
type XrayPolicyOverride struct {
	Enabled *bool                            `json:"enabled,omitempty"`
	Levels  map[string]XrayPolicyLevelConfig `json:"levels,omitempty"`
	System  *XrayPolicySystemConfig          `json:"system,omitempty"`
}

// Validate 校验 xray 配置和 inbound 唯一性。
func (x XrayConfig) Validate() error {
	if x.LogLevel != "" && !validXrayLogLevel(x.LogLevel) {
		return fmt.Errorf("xray loglevel is invalid: %s", x.LogLevel)
	}
	if x.API != nil {
		if err := validateAPIConfig(mergeAPIOverride(DefaultXrayAPIConfig(), *x.API)); err != nil {
			return err
		}
	}
	if err := x.Outbound.Validate(); err != nil {
		return err
	}
	if len(x.Inbounds) == 0 {
		return fmt.Errorf("inbounds is required for xray")
	}
	names := make([]string, 0, len(x.Inbounds))
	for _, inbound := range x.Inbounds {
		if err := inbound.Validate(); err != nil {
			return err
		}
		names = append(names, inbound.Name)
	}
	return ensureUnique(names, "duplicate inbound name")
}

// ClashController 保存 mihomo controller 配置。
type ClashController struct {
	Listen string `json:"listen"`
	Secret string `json:"secret"`
}

// Validate 校验 controller 监听地址和密钥。
func (c ClashController) Validate() error {
	if _, _, err := ParseListen(c.Listen); err != nil {
		return err
	}
	if c.Secret == "" {
		return fmt.Errorf("controller secret is required")
	}
	return nil
}

// ClashListenerUser 保存 mihomo 高级 listener 认证用户。
type ClashListenerUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Validate 校验 listener 用户名和密码。
func (c ClashListenerUser) Validate() error {
	if err := ValidateIdentifier(c.Username, "listener username"); err != nil {
		return err
	}
	if c.Password == "" {
		return fmt.Errorf("listener password is required")
	}
	return nil
}

// SocksListener 保存 mihomo socks listener 配置。
type SocksListener struct {
	Name   string              `json:"name"`
	Listen string              `json:"listen"`
	Port   int                 `json:"port"`
	Users  []ClashListenerUser `json:"users"`
}

// Validate 校验 socks listener 名称、端口和认证用户。
func (s SocksListener) Validate() error {
	if err := ValidateIdentifier(s.Name, "listener name"); err != nil {
		return err
	}
	if err := ValidatePort(s.Port, "listener port"); err != nil {
		return err
	}
	for _, user := range s.Users {
		if err := user.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// HTTPListener 保存 mihomo HTTP listener 配置。
type HTTPListener struct {
	Name   string              `json:"name"`
	Listen string              `json:"listen"`
	Port   int                 `json:"port"`
	Users  []ClashListenerUser `json:"users"`
}

// Validate 校验 HTTP listener 名称、端口和认证用户。
func (h HTTPListener) Validate() error {
	if err := ValidateIdentifier(h.Name, "listener name"); err != nil {
		return err
	}
	if err := ValidatePort(h.Port, "listener port"); err != nil {
		return err
	}
	for _, user := range h.Users {
		if err := user.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ClashListeners 保存 mihomo listener 集合，P0 只支持 socks 和 HTTP。
type ClashListeners struct {
	Socks []SocksListener `json:"socks"`
	HTTP  []HTTPListener  `json:"http"`
}

// Validate 校验 P0 listener 限制和内部唯一性。
func (c ClashListeners) Validate() error {
	if len(c.Socks) > 1 {
		return fmt.Errorf("only one socks listener is supported in P0")
	}
	if len(c.HTTP) > 1 {
		return fmt.Errorf("only one http listener is supported in P0")
	}
	socksNames := make([]string, 0, len(c.Socks))
	for _, listener := range c.Socks {
		if err := listener.Validate(); err != nil {
			return err
		}
		socksNames = append(socksNames, listener.Name)
	}
	httpNames := make([]string, 0, len(c.HTTP))
	for _, listener := range c.HTTP {
		if err := listener.Validate(); err != nil {
			return err
		}
		httpNames = append(httpNames, listener.Name)
	}
	if err := ensureUnique(socksNames, "duplicate socks listener name"); err != nil {
		return err
	}
	return ensureUnique(httpNames, "duplicate http listener name")
}

// ClashUpstream 保存 mihomo upstream/proxy 配置。
type ClashUpstream struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Ref    string         `json:"ref"`
	Config map[string]any `json:"config"`

	// configNode 保留 raw config 的原始字段顺序，避免生成 mihomo YAML 时 map 迭代导致 diff 抖动。
	configNode *yaml.Node
}

// SetRawConfig 设置 raw upstream 的 mihomo 节点字段，保留字段顺序并同步解码出 Config。
func (c *ClashUpstream) SetRawConfig(node *yaml.Node) error {
	if node == nil {
		c.configNode = nil
		c.Config = nil
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("raw upstream config must be a mapping")
	}
	config := map[string]any{}
	if err := node.Decode(&config); err != nil {
		return fmt.Errorf("raw upstream config is invalid: %w", err)
	}
	c.configNode = cloneYAMLNode(node)
	c.Config = config
	return nil
}

// ConfigNode 返回 raw upstream config 的有序 YAML 节点副本。
func (c ClashUpstream) ConfigNode() *yaml.Node {
	return cloneYAMLNode(c.configNode)
}

// Validate 校验 upstream 类型和必填字段。
func (c ClashUpstream) Validate() error {
	if err := ValidateIdentifier(c.Name, "upstream name"); err != nil {
		return err
	}
	switch c.Type {
	case "xray-socks5":
		return ValidateRef(c.Ref, 2, "xray-socks5 ref is required")
	case "raw":
		return ValidateRawProxyConfig(c.Config)
	default:
		return fmt.Errorf("unsupported upstream type: %s", c.Type)
	}
}

// ClashGroup 保存 mihomo proxy group 配置。
type ClashGroup struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Proxies  []string `json:"proxies"`
	URL      string   `json:"url"`
	Interval int      `json:"interval"`
	Strategy string   `json:"strategy"`
}

// Validate 校验 group 类型、引用列表和健康检查字段。
func (c *ClashGroup) Validate() error {
	if err := ValidateIdentifier(c.Name, "group name"); err != nil {
		return err
	}
	if !validGroupType(c.Type) {
		return fmt.Errorf("unsupported group type: %s", c.Type)
	}
	if len(c.Proxies) == 0 {
		return fmt.Errorf("group proxies is required")
	}
	if c.Type == "url-test" || c.Type == "load-balance" || c.Type == "fallback" {
		if c.URL == "" || c.Interval == 0 {
			return fmt.Errorf("url and interval are required for url-test/load-balance/fallback group")
		}
	}
	if c.Type == "load-balance" && c.Strategy == "" {
		c.Strategy = "consistent-hashing"
	}
	return nil
}

// ClashRules 保存 mihomo 规则配置。
type ClashRules struct {
	Profile string   `json:"profile"`
	Final   string   `json:"final"`
	Extra   []string `json:"extra"`
}

// ClashConfig 保存单个 stack 的 mihomo 配置。
type ClashConfig struct {
	Enabled    bool            `json:"enabled"`
	Mode       string          `json:"mode"`
	LogLevel   string          `json:"loglevel"`
	Controller ClashController `json:"controller"`
	Listeners  ClashListeners  `json:"listeners"`
	Upstreams  []ClashUpstream `json:"upstreams"`
	Groups     []ClashGroup    `json:"groups"`
	Rules      ClashRules      `json:"rules"`
}

// Validate 校验 mihomo 配置内部引用和字段约束。
func (c *ClashConfig) Validate() error {
	if !validClashMode(c.Mode) {
		return fmt.Errorf("clash mode is invalid: %s", c.Mode)
	}
	if c.LogLevel != "" && !validClashLogLevel(c.LogLevel) {
		return fmt.Errorf("clash loglevel is invalid: %s", c.LogLevel)
	}
	if err := c.Controller.Validate(); err != nil {
		return err
	}
	if err := c.Listeners.Validate(); err != nil {
		return err
	}
	upstreamNames := make([]string, 0, len(c.Upstreams))
	for _, upstream := range c.Upstreams {
		if err := upstream.Validate(); err != nil {
			return err
		}
		upstreamNames = append(upstreamNames, upstream.Name)
	}
	groupNames := make([]string, 0, len(c.Groups))
	for index := range c.Groups {
		if err := c.Groups[index].Validate(); err != nil {
			return err
		}
		groupNames = append(groupNames, c.Groups[index].Name)
	}
	if err := ensureUnique(upstreamNames, "duplicate upstream name"); err != nil {
		return err
	}
	if err := ensureUnique(groupNames, "duplicate group name"); err != nil {
		return err
	}
	if c.Rules.Profile == "" {
		c.Rules.Profile = defaultRuleProfile
	}
	if c.Rules.Final == "" {
		c.Rules.Final = defaultClashFinal
	}
	if c.Rules.Profile != defaultRuleProfile {
		return fmt.Errorf("rules.profile only supports default")
	}
	knownTargets := map[string]bool{"DIRECT": true, "REJECT": true}
	for _, name := range upstreamNames {
		knownTargets[name] = true
	}
	for _, name := range groupNames {
		knownTargets[name] = true
	}
	for _, group := range c.Groups {
		for _, target := range group.Proxies {
			if !knownTargets[target] {
				return fmt.Errorf("group proxy target does not exist: %s", target)
			}
		}
	}
	if err := ValidateRuleTarget(c.Rules.Final, knownTargets, "rules.final target does not exist"); err != nil {
		return err
	}
	for _, rule := range c.Rules.Extra {
		target, err := ExtractRuleTarget(rule)
		if err != nil {
			return err
		}
		if err := ValidateRuleTarget(target, knownTargets, "rules.extra target does not exist"); err != nil {
			return err
		}
	}
	return nil
}

// Stack 保存单个 stacks/<name>.yaml 配置。
type Stack struct {
	Name       string      `json:"name"`
	Enabled    bool        `json:"enabled"`
	Role       string      `json:"role"`
	Labels     []string    `json:"labels"`
	Xray       XrayConfig  `json:"xray"`
	Clash      ClashConfig `json:"clash"`
	SourcePath string      `json:"-"`
}

// Validate 校验 stack 文件内部配置。
func (s *Stack) Validate() error {
	if err := ValidateIdentifier(s.Name, "stack name"); err != nil {
		return err
	}
	if s.Role == "" {
		s.Role = defaultStackRole
	}
	if s.Role != "edge" && s.Role != "auto" {
		return fmt.Errorf("stack role must be edge or auto")
	}
	for _, label := range s.Labels {
		if err := ValidateIdentifier(label, "label"); err != nil {
			return err
		}
	}
	if err := s.Xray.Validate(); err != nil {
		return err
	}
	return s.Clash.Validate()
}

// DisabledUserSet 记录被临时禁用的用户，外层 key 是 stack 名，内层 key 是 user 名。
type DisabledUserSet map[string]map[string]bool

// Has 判断某 stack 上的用户是否处于禁用状态。
func (d DisabledUserSet) Has(stack string, user string) bool {
	if d == nil {
		return false
	}
	return d[stack][user]
}

// Add 记录某 stack 上的用户为禁用状态。
func (d DisabledUserSet) Add(stack string, user string) {
	if d == nil {
		return
	}
	if d[stack] == nil {
		d[stack] = map[string]bool{}
	}
	d[stack][user] = true
}

// StackSet 保存全局配置和所有 stack 的合并输入。
type StackSet struct {
	Config GlobalConfig `json:"config"`
	Stacks []Stack      `json:"stacks"`

	// DisabledUsers 是 runtime/disabled.json 的运行时覆盖，只影响 Xray 生成结果，不参与 manifest hash。
	DisabledUsers DisabledUserSet `json:"-"`
}

// StackNames 返回所有 stack 名称，保持加载顺序。
func (s StackSet) StackNames() []string {
	names := make([]string, 0, len(s.Stacks))
	for _, stack := range s.Stacks {
		names = append(names, stack.Name)
	}
	return names
}

// ByName 返回按 stack 名称索引的映射。
func (s StackSet) ByName() map[string]*Stack {
	stacks := make(map[string]*Stack, len(s.Stacks))
	for index := range s.Stacks {
		stacks[s.Stacks[index].Name] = &s.Stacks[index]
	}
	return stacks
}

// ResolveXrayLogLevel 读取 stack 级 Xray 日志级别覆盖，未配置时使用全局默认。
func ResolveXrayLogLevel(defaults DefaultXrayConfig, xray XrayConfig) string {
	if xray.LogLevel != "" {
		return xray.LogLevel
	}
	if defaults.LogLevel != "" {
		return defaults.LogLevel
	}
	return defaultXrayLogLevel
}

// ResolveXrayAccessLog 读取 Stack 的访问日志开关，未覆盖时继承全局，旧配置默认开启。
func ResolveXrayAccessLog(defaults DefaultXrayConfig, xray XrayConfig) bool {
	if xray.AccessLog != nil {
		return *xray.AccessLog
	}
	return defaults.AccessLog == nil || *defaults.AccessLog
}

// ResolveClashLogLevel 读取 stack 级 mihomo 日志级别覆盖，未配置时使用全局默认。
func ResolveClashLogLevel(defaults DefaultClashConfig, clash ClashConfig) string {
	if clash.LogLevel != "" {
		return clash.LogLevel
	}
	if defaults.LogLevel != "" {
		return defaults.LogLevel
	}
	return defaultClashLogLevel
}

// ResolveXrayAPIConfig 合并全局 defaults.xray.api 和 stack 级覆盖。
func ResolveXrayAPIConfig(defaults DefaultXrayConfig, xray XrayConfig) XrayAPIConfig {
	if xray.API == nil {
		return defaults.API
	}
	return mergeAPIOverride(defaults.API, *xray.API)
}

// ResolveXrayStatsConfig 合并全局 defaults.xray.stats 和 stack 级覆盖。
func ResolveXrayStatsConfig(defaults DefaultXrayConfig, xray XrayConfig) XrayStatsConfig {
	merged := defaults.Stats
	if xray.Stats != nil && xray.Stats.Enabled != nil {
		merged.Enabled = *xray.Stats.Enabled
	}
	return merged
}

// ResolveXrayPolicyConfig 合并全局 defaults.xray.policy 和 stack 级覆盖。
func ResolveXrayPolicyConfig(defaults DefaultXrayConfig, xray XrayConfig) XrayPolicyConfig {
	merged := defaults.Policy
	if xray.Policy == nil {
		return merged
	}
	if xray.Policy.Enabled != nil {
		merged.Enabled = *xray.Policy.Enabled
	}
	if xray.Policy.Levels != nil {
		merged.Levels = mergePolicyLevels(merged.Levels, xray.Policy.Levels)
	}
	if xray.Policy.System != nil {
		merged.System = mergePolicySystem(merged.System, *xray.Policy.System)
	}
	return merged
}

func mergeAPIOverride(base XrayAPIConfig, override XrayAPIOverride) XrayAPIConfig {
	merged := base
	if override.Enabled != nil {
		merged.Enabled = *override.Enabled
	}
	if override.Tag != nil {
		merged.Tag = *override.Tag
	}
	if override.Listen != nil {
		merged.Listen = *override.Listen
	}
	if override.Services != nil {
		merged.Services = append([]string(nil), override.Services...)
	}
	return merged
}

// IsShadowsocks2022Method 判断 shadowsocks method 是否属于 SS2022 方法。
func IsShadowsocks2022Method(method string) bool {
	_, ok := shadowsocks2022KeyLengths[method]
	return ok
}

// ValidateShadowsocks2022PSK 校验 SS2022 PSK 是 base64 且解码长度符合 method。
func ValidateShadowsocks2022PSK(value string, method string, label string) error {
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil {
		return fmt.Errorf("%s must be a base64-encoded PSK for %s", label, method)
	}
	expectedLength := shadowsocks2022KeyLengths[method]
	if len(decoded) != expectedLength {
		return fmt.Errorf("%s must decode to %d bytes for %s", label, expectedLength, method)
	}
	return nil
}

// ValidateIdentifier 校验配置标识符只包含安全的文件名/ref 片段字符。
func ValidateIdentifier(value string, label string) error {
	if !identifierPattern.MatchString(value) || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s contains invalid characters", label)
	}
	return nil
}

// ValidatePort 校验端口处于 TCP/UDP 合法范围。
func ValidatePort(port int, label string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535", label)
	}
	return nil
}

// ValidateRef 校验 ref 存在且段数符合基础格式要求。
func ValidateRef(value string, segmentCount int, message string) error {
	if value == "" {
		return errors.New(message)
	}
	parts := strings.Split(value, ".")
	if len(parts) != segmentCount {
		return fmt.Errorf("ref must contain %d dot-separated segments", segmentCount)
	}
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("ref must not contain empty segments")
		}
	}
	return nil
}

// ParseListen 解析 host:port 形式的监听地址。
func ParseListen(value string) (string, int, error) {
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		if strings.Count(value, ":") == 1 {
			parts := strings.SplitN(value, ":", 2)
			host = parts[0]
			portText = parts[1]
		} else {
			return "", 0, fmt.Errorf("listen must use host:port format")
		}
	}
	if host == "" {
		return "", 0, fmt.Errorf("listen host is required")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, fmt.Errorf("listen port must be an integer")
	}
	if err := ValidatePort(port, "listen port"); err != nil {
		return "", 0, err
	}
	return host, port, nil
}

// IsLoopbackHost 判断 host 是否为本机回环地址。
func IsLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NormalizeInternalEndpointAddress 把本机 wildcard listener 地址归一为可连接的 loopback 地址。
func NormalizeInternalEndpointAddress(address string) string {
	switch address {
	case "0.0.0.0":
		return "127.0.0.1"
	case "::":
		return "::1"
	default:
		return address
	}
}

// ValidateRawProxyConfig 校验 raw upstream 中常见 mihomo 节点的必填字段。
func ValidateRawProxyConfig(config map[string]any) error {
	if len(config) == 0 {
		return fmt.Errorf("raw upstream config is required")
	}
	proxyType, _ := config["type"].(string)
	if proxyType == "" {
		return fmt.Errorf("raw upstream config.type is required")
	}
	if server, _ := config["server"].(string); server == "" {
		return fmt.Errorf("raw upstream config.server is required")
	}
	port, ok := config["port"].(int)
	if !ok {
		return fmt.Errorf("raw upstream config.port must be between 1 and 65535")
	}
	if err := ValidatePort(port, "raw upstream config.port"); err != nil {
		return err
	}
	switch proxyType {
	case "vmess":
		uuid, _ := config["uuid"].(string)
		if uuid == "" {
			return fmt.Errorf("raw vmess upstream uuid is required")
		}
		if !uuidPattern.MatchString(uuid) {
			return fmt.Errorf("uuid must be a valid UUID")
		}
		network, _ := config["network"].(string)
		if network == "" {
			return fmt.Errorf("raw vmess upstream network is required")
		}
	case "shadowsocks":
		cipher, _ := config["cipher"].(string)
		password, _ := config["password"].(string)
		if cipher == "" {
			return fmt.Errorf("raw shadowsocks upstream cipher is required")
		}
		if password == "" {
			return fmt.Errorf("raw shadowsocks upstream password is required")
		}
	}
	return nil
}

// ExtractRuleTarget 从 mihomo 规则文本中提取目标策略或组名。
func ExtractRuleTarget(rule string) (string, error) {
	parts := make([]string, 0)
	for _, part := range strings.Split(rule, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	if len(parts) < 3 {
		return "", fmt.Errorf("rules.extra entry is invalid: %s", rule)
	}
	if parts[len(parts)-1] == "no-resolve" && len(parts) >= 4 {
		return parts[len(parts)-2], nil
	}
	return parts[len(parts)-1], nil
}

// ValidateRuleTarget 校验规则目标指向已存在的节点、组或内置策略。
func ValidateRuleTarget(target string, knownTargets map[string]bool, message string) error {
	if !knownTargets[target] {
		return fmt.Errorf("%s: %s", message, target)
	}
	return nil
}

func validateDefaultClash(config DefaultClashConfig) error {
	if !validClashMode(config.Mode) {
		return fmt.Errorf("defaults.clash.mode is invalid: %s", config.Mode)
	}
	if !validClashLogLevel(config.LogLevel) {
		return fmt.Errorf("defaults.clash.loglevel is invalid: %s", config.LogLevel)
	}
	if config.RuleProfile != defaultRuleProfile {
		return fmt.Errorf("defaults.clash.rule_profile only supports default")
	}
	return nil
}

func validateDefaultXray(config DefaultXrayConfig) error {
	if !validXrayLogLevel(config.LogLevel) {
		return fmt.Errorf("defaults.xray.loglevel is invalid: %s", config.LogLevel)
	}
	if err := validateAPIConfig(config.API); err != nil {
		return err
	}
	return nil
}

func validateAPIConfig(config XrayAPIConfig) error {
	if err := ValidateIdentifier(config.Tag, "xray api tag"); err != nil {
		return err
	}
	host, _, err := ParseListen(config.Listen)
	if err != nil {
		return err
	}
	if !IsLoopbackHost(host) {
		return fmt.Errorf("xray api listen must use loopback host")
	}
	if len(config.Services) == 0 {
		return fmt.Errorf("xray api services is required")
	}
	for _, service := range config.Services {
		if service == "" {
			return fmt.Errorf("xray api service name is required")
		}
	}
	return nil
}

func mergePolicyLevels(defaults map[string]XrayPolicyLevelConfig, overrides map[string]XrayPolicyLevelConfig) map[string]XrayPolicyLevelConfig {
	merged := make(map[string]XrayPolicyLevelConfig, len(defaults)+len(overrides))
	for level, config := range defaults {
		merged[level] = config
	}
	for level, override := range overrides {
		current := merged[level]
		if override.StatsUserUplink != nil {
			current.StatsUserUplink = override.StatsUserUplink
		}
		if override.StatsUserDownlink != nil {
			current.StatsUserDownlink = override.StatsUserDownlink
		}
		merged[level] = current
	}
	return merged
}

func mergePolicySystem(defaults XrayPolicySystemConfig, override XrayPolicySystemConfig) XrayPolicySystemConfig {
	merged := defaults
	if override.StatsInboundUplink != nil {
		merged.StatsInboundUplink = override.StatsInboundUplink
	}
	if override.StatsInboundDownlink != nil {
		merged.StatsInboundDownlink = override.StatsInboundDownlink
	}
	if override.StatsOutboundUplink != nil {
		merged.StatsOutboundUplink = override.StatsOutboundUplink
	}
	if override.StatsOutboundDownlink != nil {
		merged.StatsOutboundDownlink = override.StatsOutboundDownlink
	}
	return merged
}

func cloneYAMLNode(value *yaml.Node) *yaml.Node {
	if value == nil {
		return nil
	}
	cloned := *value
	if len(value.Content) > 0 {
		cloned.Content = make([]*yaml.Node, 0, len(value.Content))
		for _, child := range value.Content {
			cloned.Content = append(cloned.Content, cloneYAMLNode(child))
		}
	}
	return &cloned
}

func ensureUnique(values []string, message string) error {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return fmt.Errorf("%s: %s", message, value)
		}
		seen[value] = true
	}
	return nil
}

func userValues(users []InboundUser, pick func(InboundUser) string) []string {
	values := make([]string, 0, len(users))
	for _, user := range users {
		values = append(values, pick(user))
	}
	return values
}

func validInboundProtocol(protocol string) bool {
	switch protocol {
	case "vmess", "shadowsocks", "socks5", "http":
		return true
	default:
		return false
	}
}

// SupportsInboundUDPProtocol 判断 inbound 协议是否支持导出 UDP 订阅字段。
func SupportsInboundUDPProtocol(protocol string) bool {
	switch protocol {
	case "vmess", "shadowsocks", "socks5":
		return true
	default:
		return false
	}
}

func validXrayLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warning", "error", "none":
		return true
	default:
		return false
	}
}

func validClashLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warning", "error", "silent":
		return true
	default:
		return false
	}
}

func validClashMode(mode string) bool {
	switch mode {
	case "Rule", "Global", "Direct":
		return true
	default:
		return false
	}
}

func validGroupType(groupType string) bool {
	switch groupType {
	case "select", "url-test", "load-balance", "fallback":
		return true
	default:
		return false
	}
}
