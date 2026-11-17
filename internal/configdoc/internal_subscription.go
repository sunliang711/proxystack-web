package configdoc

import "proxystack-web/internal/generator/sub"

// InternalNodeSuffix distinguishes an internal node from its public counterpart.
const InternalNodeSuffix = " [LAN]"

// InternalNode returns an internal-address copy when the node's user is authorized.
func InternalNode(doc *Document, node sub.Node) (sub.Node, bool) {
	if doc == nil || doc.Settings == nil || doc.Settings.Basic.InternalHost == "" {
		return sub.Node{}, false
	}
	index := doc.UserByName(node.User)
	if index < 0 || !doc.Users[index].InternalNodes {
		return sub.Node{}, false
	}
	internal := node
	internal.ID += ":lan"
	internal.Tag += ":lan"
	internal.Remark += InternalNodeSuffix
	internal.Server = doc.Settings.Basic.InternalHost
	return internal, true
}

// AppendInternalNodes adds internal-address copies for authorized users.
// Callers must pass only locally generated regular stack nodes, before direct, manual, or remote nodes are merged.
func AppendInternalNodes(doc *Document, nodes []sub.Node) []sub.Node {
	if doc == nil || doc.Settings == nil || doc.Settings.Basic.InternalHost == "" {
		return nodes
	}
	out := make([]sub.Node, 0, len(nodes)*2)
	for _, node := range nodes {
		out = append(out, node)
		if internal, ok := InternalNode(doc, node); ok {
			out = append(out, internal)
		}
	}
	return out
}
