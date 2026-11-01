package configdoc

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"proxystack-web/internal/domain"
	"proxystack-web/internal/generator/sub"
	"proxystack-web/internal/subscription"
)

// SubNodeID 是手工节点在订阅中的节点 ID。
func (n ManualNode) SubNodeID(position int) string {
	if n.ID != 0 {
		return "manual:" + strconv.FormatInt(n.ID, 10)
	}
	return "manual:new-" + strconv.Itoa(position)
}

// SubNode 把手工节点翻译为订阅节点。
func (n ManualNode) SubNode(position int) (sub.Node, error) {
	id := n.SubNodeID(position)
	tag := n.Tag
	if tag == "" {
		tag = id
	}
	if n.Mode == NodeDirect {
		return n.directNode(id, tag)
	}
	node := sub.Node{
		ID: id, User: n.User, Protocol: n.Protocol, Server: n.Server, Port: n.Port, Tag: tag,
		Remark: n.Remark, Region: n.Region,
	}
	if domain.SupportsInboundUDPProtocol(n.Protocol) {
		udp := n.UDP
		node.UDP = &udp
	}
	switch n.Protocol {
	case "vmess":
		node.UUID, node.Network = n.UUID, n.Network
		switch n.Network {
		case "ws":
			node.WSOpts = &domain.WebSocketOptions{Path: n.WSPath, Headers: n.WSHeaders}
		case "grpc":
			node.GRPCOpts = &domain.GRPCOptions{ServiceName: n.GRPCServiceName}
		}
	case "shadowsocks":
		node.Method, node.Cipher, node.Password = n.Method, n.Method, n.Password
	case "socks5", "http":
		authType := n.AuthType
		if authType == "" {
			authType = "noauth"
		}
		node.Auth = &sub.Auth{Type: authType}
		if authType == "password" {
			node.Auth.Username, node.Auth.Password = n.AuthUsername, n.AuthPassword
		}
	}
	return node, node.Validate()
}

// directNode 用节点 ID、用户和 mihomo 原生字段拼出 direct 节点，交给订阅生成器按原样输出。
func (n ManualNode) directNode(id, tag string) (sub.Node, error) {
	if n.Config.Node == nil {
		return sub.Node{}, fmt.Errorf("config is required for a direct node")
	}
	scalar := func(value string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value} }
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	add := func(key string, value *yaml.Node) { mapping.Content = append(mapping.Content, scalar(key), value) }
	add("id", scalar(id))
	add("user", scalar(n.User))
	add("direct", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	add("tag", scalar(tag))
	add("remark", scalar(n.Remark))
	if n.Region != "" {
		add("region", scalar(n.Region))
	}
	skip := map[string]bool{"id": true, "user": true, "direct": true, "tag": true, "remark": true, "region": true}
	hasName := false
	for index := 0; index+1 < len(n.Config.Node.Content); index += 2 {
		key := n.Config.Node.Content[index].Value
		if key == "name" {
			hasName = true
			add("name", scalar(n.Remark))
			continue
		}
		if !skip[key] {
			add(key, cloneNode(n.Config.Node.Content[index+1]))
		}
	}
	if !hasName {
		add("name", scalar(n.Remark))
	}
	var node sub.Node
	if err := mapping.Decode(&node); err != nil {
		return sub.Node{}, err
	}
	return node, node.Validate()
}

// validateSubscription 校验订阅设置、节点导出、手工节点、远端节点源和模板。
func validateSubscription(issues *Issues, doc *Document) {
	if doc.Settings != nil {
		settings := doc.Settings.Subscription
		path := "settings.subscription"
		if !subscription.ValidSourceName(settings.SourceName) {
			issues.add(path+".source_name", "must be 1-32 lowercase letters, digits, '_' or '-'")
		}
		if settings.PublicBaseURL != "" {
			parsed, err := url.Parse(settings.PublicBaseURL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
				issues.add(path+".public_base_url", "must be an http(s) URL without query or fragment")
			}
		}
		checkEnum(issues, path+".disabled_response", settings.DisabledResponse, DisabledNotFound, DisabledEmpty)
		if doc.Settings.NodeExport.Enabled && len(doc.Settings.NodeExport.Token) < 16 {
			issues.add("settings.node_export.token", "a token of at least 16 characters is required")
		}
	}
	for index, node := range doc.ManualNodes {
		path := fmt.Sprintf("manual_nodes[%d]", index)
		before := len(*issues)
		if doc.UserByName(node.User) < 0 {
			issues.add(path+".user", "user does not exist: %s", node.User)
		}
		checkEnum(issues, path+".mode", node.Mode, NodeStandard, NodeDirect)
		if node.Remark == "" {
			issues.add(path+".remark", "is required")
		}
		if node.Mode == NodeStandard {
			if node.Server == "" || !validHost(node.Server) {
				issues.add(path+".server", "must be a domain name or IP address")
			}
			checkPort(issues, path+".port", node.Port)
			checkEnum(issues, path+".protocol", node.Protocol, "vmess", "shadowsocks", "socks5", "http")
		}
		if node.Region != "" && !regionPattern.MatchString(node.Region) {
			issues.add(path+".region", "must be a two-letter country code")
		}
		if len(*issues) > before {
			continue
		}
		if _, err := node.SubNode(index); err != nil {
			field := ".config"
			if node.Mode == NodeStandard {
				field = ""
			}
			issues.add(path+field, "%s", err.Error())
		}
	}
	names := map[string]bool{}
	for index, source := range doc.RemoteSources {
		path := fmt.Sprintf("remote_sources[%d]", index)
		checkIdentifier(issues, path+".name", source.Name)
		if names[source.Name] {
			issues.add(path+".name", "duplicate remote source name: %s", source.Name)
		}
		names[source.Name] = true
		if parsed, err := url.Parse(source.URL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			issues.add(path+".url", "must be an http(s) URL")
		}
		if source.Token == "" {
			issues.add(path+".token", "is required")
		}
	}
	if doc.Templates != nil {
		for field, source := range map[string]string{"clash": doc.Templates.Clash, "premium": doc.Templates.Premium, "surge": doc.Templates.Surge} {
			if source == "" {
				continue
			}
			if err := sub.CheckTemplate(source); err != nil {
				issues.add("templates."+field, "%s", err.Error())
			}
		}
	}
}

// validateSubscriptionNodes 把本机入站节点和手工节点合在一起，检查节点 tag 全局唯一、同一用户下代理名不重复。
func validateSubscriptionNodes(issues *Issues, doc *Document, set domain.StackSet) {
	// 这里只检查重名；没有对外地址时节点缺 server，用占位地址代替，不阻止保存。
	if set.Config.ExternalHost == "" {
		set.Config.ExternalHost = "external-host.invalid"
	}
	input, err := sub.RenderStackInputAt(set, "local", "now")
	if err != nil {
		issues.add("", "subscription nodes: %s", err.Error())
		return
	}
	tags := map[string]string{}
	names := map[string]string{}
	for _, node := range input.Nodes {
		tags[node.Tag] = "stack node " + node.ID
		names[node.User+"\x00"+node.Remark] = "stack node " + node.ID
	}
	direct, err := StackDirectNodes(doc)
	if err != nil {
		issues.add("", "direct subscription nodes: %s", err.Error())
		return
	}
	for _, node := range direct {
		stackName, _, _ := strings.Cut(node.ID, ":")
		path := fmt.Sprintf("stacks[%d].direct_users", doc.StackByName(stackName))
		if first, ok := tags[node.Tag]; ok {
			issues.add(path, "duplicate node tag %s, also used by %s", node.Tag, first)
		}
		tags[node.Tag] = path
		key := node.User + "\x00" + node.Remark
		if first, ok := names[key]; ok {
			issues.add(path, "user %s already has a proxy named %s (%s)", node.User, node.Remark, first)
		}
		names[key] = path
	}
	for index, manual := range doc.ManualNodes {
		node, err := manual.SubNode(index)
		if err != nil {
			continue
		}
		path := fmt.Sprintf("manual_nodes[%d]", index)
		if first, ok := tags[node.Tag]; ok {
			issues.add(path+".tag", "duplicate node tag %s, also used by %s", node.Tag, first)
		}
		tags[node.Tag] = path
		key := node.User + "\x00" + node.Remark
		if first, ok := names[key]; ok {
			issues.add(path+".remark", "user %s already has a proxy named %s (%s)", node.User, node.Remark, first)
		}
		names[key] = path
	}
}
