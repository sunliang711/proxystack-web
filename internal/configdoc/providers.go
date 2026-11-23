package configdoc

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"proxystack-web/internal/domain"
)

// ProviderDirectNode is a provider node selected for direct subscription delivery.
type ProviderDirectNode struct {
	Name            string     `json:"name" yaml:"name"`
	Config          RawMapping `json:"config,omitempty" yaml:"config,omitempty"`
	EncryptedConfig string     `json:"encrypted_config,omitempty" yaml:"encrypted_config,omitempty"`
}

// Provider is a dynamic upstream collection owned by one stack.
type Provider struct {
	ID            int64                `json:"id,omitempty" yaml:"-"`
	Name          string               `json:"name" yaml:"name"`
	URL           string               `json:"url" yaml:"url"`
	FetchMode     string               `json:"fetch_mode" yaml:"fetch_mode" enum:"direct,system"`
	AutoUpdate    bool                 `json:"auto_update" yaml:"auto_update"`
	Interval      int                  `json:"interval" yaml:"interval" minimum:"60" maximum:"2592000"`
	Filter        string               `json:"filter" yaml:"filter,omitempty"`
	ExcludeFilter string               `json:"exclude_filter" yaml:"exclude_filter,omitempty"`
	DirectNodes   []ProviderDirectNode `json:"direct_nodes,omitempty" yaml:"direct_nodes,omitempty"`
}

// ProviderDownloadProxy is reserved for subscription downloads, outside business groups.
const ProviderDownloadProxy = "__psw_download_proxy"

// RuntimeName keeps controller names stable when a saved provider is renamed.
func (p Provider) RuntimeName() string {
	if p.ID > 0 {
		return fmt.Sprintf("__psw_provider_%d", p.ID)
	}
	return fmt.Sprintf("__psw_provider_%x", sha256.Sum256([]byte(p.Name)))
}

// CachePath changes when the remote URL changes, preventing reuse of another subscription.
func (p Provider) CachePath() string {
	hash := sha256.Sum256([]byte(p.URL))
	return fmt.Sprintf("./providers/%s-%x.yaml", p.RuntimeName(), hash[:12])
}

// validateProviders checks references and bounds; regex syntax is validated by the native core.
func validateProviders(issues *Issues, doc *Document, path string, stack *Stack) map[string]bool {
	names := map[string]bool{}
	ids := map[int64]bool{}
	for index, provider := range stack.Mihomo.Providers {
		field := fmt.Sprintf("%s.providers[%d]", path, index)
		checkIdentifier(issues, field+".name", provider.Name)
		if names[provider.Name] {
			issues.add(field+".name", "duplicate provider name")
		}
		names[provider.Name] = true
		if provider.ID < 0 || (provider.ID != 0 && ids[provider.ID]) {
			issues.add(field+".id", "invalid or duplicate provider ID")
		}
		ids[provider.ID] = true
		parsed, err := url.Parse(provider.URL)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
			issues.add(field+".url", "must be an HTTP(S) subscription URL without userinfo or fragment")
		}
		checkEnum(issues, field+".fetch_mode", provider.FetchMode, "direct", "system")
		if provider.Interval < 60 || provider.Interval > 2592000 {
			issues.add(field+".interval", "must be between 60 and 2592000 seconds")
		}
		for key, pattern := range map[string]string{"filter": provider.Filter, "exclude_filter": provider.ExcludeFilter} {
			if len(pattern) > 4096 {
				issues.add(field+"."+key, "regular expression is too long")
			}
		}
		seenDirect := map[string]bool{}
		for dnIndex, dn := range provider.DirectNodes {
			dnField := fmt.Sprintf("%s.direct_nodes[%d]", field, dnIndex)
			if dn.Name == "" {
				issues.add(dnField+".name", "is required")
				continue
			}
			if seenDirect[dn.Name] {
				issues.add(dnField+".name", "duplicate direct node: %s", dn.Name)
			}
			seenDirect[dn.Name] = true
			if dn.Config.IsZero() {
				issues.add(dnField+".config", "config is required")
				continue
			}
			if err := CheckDirectRawConfig(dn.Config); err != nil {
				issues.add(dnField+".config", "%s", err.Error())
			}
		}
		if provider.FetchMode != "system" || doc.Settings == nil {
			continue
		}
		proxy := doc.Settings.DownloadProxy
		if proxy.Mode == ProxyModeCustom && proxy.Protocol == "socks5" && !proxy.RemoteDNS {
			issues.add(field+".fetch_mode", "Mihomo providers require remote DNS for a custom SOCKS5 download proxy; enable it in system settings")
		}
		if proxy.Mode == ProxyModeStack && proxy.Stack == stack.Name {
			issues.add(field+".fetch_mode", "the download proxy must not use this stack's own listener")
		}
	}
	return names
}

// compileProviders translates provider references and the shared download proxy into native Mihomo fields.
func compileProviders(doc *Document, source Stack, target *domain.Stack) error {
	settings := DefaultSettings()
	if doc.Settings != nil {
		settings = *doc.Settings
	}
	names := map[string]string{}
	usesSystem := false
	for _, provider := range source.Mihomo.Providers {
		names[provider.Name] = provider.RuntimeName()
		proxy, dependency := BuiltinDirect, ""
		if provider.FetchMode == "system" && settings.DownloadProxy.Mode != ProxyModeDirect {
			usesSystem = true
			proxy = ProviderDownloadProxy
			if settings.DownloadProxy.Mode == ProxyModeStack {
				dependency = settings.DownloadProxy.Stack
			}
		}
		target.Clash.Providers = append(target.Clash.Providers, domain.ClashProvider{
			Name: provider.RuntimeName(), URL: provider.URL, Path: provider.CachePath(), Proxy: proxy,
			Filter: provider.Filter, ExcludeFilter: provider.ExcludeFilter, DownloadStack: dependency,
		})
	}
	for index, group := range source.Mihomo.Groups {
		for _, name := range group.Use {
			target.Clash.Groups[index].Use = append(target.Clash.Groups[index].Use, names[name])
		}
	}
	if !usesSystem {
		return nil
	}
	proxy := settings.DownloadProxy
	protocol, host, port := proxy.Protocol, proxy.Host, proxy.Port
	username, password := proxy.Username, proxy.Password
	if proxy.Mode == ProxyModeStack {
		index := doc.StackByName(proxy.Stack)
		if index < 0 {
			return fmt.Errorf("download proxy stack does not exist")
		}
		listener := doc.Stacks[index].Mihomo.Socks
		protocol = "socks5"
		if proxy.Listener == "http" {
			listener, protocol = doc.Stacks[index].Mihomo.HTTP, "http"
		}
		host, port = listener.Listen, listener.Port
		if host == "0.0.0.0" {
			host = "127.0.0.1"
		} else if host == "::" {
			host = "::1"
		}
		username, password = "", ""
		if listener.Auth == ListenerAuthAccounts && len(listener.Accounts) > 0 {
			username, password = listener.Accounts[0].Username, listener.Accounts[0].Password
		}
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	var node yaml.Node
	if err := node.Encode(map[string]any{"type": protocol, "server": host, "port": port, "username": username, "password": password}); err != nil {
		return err
	}
	upstream := domain.ClashUpstream{Name: ProviderDownloadProxy, Type: "raw"}
	if err := upstream.SetRawConfig(&node); err != nil {
		return err
	}
	target.Clash.DownloadProxy = &upstream
	return nil
}

// RenameProviderReferences keeps group references aligned with provider renames made through the API.
func RenameProviderReferences(stack *Stack, old Stack) {
	for _, previous := range old.Mihomo.Providers {
		for _, provider := range stack.Mihomo.Providers {
			if provider.ID == 0 || provider.ID != previous.ID || provider.Name == previous.Name || slices.ContainsFunc(stack.Mihomo.Providers, func(p Provider) bool { return p.Name == previous.Name }) {
				continue
			}
			for index := range stack.Mihomo.Groups {
				for position, name := range stack.Mihomo.Groups[index].Use {
					if name == previous.Name {
						stack.Mihomo.Groups[index].Use[position] = provider.Name
					}
				}
			}
		}
	}
}
