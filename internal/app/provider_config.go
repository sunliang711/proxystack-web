package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/runtime"
	"proxystack-web/internal/units"
)

// validateNativeProviderFilters uses the installed core for all regex syntax, without adding a different regex engine.
func (s *Service) validateNativeProviderFilters(ctx context.Context, before, doc *configdoc.Document) configdoc.Issues {
	var issues configdoc.Issues
	for stackIndex, stack := range doc.Stacks {
		for index, provider := range stack.Mihomo.Providers {
			for field, pattern := range map[string]string{"filter": provider.Filter, "exclude_filter": provider.ExcludeFilter} {
				if pattern == "" || unchangedProviderFilter(before, provider, field, pattern) {
					continue
				}
				// Use a dummy URL: validation must never fetch or expose real subscription credentials.
				content, err := yaml.Marshal(map[string]any{"proxy-providers": map[string]any{"validation": map[string]any{
					"type": "http", "url": "http://127.0.0.1:1/subscription", strings.ReplaceAll(field, "_", "-"): pattern,
				}}})
				if err == nil {
					err = s.Checker.Check(ctx, units.Mihomo, "provider-filter-validation-"+configdoc.RandomToken(), content)
				}
				if err != nil {
					message := "invalid regular expression or unsupported by the installed Mihomo version"
					if errors.Is(err, runtime.ErrBinaryMissing) {
						message = "install Mihomo before configuring regular expression filters"
					}
					issues = append(issues, configdoc.Issue{Path: fmt.Sprintf("stacks[%d].mihomo.providers[%d].%s", stackIndex, index, field), Message: message})
				}
			}
		}
	}
	return issues
}

// providerFiltersChanged identifies mutations that require an external check before committing.
func providerFiltersChanged(before, doc *configdoc.Document) bool {
	for _, stack := range doc.Stacks {
		for _, provider := range stack.Mihomo.Providers {
			if (provider.Filter != "" && !unchangedProviderFilter(before, provider, "filter", provider.Filter)) ||
				(provider.ExcludeFilter != "" && !unchangedProviderFilter(before, provider, "exclude_filter", provider.ExcludeFilter)) {
				return true
			}
		}
	}
	return false
}

// unchangedProviderFilter avoids rerunning the core when an unrelated configuration field changes.
func unchangedProviderFilter(before *configdoc.Document, source configdoc.Provider, field, pattern string) bool {
	if source.ID == 0 {
		return false
	}
	for _, stack := range before.Stacks {
		for _, previous := range stack.Mihomo.Providers {
			if source.ID == previous.ID {
				if field == "filter" {
					return previous.Filter == pattern
				}
				return previous.ExcludeFilter == pattern
			}
		}
	}
	return false
}

// redactProviderConfig hides subscription URLs and the private proxy credentials in generated previews.
func redactProviderConfig(content []byte) []byte {
	if len(content) == 0 {
		return content
	}
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil || len(root.Content) == 0 {
		return content
	}
	top := root.Content[0]
	providers := yamlField(top, "proxy-providers")
	if providers == nil {
		return content
	}
	for index := 1; index < len(providers.Content); index += 2 {
		if value := yamlField(providers.Content[index], "url"); value != nil {
			value.Value = "[REDACTED]"
		}
	}
	if proxies := yamlField(top, "proxies"); proxies != nil {
		for _, proxy := range proxies.Content {
			if name := yamlField(proxy, "name"); name != nil && name.Value == configdoc.ProviderDownloadProxy {
				for _, field := range []string{"username", "password"} {
					if value := yamlField(proxy, field); value != nil && value.Value != "" {
						value.Value = "[REDACTED]"
					}
				}
			}
		}
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return []byte("Provider configuration preview is unavailable")
	}
	return output.Bytes()
}

// yamlField locates a mapping value without changing YAML ordering or scalar styles.
func yamlField(node *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

// SanitizeProviderLog prevents native provider errors from exposing URLs or node credentials through log streams.
func SanitizeProviderLog(message string) string {
	if (strings.Contains(message, "[Provider]") || strings.Contains(message, "__psw_provider_")) && (strings.Contains(message, "error") || strings.Contains(message, "failed")) {
		return "[Provider] Subscription operation failed; check download access, format and filters"
	}
	return message
}
