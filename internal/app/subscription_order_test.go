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

func clashProxyNames(t *testing.T, body string) []string {
	t.Helper()
	var parsed struct {
		Proxies []struct {
			Name string `yaml:"name"`
		} `yaml:"proxies"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(body), &parsed))
	names := make([]string, 0, len(parsed.Proxies))
	for _, proxy := range parsed.Proxies {
		names = append(names, proxy.Name)
	}
	return names
}

// TestSubscriptionStackOrderAndUserIsolation 验证：
// 1. 默认按 Stack 名称标准升序排列，不受各自应用版本 revision_id 先后影响；
// 2. 同一 Stack 内交叉排列本机节点（含紧跟的 [LAN] 副本）与直连节点，手工节点排在后面；
// 3. 指定 stack_order 后按指定顺序置顶，未列入的 Stack 按标准升序排在末尾；
// 4. 用户不拥有的 Stack 或已暂停的 Stack 绝不会因排序而被包含。
func TestSubscriptionStackOrderAndUserIsolation(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)

	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	doc.Settings.Basic.InternalHost = "10.2.1.115"
	_, err = h.svc.UpdateSettings(ctx, h.actor, *doc.Settings)
	require.NoError(t, err)

	// alice 开启内网节点；新建只属于 usa2 的用户 bob。
	alice := doc.Users[doc.UserByName("alice")]
	alice.InternalNodes = true
	_, err = h.svc.UpdateUser(ctx, h.actor, alice.ID, alice)
	require.NoError(t, err)
	bob, err := h.svc.CreateUser(ctx, h.actor, configdoc.User{
		Name: "bob", Enabled: true, UUID: configdoc.RandomUUID(), SSPassword: "bob-ss-password",
		SubToken: "bob-token", InternalNodes: true, ResetMode: "none", DailyLimitAction: configdoc.DailyActionPause,
		PauseWindowMode: configdoc.PauseWindowInherit,
	})
	require.NoError(t, err)

	// usa2 同时授权 alice 与 bob；usa1 只授权 alice（含直连），使 bob 绝对不拥有 auto/usa1。
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	usa2 := doc.Stacks[doc.StackByName("usa2")]
	usa2.Xray.Inbounds[0].Users = append(usa2.Xray.Inbounds[0].Users, configdoc.UserRef{User: bob.Name})
	usa2.DirectUsers = []string{"alice"}
	_, err = h.svc.UpdateStack(ctx, h.actor, usa2.ID, usa2)
	require.NoError(t, err)

	usa1 := doc.Stacks[doc.StackByName("usa1")]
	usa1.DirectUsers = []string{"alice"}
	_, err = h.svc.UpdateStack(ctx, h.actor, usa1.ID, usa1)
	require.NoError(t, err)

	_, err = h.svc.SaveManualNodes(ctx, h.actor, []configdoc.ManualNode{{
		User: "alice", Mode: configdoc.NodeStandard, Protocol: "socks5", Server: "manual.example.com", Port: 1080,
		Remark: "Manual Node", AuthType: "noauth",
	}})
	require.NoError(t, err)

	// 故意先应用 usa2，再应用 auto，最后应用 usa1（使 usa1 拥有更大的 revision_id）。
	for _, stackName := range []string{"usa2", "auto", "usa1"} {
		id, err := h.svc.Apply(ctx, h.actor, []string{stackName})
		require.NoError(t, err)
		require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
	}

	subs := newSubServer(t, sourceOf(t, h, true))
	subs.deliver(t, publish(t, h))

	// 默认 stack_order=[]：按 Stack 标准升序 auto < usa1 < usa2，且按 Stack 交叉本机(含 LAN)+直连。
	status, body, _ := get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, []string{
		"auto relay socks",
		"auto relay socks [LAN]",
		"usa1 relay socks",
		"usa1 relay socks [LAN]",
		"usa1 vmess",
		"usa1 vmess [LAN]",
		"usa1 relay socks [DRT]",
		"usa2 relay socks",
		"usa2 relay socks [LAN]",
		"usa2 relay socks [DRT]",
		"Manual Node",
	}, clashProxyNames(t, body))

	// bob 只拥有 usa2：绝不包含 auto 或 usa1。
	status, body, _ = get(t, subs.http, "/s/bob-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, []string{
		"usa2 relay socks",
		"usa2 relay socks [LAN]",
	}, clashProxyNames(t, body))

	// 指定 stack_order: ["usa2", "auto"]，未列入的 usa1 按标准升序排在末尾。
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	doc.Settings.Subscription.StackOrder = []string{"usa2", "auto"}
	_, err = h.svc.UpdateSettings(ctx, h.actor, *doc.Settings)
	require.NoError(t, err)

	subs.deliver(t, publish(t, h))
	status, body, _ = get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, []string{
		"usa2 relay socks",
		"usa2 relay socks [LAN]",
		"usa2 relay socks [DRT]",
		"auto relay socks",
		"auto relay socks [LAN]",
		"usa1 relay socks",
		"usa1 relay socks [LAN]",
		"usa1 vmess",
		"usa1 vmess [LAN]",
		"usa1 relay socks [DRT]",
		"Manual Node",
	}, clashProxyNames(t, body))

	// 即使 stack_order 包含 usa2、auto、usa1，bob 也只收到自己拥有的 usa2，不拥有的绝不包含。
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	doc.Settings.Subscription.StackOrder = []string{"usa1", "auto", "usa2"}
	_, err = h.svc.UpdateSettings(ctx, h.actor, *doc.Settings)
	require.NoError(t, err)
	subs.deliver(t, publish(t, h))

	status, body, _ = get(t, subs.http, "/s/bob-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, []string{
		"usa2 relay socks",
		"usa2 relay socks [LAN]",
	}, clashProxyNames(t, body))

	// 若 alice 在 auto 上被暂停，即使 auto 在 stack_order 中，也不能包含 auto 的节点。
	_, err = h.svc.SetUserPause(ctx, h.actor, alice.ID, true, []string{"auto"})
	require.NoError(t, err)
	subs.deliver(t, publish(t, h))
	status, body, _ = get(t, subs.http, "/s/alice-token/clash")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, []string{
		"usa1 relay socks",
		"usa1 relay socks [LAN]",
		"usa1 vmess",
		"usa1 vmess [LAN]",
		"usa1 relay socks [DRT]",
		"usa2 relay socks",
		"usa2 relay socks [LAN]",
		"usa2 relay socks [DRT]",
		"Manual Node",
	}, clashProxyNames(t, body))
}

// TestStackOrderValidationAndLifecycle 验证 stack_order 的重名/未知栈校验及改名删除同步。
func TestStackOrderValidationAndLifecycle(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)

	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	doc.Settings.Subscription.StackOrder = []string{"missing"}
	_, err = h.svc.UpdateSettings(ctx, h.actor, *doc.Settings)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stack_order[0]")

	doc.Settings.Subscription.StackOrder = []string{"usa1", "usa1"}
	_, err = h.svc.UpdateSettings(ctx, h.actor, *doc.Settings)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate stack")

	doc.Settings.Subscription.StackOrder = []string{"usa2", "usa1"}
	_, err = h.svc.UpdateSettings(ctx, h.actor, *doc.Settings)
	require.NoError(t, err)

	// 改名 usa2 -> jp1 后，stack_order 随之同步。
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	usa2 := doc.Stacks[doc.StackByName("usa2")]
	usa2.Name = "jp1"
	_, err = h.svc.UpdateStack(ctx, h.actor, usa2.ID, usa2)
	require.NoError(t, err)
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"jp1", "usa1"}, doc.Settings.Subscription.StackOrder)

	// 先从 auto 中移除 jp1 引用后再删除 jp1，stack_order 中应自动移除 jp1。
	auto := doc.Stacks[doc.StackByName("auto")]
	configdoc.SetMembers(&auto, []string{"usa1"})
	_, err = h.svc.UpdateStack(ctx, h.actor, auto.ID, auto)
	require.NoError(t, err)
	require.NoError(t, h.svc.DeleteStack(ctx, h.actor, usa2.ID))
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"usa1"}, doc.Settings.Subscription.StackOrder)
}
