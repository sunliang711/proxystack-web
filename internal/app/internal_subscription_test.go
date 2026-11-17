package app_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
)

// TestInternalSubscriptionNodes verifies LAN copies across formats without duplicating non-stack sources.
func TestInternalSubscriptionNodes(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	externalHost := doc.Settings.Basic.ExternalHost
	doc.Settings.Basic.InternalHost = "10.2.1.115"
	doc.Settings.NodeExport.Enabled = true
	doc.Settings.NodeExport.Token = "internal-node-export-token"
	_, err = h.svc.UpdateSettings(ctx, h.actor, *doc.Settings)
	require.NoError(t, err)

	alice := doc.Users[doc.UserByName("alice")]
	alice.InternalNodes = true
	_, err = h.svc.UpdateUser(ctx, h.actor, alice.ID, alice)
	require.NoError(t, err)
	_, err = h.svc.SaveManualNodes(ctx, h.actor, append(doc.ManualNodes, configdoc.ManualNode{
		User: "alice", Mode: configdoc.NodeStandard, Protocol: "socks5", Server: "manual.example.com", Port: 1080,
		Remark: "Manual Relay", AuthType: "noauth",
	}))
	require.NoError(t, err)

	stack := doc.Stacks[doc.StackByName("usa1")]
	stack.DirectUsers = []string{"alice"}
	_, err = h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)

	subs := newSubServer(t, sourceOf(t, h, true))
	subs.deliver(t, publish(t, h))
	for _, format := range []string{"clash", "premium", "surge"} {
		status, body, _ := get(t, subs.http, "/s/alice-token/"+format)
		require.Equal(t, http.StatusOK, status, body)
		require.Contains(t, body, "usa1 vmess")
		require.Contains(t, body, "usa1 vmess [LAN]")
		require.Contains(t, body, externalHost)
		require.Contains(t, body, "10.2.1.115")
		require.Contains(t, body, "Manual Relay")
		require.NotContains(t, body, "Manual Relay [LAN]")
		require.Contains(t, body, "[DRT]")
		require.NotContains(t, body, "[DRT] [LAN]")
	}
	exported, ok, err := h.svc.ExportNodes(ctx, "internal-node-export-token")
	require.NoError(t, err)
	require.True(t, ok)
	for _, node := range exported.Nodes {
		require.NotContains(t, node.Remark, configdoc.InternalNodeSuffix)
		require.NotEqual(t, "10.2.1.115", node.Server)
	}
}

// TestInternalSubscriptionSkipsAppliedNameCollision verifies stale applied nodes cannot break subscription rendering.
func TestInternalSubscriptionSkipsAppliedNameCollision(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	firstIndex, secondIndex := doc.StackByName("usa1"), doc.StackByName("usa2")
	require.GreaterOrEqual(t, firstIndex, 0)
	require.GreaterOrEqual(t, secondIndex, 0)
	first, second := doc.Stacks[firstIndex], doc.Stacks[secondIndex]
	firstInbound := firstSubscriptionInbound(t, first, "alice")
	secondInbound := firstSubscriptionInbound(t, second, "alice")
	first.Xray.Inbounds[firstInbound].DisplayTemplate = "Collision"
	second.Xray.Inbounds[secondInbound].DisplayTemplate = "Collision [LAN]"
	_, err = h.svc.UpdateStack(ctx, h.actor, first.ID, first)
	require.NoError(t, err)
	_, err = h.svc.UpdateStack(ctx, h.actor, second.ID, second)
	require.NoError(t, err)
	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)

	second.Xray.Inbounds[secondInbound].Sub = false
	_, err = h.svc.UpdateStack(ctx, h.actor, second.ID, second)
	require.NoError(t, err)
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	doc.Settings.Basic.InternalHost = "10.2.1.115"
	_, err = h.svc.UpdateSettings(ctx, h.actor, *doc.Settings)
	require.NoError(t, err)
	alice := doc.Users[doc.UserByName("alice")]
	alice.InternalNodes = true
	_, err = h.svc.UpdateUser(ctx, h.actor, alice.ID, alice)
	require.NoError(t, err)

	subs := newSubServer(t, sourceOf(t, h, true))
	subs.deliver(t, publish(t, h))
	status, body, _ := get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	var rendered struct {
		Proxies []struct {
			Name string `yaml:"name"`
		} `yaml:"proxies"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(body), &rendered))
	count := 0
	for _, proxy := range rendered.Proxies {
		if proxy.Name == "Collision [LAN]" {
			count++
		}
	}
	require.Equal(t, 1, count, "the applied public node must remain and the conflicting LAN copy must be skipped")
}

// TestInternalSubscriptionSkipsManualNameCollision verifies manual nodes reserve names before LAN copies are added.
func TestInternalSubscriptionSkipsManualNameCollision(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stack := doc.Stacks[doc.StackByName("usa1")]
	inboundIndex := firstSubscriptionInbound(t, stack, "alice")
	stack.Xray.Inbounds[inboundIndex].DisplayTemplate = "Collision"
	_, err = h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)

	stack.Xray.Inbounds[inboundIndex].Sub = false
	_, err = h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	doc.Settings.Basic.InternalHost = "10.2.1.115"
	_, err = h.svc.UpdateSettings(ctx, h.actor, *doc.Settings)
	require.NoError(t, err)
	_, err = h.svc.SaveManualNodes(ctx, h.actor, append(doc.ManualNodes, configdoc.ManualNode{
		User: "alice", Mode: configdoc.NodeStandard, Protocol: "socks5", Server: "manual.example.com", Port: 1080,
		Remark: "Collision [LAN]", AuthType: "noauth",
	}))
	require.NoError(t, err)
	alice := doc.Users[doc.UserByName("alice")]
	alice.InternalNodes = true
	_, err = h.svc.UpdateUser(ctx, h.actor, alice.ID, alice)
	require.NoError(t, err)

	subs := newSubServer(t, sourceOf(t, h, true))
	subs.deliver(t, publish(t, h))
	status, body, _ := get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	var rendered struct {
		Proxies []struct {
			Name   string `yaml:"name"`
			Server string `yaml:"server"`
		} `yaml:"proxies"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(body), &rendered))
	count := 0
	for _, proxy := range rendered.Proxies {
		if proxy.Name == "Collision [LAN]" {
			count++
			require.Equal(t, "manual.example.com", proxy.Server)
		}
	}
	require.Equal(t, 1, count, "the manual node must remain and the conflicting LAN copy must be skipped")
}

// firstSubscriptionInbound returns the first subscribed inbound that serves the user.
func firstSubscriptionInbound(t *testing.T, stack configdoc.Stack, user string) int {
	t.Helper()
	for index, inbound := range stack.Xray.Inbounds {
		if !inbound.Sub {
			continue
		}
		for _, ref := range inbound.Users {
			if ref.User == user {
				return index
			}
		}
	}
	t.Fatalf("stack %s has no subscribed inbound for %s", stack.Name, user)
	return -1
}
