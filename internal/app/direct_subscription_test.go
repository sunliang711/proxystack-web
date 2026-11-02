package app_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
)

// TestDirectUpstreamsSubscription 验证真实签名包导入后所有订阅格式的授权隔离及撤销，不依赖本机应用。
func TestDirectUpstreamsSubscription(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
	subs := newSubServer(t, sourceOf(t, h, true))
	bob, err := h.svc.CreateUser(ctx, h.actor, configdoc.User{Name: "bob", Enabled: true, ResetMode: "none"})
	require.NoError(t, err)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	stack := doc.Stacks[doc.StackByName("usa1")]
	stack.Xray.Inbounds[0].Sub = false
	stack.DirectUsers = []string{"alice", "bob"}
	var raw configdoc.RawMapping
	require.NoError(t, yaml.Unmarshal([]byte("type: http\nserver: remote-http.example.com\nport: 8443\ntls: true\nusername: upstream-user\npassword: upstream-password\nsni: http.example.com"), &raw))
	stack.Mihomo.Upstreams = append(stack.Mihomo.Upstreams, configdoc.Upstream{Name: "server-b", Type: "raw", Config: raw},
		configdoc.Upstream{Name: "local-ref", Type: "xray-socks5", Stack: "usa2", Inbound: "relay"})
	_, err = h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)

	// 普通节点仍来自已应用版本；直连取当前保存的授权和上游，且不要求 bob 属于任何入站。
	subs.deliver(t, publish(t, h))
	for _, format := range []string{"clash", "premium", "surge"} {
		status, body, _ := get(t, subs.http, "/s/alice-token/"+format)
		require.Equal(t, http.StatusOK, status, body)
		require.Contains(t, body, "usa1 vmess")
		require.Contains(t, body, "usa1 relay socks")
		require.Contains(t, body, "usa1 vmess · server-a [DRT]")
		require.Contains(t, body, "usa1 vmess · server-b [DRT]")
		require.Contains(t, body, "remote-http.example.com")
		require.Contains(t, body, "upstream-user")
		require.Contains(t, body, "upstream-password")
		require.NotContains(t, body, "local-ref[DRT]")
		status, body, _ = get(t, subs.http, "/s/"+bob.SubToken+"/"+format)
		require.Equal(t, http.StatusOK, status, body)
		require.Contains(t, body, "usa1 vmess · server-a [DRT]")
		require.NotContains(t, body, "usa2 relay socks")
		if format == "surge" {
			require.Contains(t, body, `https, remote-http.example.com, 8443, username="upstream-user", password="upstream-password", sni=http.example.com`)
		} else {
			require.Contains(t, body, "tls: true")
		}
	}
	stack.DirectSuffix = " [直连]"
	_, err = h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	subs.deliver(t, publish(t, h))
	for _, format := range []string{"clash", "premium", "surge"} {
		status, renamed, _ := get(t, subs.http, "/s/alice-token/"+format)
		require.Equal(t, http.StatusOK, status, renamed)
		require.Contains(t, renamed, "usa1 vmess · server-a [直连]")
		require.Contains(t, renamed, "usa1 vmess · server-b [直连]")
		require.NotContains(t, renamed, "[DRT]")
	}
	// 逐 Stack 暂停、到期、停用和撤销授权均在下次发布后生效。
	_, err = h.svc.SetUserPause(ctx, h.actor, bob.ID, true, []string{"usa1"})
	require.NoError(t, err)
	subs.deliver(t, publish(t, h))
	_, body, _ := get(t, subs.http, "/s/"+bob.SubToken+"/clash")
	require.NotContains(t, body, "[直连]")
	_, err = h.svc.SetUserPause(ctx, h.actor, bob.ID, true, nil)
	require.NoError(t, err)
	for _, state := range []string{"expired", "disabled", "revoked"} {
		bob.Enabled, bob.ExpireAt = true, ""
		switch state {
		case "expired":
			bob.ExpireAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		case "disabled":
			bob.Enabled = false
		case "revoked":
			stack.DirectUsers = []string{"alice"}
			_, err = h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
			require.NoError(t, err)
		}
		_, err = h.svc.UpdateUser(ctx, h.actor, bob.ID, *bob)
		require.NoError(t, err)
		subs.deliver(t, publish(t, h))
		_, body, _ = get(t, subs.http, "/s/"+bob.SubToken+"/clash")
		require.NotContains(t, body, "[直连]", state)
		require.NotContains(t, body, "upstream-password", state)
	}
	_, body, _ = get(t, subs.http, "/s/alice-token/clash")
	require.Contains(t, body, "[直连]", "revocation must not remove another user's grant")
}

// TestDirectAndAppliedNodesUseCurrentDisplayTemplates 验证普通节点与直连节点共用当前命名优先级，连接参数仍来自已应用版本。
func TestDirectAndAppliedNodesUseCurrentDisplayTemplates(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	alice := doc.Users[doc.UserByName("alice")]
	stack := doc.Stacks[doc.StackByName("usa1")]
	stack.Xray.Inbounds[0].Sub = false
	stack.Xray.Inbounds[1].DisplayTemplate = "{{ .stack | toUpper }} {{ .user }}"
	stack.DirectUsers = []string{"alice"}
	_, err = h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	alice.DisplayTemplate = "{{ .stack | toUpper }}"
	_, err = h.svc.UpdateUser(ctx, h.actor, alice.ID, alice)
	require.NoError(t, err)
	id, err := h.svc.Apply(ctx, h.actor, []string{"usa1"})
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)

	// 清空用户模板后不重新应用，普通节点和直连节点都应立即使用当前入站模板。
	alice.DisplayTemplate = ""
	_, err = h.svc.UpdateUser(ctx, h.actor, alice.ID, alice)
	require.NoError(t, err)
	preview, err := h.svc.PreviewSubscription(ctx, alice.ID, "clash", nil)
	require.NoError(t, err)
	require.Contains(t, preview, "name: USA1 alice\n")
	require.Contains(t, preview, "name: USA1 alice [DRT]\n")

	// 入站模板也清空时，两类节点共同回退到当前全局模板。
	stack.Xray.Inbounds[1].DisplayTemplate = ""
	_, err = h.svc.UpdateStack(ctx, h.actor, stack.ID, stack)
	require.NoError(t, err)
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Subscription.DisplayTemplate = "GLOBAL {{ .stack }} {{ .user }}"
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
	preview, err = h.svc.PreviewSubscription(ctx, alice.ID, "clash", nil)
	require.NoError(t, err)
	require.Contains(t, preview, "name: GLOBAL usa1 alice\n")
	require.Contains(t, preview, "name: GLOBAL usa1 alice [DRT]\n")
}
