package configdoc

import (
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"proxystack-web/internal/domain"
	"proxystack-web/internal/domain/validation"
)

// Issue 是一条校验问题，Path 为文档内的字段路径（例如 stacks[1].xray.inbounds[0].port），
// 为空表示无法定位到具体字段。
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Issues 是校验问题列表。
type Issues []Issue

func (i Issues) Error() string {
	lines := make([]string, 0, len(i))
	for _, issue := range i {
		if issue.Path == "" {
			lines = append(lines, issue.Message)
			continue
		}
		lines = append(lines, issue.Path+": "+issue.Message)
	}
	return strings.Join(lines, "\n")
}

func (i *Issues) add(path, format string, args ...any) {
	*i = append(*i, Issue{Path: path, Message: fmt.Sprintf(format, args...)})
}

// xrayPlaceholderUser 与 xray 生成器的占位用户同名，不能用作真实用户名。
const xrayPlaceholderUser = "psw-placeholder"

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	uuidPattern       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	regionPattern     = regexp.MustCompile(`^[A-Z]{2}$`)
	sha256Pattern     = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	hostnamePattern   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

// ShadowsocksMethods 是入站允许的 shadowsocks method。
var ShadowsocksMethods = []string{
	"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305",
	"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305",
}

// RuleTypes 是附加规则允许的类型。
var RuleTypes = []string{
	"DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-REGEX", "GEOSITE",
	"IP-CIDR", "IP-CIDR6", "IP-SUFFIX", "IP-ASN", "GEOIP",
	"SRC-IP-CIDR", "SRC-PORT", "DST-PORT", "IN-PORT", "IN-TYPE",
	"PROCESS-NAME", "PROCESS-PATH", "NETWORK", "RULE-SET", "AND", "OR", "NOT",
}

// Validate 校验整份文档：先做字段级校验，全部通过后再交给领域模型做跨 stack 校验
// （端口冲突、引用图、订阅节点重名、公开入站认证）。不探测系统端口占用。
func Validate(doc *Document) Issues {
	var issues Issues
	settings := DefaultSettings()
	if doc.Settings != nil {
		settings = *doc.Settings
		validateSettings(&issues, doc, settings)
	}
	validateUsers(&issues, doc)
	stackNames := map[string]bool{}
	for index := range doc.Stacks {
		path := fmt.Sprintf("stacks[%d]", index)
		stack := &doc.Stacks[index]
		if stackNames[stack.Name] {
			issues.add(path+".name", "duplicate stack name: %s", stack.Name)
		}
		stackNames[stack.Name] = true
		validateStack(&issues, doc, path, stack)
	}
	validateSubscription(&issues, doc)
	if len(issues) > 0 {
		return issues
	}
	return validateDomain(doc)
}

func validateDomain(doc *Document) Issues {
	var issues Issues
	set, err := Compile(doc)
	if err != nil {
		issues.add("", "%s", err.Error())
		return issues
	}
	if err := set.Config.Validate(); err != nil {
		issues.add("settings", "%s", err.Error())
	}
	for index := range set.Stacks {
		if err := set.Stacks[index].Validate(); err != nil {
			issues.add(fmt.Sprintf("stacks[%d]", index), "%s", err.Error())
		}
	}
	if len(issues) > 0 {
		return issues
	}
	resolved, err := domain.ResolveStackSetUserRefs(set)
	if err != nil {
		issues.add("", "%s", err.Error())
		return issues
	}
	if err := validation.ValidateStackSet(resolved, validation.WithPortChecker(validation.NoopPortChecker{})); err != nil {
		var cfgErr validation.ConfigValidationError
		if ok := asConfigError(err, &cfgErr); ok {
			for _, issue := range cfgErr.Issues {
				issues = append(issues, Issue{Path: MapDomainPath(doc, issue.Path), Message: issue.Message})
				// 重复类问题只报在后出现的位置；在先出现的位置也补一条，保存任一方时都能定位到自己的字段。
				if match := firstSeenPattern.FindStringSubmatch(issue.Message); match != nil {
					message := strings.TrimSpace(firstSeenPattern.ReplaceAllString(issue.Message, ""))
					issues = append(issues, Issue{
						Path:    MapDomainPath(doc, match[1]),
						Message: message + ", also used at " + issue.Path,
					})
				}
			}
		} else {
			issues.add("", "%s", err.Error())
		}
	}
	if len(issues) == 0 {
		validateSubscriptionNodes(&issues, doc, resolved)
	}
	return issues
}

func asConfigError(err error, target *validation.ConfigValidationError) bool {
	cfgErr, ok := err.(validation.ConfigValidationError)
	if ok {
		*target = cfgErr
	}
	return ok
}

var (
	domainPathPattern = regexp.MustCompile(`^stacks\.([^.]+)\.(.*)$`)
	firstSeenPattern  = regexp.MustCompile(`,? first seen (?:at|in) (stacks\.\S+?)(?:,|$)`)
)

// MapDomainPath 把领域校验路径（stacks.<name>.xray.inbounds[0].port）翻译为文档路径。
func MapDomainPath(doc *Document, path string) string {
	match := domainPathPattern.FindStringSubmatch(path)
	if match == nil {
		return ""
	}
	index := doc.StackByName(match[1])
	if index < 0 {
		return ""
	}
	prefix := fmt.Sprintf("stacks[%d]", index)
	rest := match[2]
	replacements := []struct{ from, to string }{
		{"xray.api.listen", "xray.api_port"},
		{"xray.outbound.ref", "xray.outbound.type"},
		{"clash.controller.listen", "mihomo.controller_port"},
		{"clash.listeners.socks[0]", "mihomo.socks"},
		{"clash.listeners.http[0]", "mihomo.http"},
		{"clash.upstreams", "mihomo.upstreams"},
		{"clash.groups", "mihomo.groups"},
		{".user_refs", ".users"},
		{".auth", ".auth_type"},
	}
	for _, replacement := range replacements {
		rest = strings.Replace(rest, replacement.from, replacement.to, 1)
	}
	rest = strings.TrimSuffix(rest, ".ref")
	if strings.HasPrefix(rest, "mihomo.upstreams[") && !strings.Contains(rest, "].") {
		rest += ".inbound"
	}
	return prefix + "." + rest
}

func validateSettings(issues *Issues, doc *Document, settings Settings) {
	if host := settings.Basic.ExternalHost; host != "" && !validHost(host) {
		issues.add("settings.basic.external_host", "must be a domain name or IP address")
	}
	if settings.Basic.Timezone != "" {
		if _, err := time.LoadLocation(settings.Basic.Timezone); err != nil {
			issues.add("settings.basic.timezone", "unknown time zone")
		}
	}
	if settings.Basic.PanelURL != "" {
		parsed, err := url.Parse(settings.Basic.PanelURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			issues.add("settings.basic.panel_url", "must be an http(s) URL without query or fragment")
		}
	}
	validateTrafficNotify(issues, settings)
	pools := map[string]PortRange{
		"xray_inbound":      settings.PortPools.XrayInbound,
		"mihomo_socks":      settings.PortPools.MihomoSocks,
		"mihomo_http":       settings.PortPools.MihomoHTTP,
		"xray_api":          settings.PortPools.XrayAPI,
		"mihomo_controller": settings.PortPools.MihomoController,
	}
	for name, pool := range pools {
		if err := domain.PortRange(pool).Validate(); err != nil {
			issues.add("settings.port_pools."+name, "%s", err.Error())
		}
	}
	xray := settings.Defaults.Xray
	if xray.APIEnabled && len(xray.APIServices) == 0 {
		issues.add("settings.defaults.xray.api_services", "at least one service is required when the API is enabled")
	}
	for name, source := range map[string]DepSource{"xray": settings.Deps.Xray, "mihomo": settings.Deps.Mihomo, "geo": settings.Deps.Geo} {
		path := "settings.deps." + name
		checkEnum(issues, path+".source", source.Source, "auto", "github", "custom")
		if source.Version == "" {
			issues.add(path+".version", "is required")
		}
		if source.Source == "custom" {
			if parsed, err := url.Parse(source.URL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				issues.add(path+".url", "must be an http(s) URL")
			}
			if !sha256Pattern.MatchString(source.SHA256) {
				issues.add(path+".sha256", "a 64-character hex sha256 is required for custom URLs")
			}
		}
	}
	checkEnum(issues, "settings.deps.mihomo_variant", settings.Deps.MihomoVariant, "auto", "standard", "compatible")
	for field, value := range map[string]string{"window_start": settings.Deps.WindowStart, "window_end": settings.Deps.WindowEnd} {
		if _, err := time.Parse("15:04", value); err != nil {
			issues.add("settings.deps."+field, "must use HH:MM")
		}
	}
	validateDownloadProxy(issues, doc, settings.DownloadProxy)
}

func validateDownloadProxy(issues *Issues, doc *Document, proxy DownloadProxySettings) {
	path := "settings.download_proxy"
	switch proxy.Mode {
	case ProxyModeDirect:
	case ProxyModeStack:
		index := doc.StackByName(proxy.Stack)
		if index < 0 {
			issues.add(path+".stack", "stack does not exist: %s", proxy.Stack)
			return
		}
		checkEnum(issues, path+".listener", proxy.Listener, "socks", "http")
		mihomo := doc.Stacks[index].Mihomo
		if !mihomo.Enabled || (proxy.Listener == "http" && !mihomo.HTTP.Enabled) {
			issues.add(path+".listener", "the stack has no such mihomo listener")
		}
	case ProxyModeCustom:
		checkEnum(issues, path+".protocol", proxy.Protocol, "http", "socks5")
		if proxy.Host == "" || !validHost(proxy.Host) {
			issues.add(path+".host", "must be a domain name or IP address")
		}
		checkPort(issues, path+".port", proxy.Port)
		if (proxy.Username == "") != (proxy.Password == "") {
			issues.add(path+".password", "username and password must be set together")
		}
	default:
		issues.add(path+".mode", "must be direct, stack or custom")
	}
}

func validateUsers(issues *Issues, doc *Document) {
	names := map[string]bool{}
	tokens := map[string]bool{}
	for index := range doc.Users {
		user := &doc.Users[index]
		path := fmt.Sprintf("users[%d]", index)
		checkIdentifier(issues, path+".name", user.Name)
		checkDisplayTemplate(issues, path+".display_template", user.DisplayTemplate)
		if user.Name == xrayPlaceholderUser {
			issues.add(path+".name", "this name is reserved")
		}
		for pausedIndex, stack := range user.PausedStacks {
			if doc.StackByName(stack) < 0 {
				issues.add(fmt.Sprintf("%s.paused_stacks[%d]", path, pausedIndex), "stack does not exist: %s", stack)
			}
		}
		if names[user.Name] {
			issues.add(path+".name", "duplicate user name: %s", user.Name)
		}
		names[user.Name] = true
		checkUUID(issues, path+".uuid", user.UUID, true)
		if user.SSPassword == "" {
			issues.add(path+".ss_password", "is required")
		}
		if user.SubToken == "" {
			issues.add(path+".sub_token", "is required")
		} else if tokens[user.SubToken] {
			issues.add(path+".sub_token", "duplicate subscription token")
		}
		tokens[user.SubToken] = true
		if user.QuotaBytes < 0 {
			issues.add(path+".quota_bytes", "must not be negative")
		}
		if user.DailyLimitBytes < 0 {
			issues.add(path+".daily_limit_bytes", "must not be negative")
		}
		checkEnum(issues, path+".daily_limit_action", user.DailyLimitAction, DailyActionPause, DailyActionNotify)
		checkEnum(issues, path+".pause_window_mode", user.PauseWindowMode, PauseWindowInherit, PauseWindowCustom, PauseWindowOff)
		checkPauseWindows(issues, path+".pause_windows", user.PauseWindows)
		checkBlockedDomains(issues, path+".blocked_domains", user.BlockedDomains)
		if user.PauseWindowMode == PauseWindowCustom && len(user.PauseWindows) == 0 {
			issues.add(path+".pause_windows", "at least one window is required for custom mode")
		}
		switch user.ResetMode {
		case "none":
		case "monthly":
			if user.ResetDay < 1 || user.ResetDay > 28 {
				issues.add(path+".reset_day", "must be between 1 and 28")
			}
		default:
			issues.add(path+".reset_mode", "must be none or monthly")
		}
		if user.ExpireAt != "" {
			if _, err := time.Parse(time.RFC3339, user.ExpireAt); err != nil {
				issues.add(path+".expire_at", "must be an RFC 3339 time")
			}
		}
		profiles := map[string]bool{DefaultProfile: true}
		for profileIndex, profile := range user.Profiles {
			profilePath := fmt.Sprintf("%s.profiles[%d]", path, profileIndex)
			checkIdentifier(issues, profilePath+".name", profile.Name)
			checkDisplayTemplate(issues, profilePath+".display_template", profile.DisplayTemplate)
			if profiles[profile.Name] {
				issues.add(profilePath+".name", "duplicate profile name: %s", profile.Name)
			}
			profiles[profile.Name] = true
			checkUUID(issues, profilePath+".uuid", profile.UUID, false)
		}
	}
}

func validateStack(issues *Issues, doc *Document, path string, stack *Stack) {
	checkIdentifier(issues, path+".name", stack.Name)
	if stack.DailyLimitBytes < 0 {
		issues.add(path+".daily_limit_bytes", "must be at least 0")
	}
	if strings.ContainsAny(stack.DirectSuffix, "\r\n\t") {
		issues.add(path+".direct_suffix", "must not contain line breaks or tabs")
	}
	directUsers := map[string]bool{}
	for index, user := range stack.DirectUsers {
		field := fmt.Sprintf("%s.direct_users[%d]", path, index)
		if doc.UserByName(user) < 0 {
			issues.add(field, "user does not exist: %s", user)
		}
		if directUsers[user] {
			issues.add(field, "duplicate direct user: %s", user)
		}
		directUsers[user] = true
	}
	validateDirectOverrides(issues, path, stack, directUsers)
	if stack.Role != "edge" && stack.Role != "auto" {
		issues.add(path+".role", "must be edge or auto")
	}
	for index, label := range stack.Labels {
		checkIdentifier(issues, fmt.Sprintf("%s.labels[%d]", path, index), label)
	}
	xray := stack.Xray
	checkEnum(issues, path+".xray.loglevel", xray.LogLevel, Inherit, "debug", "info", "warning", "error", "none")
	for field, value := range map[string]string{"access_log": xray.AccessLog, "api": xray.API, "stats": xray.Stats, "policy": xray.Policy} {
		checkEnum(issues, path+".xray."+field, value, Inherit, On, Off)
	}
	checkPort(issues, path+".xray.api_port", xray.APIPort)
	validateOutbound(issues, path+".xray.outbound", xray.Outbound)
	if xray.Enabled && len(xray.Inbounds) == 0 {
		issues.add(path+".xray.inbounds", "at least one inbound is required")
	}
	inboundNames := map[string]bool{}
	for index := range xray.Inbounds {
		inboundPath := fmt.Sprintf("%s.xray.inbounds[%d]", path, index)
		inbound := &xray.Inbounds[index]
		if inboundNames[inbound.Name] {
			issues.add(inboundPath+".name", "duplicate inbound name: %s", inbound.Name)
		}
		inboundNames[inbound.Name] = true
		validateInbound(issues, doc, inboundPath, inbound)
	}
	validateMihomo(issues, doc, path+".mihomo", stack)
}

func validateOutbound(issues *Issues, path string, outbound Outbound) {
	switch outbound.Type {
	case "mihomo", "direct":
		if outbound.PrivateDirect {
			issues.add(path+".private_direct", "is only supported for socks5/http outbound")
		}
	case "socks5", "http":
		if outbound.Server == "" {
			issues.add(path+".server", "is required")
		} else if !validHost(outbound.Server) {
			issues.add(path+".server", "must be a domain name or IP address")
		}
		checkPort(issues, path+".port", outbound.Port)
		if (outbound.Username == "") != (outbound.Password == "") {
			issues.add(path+".password", "username and password must be set together")
		}
	default:
		issues.add(path+".type", "must be mihomo, socks5, http or direct")
	}
}

func validateInbound(issues *Issues, doc *Document, path string, inbound *Inbound) {
	checkIdentifier(issues, path+".name", inbound.Name)
	checkDisplayTemplate(issues, path+".display_template", inbound.DisplayTemplate)
	if !validListen(inbound.Listen) {
		issues.add(path+".listen", "must be an IP address")
	}
	checkPort(issues, path+".port", inbound.Port)
	if inbound.Region != "" && !regionPattern.MatchString(inbound.Region) {
		issues.add(path+".region", "must be a two-letter country code")
	}
	if inbound.Server != "" && !validHost(inbound.Server) {
		issues.add(path+".server", "must be a domain name or IP address")
	}
	if inbound.UDP && !domain.SupportsInboundUDPProtocol(inbound.Protocol) {
		issues.add(path+".udp", "is not supported for %s", inbound.Protocol)
	}
	switch inbound.Protocol {
	case "vmess":
		checkEnum(issues, path+".network", inbound.Network, "raw", "ws", "grpc")
		if inbound.Network == "ws" && !strings.HasPrefix(inbound.WSPath, "/") {
			issues.add(path+".ws_path", "must start with /")
		}
		if inbound.Network == "grpc" && inbound.GRPCServiceName == "" {
			issues.add(path+".grpc_service_name", "is required")
		}
		if len(inbound.Users) == 0 {
			issues.add(path+".users", "at least one user is required")
		}
	case "shadowsocks":
		if !slices.Contains(ShadowsocksMethods, inbound.Method) {
			issues.add(path+".method", "unsupported method")
		} else if inbound.Password == "" {
			issues.add(path+".password", "is required")
		} else if domain.IsShadowsocks2022Method(inbound.Method) {
			if err := domain.ValidateShadowsocks2022PSK(inbound.Password, inbound.Method, "password"); err != nil {
				issues.add(path+".password", "%s", err.Error())
			}
		}
	case "socks5", "http":
		checkEnum(issues, path+".auth_type", inbound.AuthType, "noauth", "password")
		if inbound.AuthType == "password" {
			if inbound.AuthUsername == "" {
				issues.add(path+".auth_username", "is required")
			}
			if inbound.AuthPassword == "" {
				issues.add(path+".auth_password", "is required")
			}
		}
		if inbound.Sub && inbound.AuthType != "password" {
			issues.add(path+".auth_type", "password auth is required when the inbound is published to subscriptions")
		}
		if inbound.Sub && len(inbound.Users) == 0 {
			issues.add(path+".users", "at least one subscription user is required when the inbound is published")
		}
	default:
		issues.add(path+".protocol", "must be vmess, shadowsocks, socks5 or http")
	}
	seen := map[string]bool{}
	for index, ref := range inbound.Users {
		refPath := fmt.Sprintf("%s.users[%d]", path, index)
		checkDisplayTemplate(issues, refPath+".display_template", ref.DisplayTemplate)
		profile := ref.Profile
		if profile == "" {
			profile = DefaultProfile
		}
		if seen[ref.User] {
			issues.add(refPath+".user", "user is referenced more than once: %s", ref.User)
		}
		seen[ref.User] = true
		userIndex := doc.UserByName(ref.User)
		if userIndex < 0 {
			issues.add(refPath+".user", "user does not exist: %s", ref.User)
			continue
		}
		user := &doc.Users[userIndex]
		uuid, password := user.UUID, user.SSPassword
		if profile != DefaultProfile {
			profileIndex := slices.IndexFunc(user.Profiles, func(p Profile) bool { return p.Name == profile })
			if profileIndex < 0 {
				issues.add(refPath+".profile", "profile does not exist: %s", profile)
				continue
			}
			uuid, password = user.Profiles[profileIndex].UUID, user.Profiles[profileIndex].SSPassword
		}
		switch inbound.Protocol {
		case "vmess":
			if uuid == "" {
				issues.add(refPath+".profile", "profile %s/%s has no uuid", ref.User, profile)
			}
		case "shadowsocks":
			if password == "" {
				issues.add(refPath+".profile", "profile %s/%s has no shadowsocks password", ref.User, profile)
			} else if domain.IsShadowsocks2022Method(inbound.Method) {
				if err := domain.ValidateShadowsocks2022PSK(password, inbound.Method, "user password"); err != nil {
					issues.add(refPath+".profile", "%s/%s: %s", ref.User, profile, err.Error())
				}
			}
		}
	}
}

func checkDisplayTemplate(issues *Issues, path string, value string) {
	if err := validation.CheckDisplayTemplate(value); err != nil {
		issues.add(path, "%s", err.Error())
	}
}

func validateMihomo(issues *Issues, doc *Document, path string, stack *Stack) {
	mihomo := stack.Mihomo
	checkEnum(issues, path+".mode", mihomo.Mode, Inherit, "Rule", "Global", "Direct")
	checkEnum(issues, path+".loglevel", mihomo.LogLevel, Inherit, "debug", "info", "warning", "error", "silent")
	checkPort(issues, path+".controller_port", mihomo.ControllerPort)
	if mihomo.ControllerSecret == "" {
		issues.add(path+".controller_secret", "is required")
	}
	validateListener(issues, path+".socks", mihomo.Socks)
	if mihomo.HTTP.Enabled {
		validateListener(issues, path+".http", mihomo.HTTP)
	}
	targets := map[string]bool{BuiltinDirect: true, BuiltinReject: true}
	for index, upstream := range mihomo.Upstreams {
		upstreamPath := fmt.Sprintf("%s.upstreams[%d]", path, index)
		checkIdentifier(issues, upstreamPath+".name", upstream.Name)
		if targets[upstream.Name] {
			issues.add(upstreamPath+".name", "duplicate proxy name: %s", upstream.Name)
		}
		targets[upstream.Name] = true
		switch upstream.Type {
		case "raw":
			if upstream.Config.Node == nil {
				issues.add(upstreamPath+".config", "is required")
				continue
			}
			var compiled domain.ClashUpstream
			if err := compiled.SetRawConfig(upstream.Config.Node); err != nil {
				issues.add(upstreamPath+".config", "%s", err.Error())
			} else if err := domain.ValidateRawProxyConfig(compiled.Config); err != nil {
				issues.add(upstreamPath+".config", "%s", err.Error())
			}
		case "xray-socks5":
			stackIndex := doc.StackByName(upstream.Stack)
			if stackIndex < 0 {
				issues.add(upstreamPath+".stack", "stack does not exist: %s", upstream.Stack)
				continue
			}
			target := &doc.Stacks[stackIndex]
			inboundIndex := target.InboundByName(upstream.Inbound)
			if inboundIndex < 0 {
				issues.add(upstreamPath+".inbound", "inbound does not exist: %s.%s", upstream.Stack, upstream.Inbound)
			} else if target.Xray.Inbounds[inboundIndex].Protocol != "socks5" {
				issues.add(upstreamPath+".inbound", "must reference a socks5 inbound")
			}
		default:
			issues.add(upstreamPath+".type", "must be raw or xray-socks5")
		}
	}
	for index, group := range mihomo.Groups {
		groupPath := fmt.Sprintf("%s.groups[%d]", path, index)
		checkIdentifier(issues, groupPath+".name", group.Name)
		if targets[group.Name] {
			issues.add(groupPath+".name", "duplicate proxy name: %s", group.Name)
		}
		targets[group.Name] = true
	}
	groups := map[string]Group{}
	for _, group := range mihomo.Groups {
		groups[group.Name] = group
	}
	for index, group := range mihomo.Groups {
		groupPath := fmt.Sprintf("%s.groups[%d]", path, index)
		checkEnum(issues, groupPath+".type", group.Type, "select", "url-test", "load-balance", "fallback")
		if len(group.Proxies) == 0 {
			issues.add(groupPath+".proxies", "at least one proxy is required")
		}
		for _, proxy := range group.Proxies {
			if proxy == group.Name {
				issues.add(groupPath+".proxies", "group must not contain itself")
			} else if !targets[proxy] {
				issues.add(groupPath+".proxies", "proxy does not exist: %s", proxy)
			}
		}
		if group.Type != "select" {
			if group.URL == "" {
				issues.add(groupPath+".url", "is required")
			}
			if group.Interval <= 0 {
				issues.add(groupPath+".interval", "must be positive")
			}
		}
		if group.Type == "load-balance" {
			checkEnum(issues, groupPath+".strategy", group.Strategy, "consistent-hashing", "round-robin", "sticky-sessions")
		}
		if cycle := groupCycle(groups, group.Name); cycle != "" {
			issues.add(groupPath+".proxies", "groups form a cycle: %s", cycle)
		}
	}
	if mihomo.Rules.Profile != "default" {
		issues.add(path+".rules.profile", "must be default")
	}
	if !targets[mihomo.Rules.Final] {
		issues.add(path+".rules.final", "target does not exist: %s", mihomo.Rules.Final)
	}
	for index, rule := range mihomo.Rules.Extra {
		rulePath := fmt.Sprintf("%s.rules.extra[%d]", path, index)
		if !slices.Contains(RuleTypes, rule.Type) {
			issues.add(rulePath+".type", "unsupported rule type: %s", rule.Type)
		}
		if rule.Value == "" {
			issues.add(rulePath+".value", "is required")
		}
		if !targets[rule.Target] {
			issues.add(rulePath+".target", "target does not exist: %s", rule.Target)
		}
	}
	if stack.Xray.Enabled && stack.Xray.Outbound.Type == "mihomo" && !mihomo.Enabled {
		issues.add(path+".enabled", "xray outbound uses this stack's mihomo, so mihomo must be enabled")
	}
}

func groupCycle(groups map[string]Group, start string) string {
	var visit func(name string, trail []string) string
	visit = func(name string, trail []string) string {
		if slices.Contains(trail, name) {
			return strings.Join(append(trail, name), " -> ")
		}
		group, ok := groups[name]
		if !ok {
			return ""
		}
		for _, proxy := range group.Proxies {
			if proxy == name {
				continue
			}
			if cycle := visit(proxy, append(trail, name)); cycle != "" {
				return cycle
			}
		}
		return ""
	}
	cycle := visit(start, nil)
	if cycle != "" && !strings.HasPrefix(cycle, start+" ") {
		return ""
	}
	return cycle
}

func validateListener(issues *Issues, path string, listener Listener) {
	checkIdentifier(issues, path+".name", listener.Name)
	if !validListen(listener.Listen) {
		issues.add(path+".listen", "must be an IP address")
	}
	checkPort(issues, path+".port", listener.Port)
	checkEnum(issues, path+".auth", listener.Auth, ListenerAuthInherit, ListenerAuthNone, ListenerAuthAccounts)
	if listener.Auth == ListenerAuthAccounts {
		if len(listener.Accounts) == 0 {
			issues.add(path+".accounts", "at least one account is required")
		}
		for index, account := range listener.Accounts {
			checkIdentifier(issues, fmt.Sprintf("%s.accounts[%d].username", path, index), account.Username)
			if account.Password == "" {
				issues.add(fmt.Sprintf("%s.accounts[%d].password", path, index), "is required")
			}
		}
	}
}

func checkIdentifier(issues *Issues, path, value string) {
	if value == "" {
		issues.add(path, "is required")
		return
	}
	if err := domain.ValidateIdentifier(value, "value"); err != nil || !identifierPattern.MatchString(value) {
		issues.add(path, "may only contain letters, digits, _ . - and must start with a letter, digit or _")
	}
}

func checkUUID(issues *Issues, path, value string, required bool) {
	if value == "" {
		if required {
			issues.add(path, "is required")
		}
		return
	}
	if !uuidPattern.MatchString(value) {
		issues.add(path, "must be a valid UUID")
	}
}

func checkPort(issues *Issues, path string, port int) {
	if port < 1 || port > 65535 {
		issues.add(path, "must be between 1 and 65535")
	}
}

func checkEnum(issues *Issues, path, value string, allowed ...string) {
	if !slices.Contains(allowed, value) {
		issues.add(path, "must be one of: %s", strings.Join(allowed, ", "))
	}
}

func validListen(value string) bool {
	return net.ParseIP(value) != nil
}

func validHost(value string) bool {
	return net.ParseIP(value) != nil || hostnamePattern.MatchString(value)
}

// Clone 深拷贝 stack。
func (s Stack) Clone() Stack {
	var clone Stack
	mustRoundTrip(s, &clone)
	return clone
}

// Clone 深拷贝文档。
func (d *Document) Clone() *Document {
	clone := &Document{}
	mustRoundTrip(d, clone)
	return clone
}

func mustRoundTrip(in, out any) {
	data, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		panic(err)
	}
}

// validateTrafficNotify 校验流量监控和邮件通知设置。邮件未启用时不要求服务器和收发件人。
func validateTrafficNotify(issues *Issues, settings Settings) {
	traffic := settings.Traffic
	if traffic.Interval != 5 && traffic.Interval != 10 && traffic.Interval != 30 {
		issues.add("settings.traffic.interval", "must be 5, 10 or 30 seconds")
	}
	// 上一级汇总从下一级重算，下一级至少要保留一个完整的上级周期（0 表示永久保留）。
	for field, retention := range map[string]struct{ days, minimum int }{
		"retention_minute_days": {traffic.RetentionMinuteDays, 2},
		"retention_hour_days":   {traffic.RetentionHourDays, 3},
		"retention_day_days":    {traffic.RetentionDayDays, 62},
		"retention_month_days":  {traffic.RetentionMonthDays, 0},
	} {
		if retention.days < 0 || (retention.days != 0 && retention.days < retention.minimum) {
			issues.add("settings.traffic."+field, "must be 0 (keep forever) or at least %d days", retention.minimum)
		}
	}
	checkPauseWindows(issues, "settings.traffic.pause_windows", traffic.PauseWindows)
	checkBlockedDomains(issues, "settings.traffic.blocked_domains", traffic.BlockedDomains)
	checkAccessGroups(issues, "settings.traffic.access_groups", traffic.AccessGroups)
	if traffic.AccessRetentionDays < 1 || traffic.AccessRetentionDays > 3650 {
		issues.add("settings.traffic.access_retention_days", "must be between 1 and 3650 days")
	}
	notify := settings.Notify
	checkEnum(issues, "settings.notify.security", notify.Security, SMTPSecurityNone, SMTPSecuritySTARTTLS, SMTPSecurityTLS)
	if notify.WarnPercent < 1 || notify.WarnPercent > 100 {
		issues.add("settings.notify.warn_percent", "must be between 1 and 100")
	}
	if notify.ExpireDays < 1 {
		issues.add("settings.notify.expire_days", "must be at least 1")
	}
	if notify.DBWarnMB < 1 || notify.DBWarnMB > MaxDBWarnMB {
		issues.add("settings.notify.db_warn_mb", "must be between 1 and %d", MaxDBWarnMB)
	}
	for index, event := range notify.Events {
		checkEnum(issues, fmt.Sprintf("settings.notify.events[%d]", index), event,
			EventDailyLimit, EventDailyWarning, EventQuotaLimit, EventQuotaWarning, EventExpired, EventExpiring, EventEgressDown, EventEgressRecovered,
			EventHealthAlert, EventHealthRecovered)
	}
	if notify.From != "" {
		if _, err := mail.ParseAddress(notify.From); err != nil {
			issues.add("settings.notify.from", "must be an email address")
		}
	}
	for index, address := range notify.To {
		if _, err := mail.ParseAddress(address); err != nil {
			issues.add(fmt.Sprintf("settings.notify.to[%d]", index), "must be an email address")
		}
	}
	if !notify.Enabled {
		return
	}
	if notify.Host == "" {
		issues.add("settings.notify.host", "is required when notifications are enabled")
	}
	if notify.Port < 1 {
		issues.add("settings.notify.port", "is required when notifications are enabled")
	}
	if notify.From == "" {
		issues.add("settings.notify.from", "is required when notifications are enabled")
	}
	if len(notify.To) == 0 {
		issues.add("settings.notify.to", "at least one recipient is required when notifications are enabled")
	}
	if (notify.Username == "") != (notify.Password == "") {
		issues.add("settings.notify.password", "username and password must be set together")
	}
}
