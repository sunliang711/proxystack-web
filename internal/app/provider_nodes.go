package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"proxystack-web/internal/configdoc"
)

const providerNodeCacheLimit = 16 << 20

// ProviderNodeSummary identifies a current provider node without exposing its credentials.
type ProviderNodeSummary struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Server string `json:"server"`
	Port   string `json:"port"`
}

// ProviderNodeDetails contains one cached node configuration, masked unless explicitly revealed.
type ProviderNodeDetails struct {
	Node   ProviderNodeSummary `json:"node"`
	Config string              `json:"config"`
	Masked bool                `json:"masked"`
}

// providerNodeSnapshot keeps configuration material inside the service layer.
type providerNodeSnapshot struct {
	nodes   []ProviderNodeSummary
	configs map[string]map[string]any
}

// ListProviderNodes loads server and port information only when the node browser requests it.
func (s *Service) ListProviderNodes(ctx context.Context, stackID, providerID int64) ([]ProviderNodeSummary, error) {
	snapshot, err := s.providerNodeSnapshot(ctx, stackID, providerID)
	if err != nil {
		return nil, err
	}
	return snapshot.nodes, nil
}

// GetProviderNodeDetails audits explicit secret disclosure and otherwise masks nested credentials.
func (s *Service) GetProviderNodeDetails(ctx context.Context, actor Actor, stackID, providerID int64, key string, reveal bool) (*ProviderNodeDetails, error) {
	snapshot, err := s.providerNodeSnapshot(ctx, stackID, providerID)
	if err != nil {
		return nil, err
	}
	var selected *ProviderNodeSummary
	for index := range snapshot.nodes {
		if snapshot.nodes[index].Key == key {
			selected = &snapshot.nodes[index]
			break
		}
	}
	config, exists := snapshot.configs[key]
	if selected == nil || !exists {
		return nil, &FieldError{Field: "name", Message: "node is no longer available; refresh the node list"}
	}
	if reveal {
		if err := s.AuditSecretView(ctx, actor, fmt.Sprintf("provider_node:%d:%d", stackID, providerID)); err != nil {
			return nil, err
		}
	} else {
		maskProviderNode(config)
	}
	content, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("cannot encode provider node details")
	}
	return &ProviderNodeDetails{Node: *selected, Config: string(content), Masked: !reveal}, nil
}

// providerNodeSnapshot matches cached configurations only against the applied core's filtered membership.
func (s *Service) providerNodeSnapshot(ctx context.Context, stackID, providerID int64) (*providerNodeSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.lockProviderRuntime(ctx); err != nil {
		return nil, err
	}
	defer s.runtimeMu.Unlock()
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	stack, err := stackByID(doc, stackID)
	if err != nil {
		return nil, err
	}
	var source *configdoc.Provider
	for index := range stack.Mihomo.Providers {
		if stack.Mihomo.Providers[index].ID == providerID {
			source = &stack.Mihomo.Providers[index]
			break
		}
	}
	if source == nil {
		return nil, ErrNotFound
	}
	if err := s.requireRunning(ctx, stack.Name); err != nil {
		return nil, err
	}
	applied, _, err := s.providerRuntime(*stack)
	if err != nil {
		return nil, err
	}
	config, exists := applied.Providers[source.RuntimeName()]
	if !exists || applied.Controller == "" {
		return nil, &FieldError{Field: "provider", Message: "apply this provider before viewing its nodes"}
	}
	native, err := providerClient(applied).Providers(ctx)
	if err != nil {
		return nil, err
	}
	provider, exists := native[source.RuntimeName()]
	if !exists {
		return nil, &FieldError{Field: "provider", Message: "provider is not initialized"}
	}
	configs, err := readProviderNodeCache(s.Layout.MihomoWorkDir(stack.Name), config)
	if err != nil {
		return nil, err
	}
	out := &providerNodeSnapshot{nodes: []ProviderNodeSummary{}, configs: configs}
	for _, node := range provider.Proxies {
		key := strings.TrimPrefix(node.Name, source.RuntimeName()+" | ")
		cached := configs[key]
		out.nodes = append(out.nodes, ProviderNodeSummary{
			Key: key, Name: source.Name + " | " + key, Type: node.Type,
			Server: providerNodeScalar(cached["server"]), Port: providerNodeScalar(cached["port"]),
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// readProviderNodeCache reads only a bounded file inside the applied stack's provider directory.
func readProviderNodeCache(workdir string, provider map[string]any) (map[string]map[string]any, error) {
	path, _ := provider["path"].(string)
	path = filepath.Clean(path)
	if !filepath.IsLocal(path) || !strings.HasPrefix(path, "providers"+string(filepath.Separator)) {
		return nil, fmt.Errorf("provider cache path is invalid")
	}
	file, err := os.OpenInRoot(workdir, path)
	if err != nil {
		return nil, fmt.Errorf("provider node cache is unavailable; update the subscription first")
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, providerNodeCacheLimit+1))
	if err != nil || len(content) > providerNodeCacheLimit {
		return nil, fmt.Errorf("cannot read provider node cache")
	}
	var cache struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(content, &cache); err != nil || cache.Proxies == nil {
		return nil, &FieldError{Field: "provider", Message: "node details require a Clash/Mihomo YAML subscription cache"}
	}
	out := make(map[string]map[string]any, len(cache.Proxies))
	for _, node := range cache.Proxies {
		name, _ := node["name"].(string)
		if _, exists := out[name]; !exists && name != "" {
			out[name] = node
		}
	}
	return out, nil
}

// providerNodeScalar returns only scalar endpoint data, never arbitrary cached objects.
func providerNodeScalar(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case int:
		return strconv.Itoa(value)
	default:
		return ""
	}
}

// maskProviderNode handles credentials in protocol options and arbitrary nested transport headers.
func maskProviderNode(value any) {
	mask := func(key string) bool {
		key = strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
		for _, part := range []string{"password", "passwd", "passphrase", "uuid", "token", "secret", "privatekey", "presharedkey", "authorization"} {
			if strings.Contains(key, part) {
				return true
			}
		}
		return (strings.HasSuffix(key, "key") && !strings.Contains(key, "public")) || key == "username" || key == "auth" || key == "authstr" || key == "authstring" || key == "psk" || key == "headers" || key == "shortid" || key == "seed" || key == "protocolparam"
	}
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if mask(key) {
				value[key] = configdoc.MaskedValue
			} else {
				maskProviderNode(child)
			}
		}
	case map[any]any:
		for key, child := range value {
			if text, ok := key.(string); ok && mask(text) {
				value[key] = configdoc.MaskedValue
			} else {
				maskProviderNode(child)
			}
		}
	case []any:
		for _, child := range value {
			maskProviderNode(child)
		}
	}
}
