package mihomo

import (
	"gopkg.in/yaml.v3"

	"proxystack-web/internal/domain"
)

// renderProviders delegates downloading and parsing to Mihomo; the panel schedules refreshes.
func renderProviders(providers []domain.ClashProvider) *yaml.Node {
	pairs := make([]nodePair, 0, len(providers))
	for _, provider := range providers {
		fields := []nodePair{
			pair("type", scalar("http")),
			pair("url", scalar(provider.URL)),
			pair("path", scalar(provider.Path)),
			pair("interval", intScalar(0)),
			pair("proxy", scalar(provider.Proxy)),
			pair("size-limit", intScalar(16<<20)),
			pair("override", mapping(pair("additional-prefix", scalar(provider.Name+" | ")))),
		}
		if provider.Filter != "" {
			fields = append(fields, pair("filter", scalar(provider.Filter)))
		}
		if provider.ExcludeFilter != "" {
			fields = append(fields, pair("exclude-filter", scalar(provider.ExcludeFilter)))
		}
		pairs = append(pairs, pair(provider.Name, mapping(fields...)))
	}
	return mapping(pairs...)
}
