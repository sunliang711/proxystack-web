package mihomo

import (
	"fmt"

	"gopkg.in/yaml.v3"
	"proxystack-web/internal/domain"
	"proxystack-web/internal/generator/sub"
	"proxystack-web/internal/graph"
)

// renderXrayInboundProxy converts a local inbound and selected credentials into a Mihomo proxy.
func renderXrayInboundProxy(stackSet domain.StackSet, referenceGraph graph.ReferenceGraph, upstream domain.ClashUpstream) (*yaml.Node, error) {
	endpoint, ok := referenceGraph.Index.ResolveXrayInbound(upstream.Ref)
	if !ok {
		return nil, fmt.Errorf("xray inbound ref does not exist: %s", upstream.Ref)
	}
	target := stackSet.ByName()[endpoint.Stack]
	for _, inbound := range target.Xray.Inbounds {
		if inbound.Name != endpoint.Name {
			continue
		}
		user, err := upstream.SelectInboundUser(inbound)
		if err != nil {
			return nil, err
		}
		node := sub.Node{
			Remark: upstream.Name, Protocol: inbound.Protocol,
			Server: domain.NormalizeInternalEndpointAddress(inbound.Listen), Port: inbound.Port,
			UUID: user.UUID, Network: inbound.Network, WSOpts: inbound.WSOpts, GRPCOpts: inbound.GRPCOpts,
			Method: inbound.Method, Password: inbound.Password,
		}
		// Xray calls TCP transport "raw"; Mihomo expects "tcp".
		if node.Network == "raw" || node.Network == "" {
			node.Network = "tcp"
		}
		if inbound.Protocol != "http" {
			udp := inbound.UDPEnabled()
			node.UDP = &udp
		}
		if inbound.Auth != nil {
			node.Auth = &sub.Auth{Type: inbound.Auth.Type, Username: inbound.Auth.Username, Password: inbound.Auth.Password}
		}
		if inbound.Protocol == "shadowsocks" && user.User != "" {
			node.Password = user.Password
			if domain.IsShadowsocks2022Method(inbound.Method) {
				node.Password = inbound.Password + ":" + user.Password
			}
		}
		return sub.RenderClashProxy(node), nil
	}
	return nil, fmt.Errorf("xray inbound ref does not exist: %s", upstream.Ref)
}
