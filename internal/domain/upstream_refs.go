package domain

import "fmt"

// IsInboundUpstreamType recognizes generic and legacy local inbound references.
func IsInboundUpstreamType(kind string) bool {
	return kind == "xray-inbound" || kind == "xray-socks5"
}

// SupportsInboundUpstream reports which Xray protocols can become Mihomo proxies.
func SupportsInboundUpstream(protocol string) bool {
	switch protocol {
	case "socks5", "http", "vmess", "shadowsocks":
		return true
	default:
		return false
	}
}

// SelectInboundUser resolves an explicit user/profile or an unambiguous single user.
// Callers must expand user_refs before using the returned credentials.
func (c ClashUpstream) SelectInboundUser(inbound Inbound) (InboundUser, error) {
	if c.Type == "xray-socks5" && inbound.Protocol != "socks5" {
		return InboundUser{}, fmt.Errorf("xray-socks5 ref must target socks5 inbound: %s", c.Ref)
	}
	if !SupportsInboundUpstream(inbound.Protocol) {
		return InboundUser{}, fmt.Errorf("unsupported referenced inbound protocol: %s", inbound.Protocol)
	}
	if inbound.Protocol == "socks5" || inbound.Protocol == "http" {
		if c.User != "" || c.Profile != "" {
			return InboundUser{}, fmt.Errorf("user/profile selection is only supported for vmess/shadowsocks references")
		}
		return InboundUser{}, nil
	}
	if c.User == "" {
		if c.Profile != "" {
			return InboundUser{}, fmt.Errorf("user is required when profile is specified")
		}
		if len(inbound.Users) == 1 {
			return inbound.Users[0], nil
		}
		if inbound.Protocol == "shadowsocks" && len(inbound.Users) == 0 {
			return InboundUser{}, nil
		}
		return InboundUser{}, fmt.Errorf("select an inbound user/profile for %s", c.Ref)
	}
	for _, user := range inbound.Users {
		if user.User == c.User && user.ProfileOrDefault() == NormalizeUserProfile(c.Profile) {
			return user, nil
		}
	}
	return InboundUser{}, fmt.Errorf("selected user/profile is not attached to the referenced inbound: %s", c.Ref)
}
