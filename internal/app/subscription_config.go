package app

import (
	"context"
	"fmt"

	"proxystack-web/internal/configdoc"
)

// 订阅相关配置的审计动作。
const (
	ActionConfigManualNodes   = "config.manual_nodes"
	ActionConfigRemoteSources = "config.remote_sources"
	ActionConfigTemplates     = "config.templates"
)

// SaveManualNodes 整体保存手工节点列表，仍为占位的凭据按 ID 保持原值。
func (s *Service) SaveManualNodes(ctx context.Context, actor Actor, nodes []configdoc.ManualNode) ([]configdoc.ManualNode, error) {
	change := &ConfigChange{Action: ActionConfigManualNodes, Object: "manual_nodes", Summary: fmt.Sprintf("保存手工节点（%d 个）", len(nodes)), Scope: "manual_nodes"}
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		configdoc.UnmaskManualNodes(nodes, doc.ManualNodes)
		doc.ManualNodes = nodes
		return nil
	})
	if err != nil {
		return nil, err
	}
	return doc.ManualNodes, nil
}

// SaveRemoteSources 整体保存远端节点源列表，仍为占位的 token 按 ID 保持原值。
func (s *Service) SaveRemoteSources(ctx context.Context, actor Actor, sources []configdoc.RemoteSource) ([]configdoc.RemoteSource, error) {
	change := &ConfigChange{Action: ActionConfigRemoteSources, Object: "remote_sources", Summary: fmt.Sprintf("保存远端节点源（%d 个）", len(sources)), Scope: "remote_sources"}
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		configdoc.UnmaskRemoteSources(sources, doc.RemoteSources)
		doc.RemoteSources = sources
		return nil
	})
	if err != nil {
		return nil, err
	}
	return doc.RemoteSources, nil
}

// SaveTemplates 保存自定义订阅模板；空字符串恢复内置模板。
func (s *Service) SaveTemplates(ctx context.Context, actor Actor, templates configdoc.Templates) (*configdoc.Templates, error) {
	change := &ConfigChange{Action: ActionConfigTemplates, Object: "templates", Summary: "保存订阅模板", Scope: "templates"}
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		doc.Templates = &templates
		return nil
	})
	if err != nil {
		return nil, err
	}
	return doc.Templates, nil
}
