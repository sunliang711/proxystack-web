package app_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/subpublish"
)

// TestUserStackMembership 验证从用户一侧加入和移出 stack：加入写入全部进入订阅的入站并保留已有引用，
// 移出清除全部入站引用和该 stack 上的暂停；没有进入订阅的入站时拒绝加入。
func TestUserStackMembership(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	bob, err := h.svc.CreateUser(ctx, h.actor, configdoc.User{Name: "bob", Enabled: true, ResetMode: "none"})
	require.NoError(t, err)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	usa1 := doc.Stacks[doc.StackByName("usa1")]
	usa1.Xray.Inbounds[0].Users = append(usa1.Xray.Inbounds[0].Users, configdoc.UserRef{User: "bob", Profile: "", Remark: "kept"})
	usa1.Xray.Inbounds[1].Sub = false
	_, err = h.svc.UpdateStack(ctx, h.actor, usa1.ID, usa1)
	require.NoError(t, err)

	_, err = h.svc.SetUserStackMember(ctx, h.actor, bob.ID, usa1.ID, true)
	require.NoError(t, err)
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	usa1 = doc.Stacks[doc.StackByName("usa1")]
	refs := func(inbound configdoc.Inbound) []configdoc.UserRef {
		var out []configdoc.UserRef
		for _, ref := range inbound.Users {
			if ref.User == "bob" {
				out = append(out, ref)
			}
		}
		return out
	}
	require.Equal(t, []configdoc.UserRef{{User: "bob", Remark: "kept"}}, refs(usa1.Xray.Inbounds[0]), "existing reference is kept as is")
	require.Empty(t, refs(usa1.Xray.Inbounds[1]), "inbounds outside the subscription are left alone")

	_, err = h.svc.SetUserPause(ctx, h.actor, bob.ID, true, []string{"usa1"})
	require.NoError(t, err)
	user, err := h.svc.SetUserStackMember(ctx, h.actor, bob.ID, usa1.ID, false)
	require.NoError(t, err)
	require.Equal(t, []string{"usa1"}, user.PausedStacks, "leaving keeps the pause until the removal is applied")
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	for _, inbound := range doc.Stacks[doc.StackByName("usa1")].Xray.Inbounds {
		require.Empty(t, refs(inbound))
	}
	user, err = h.svc.SetUserStackMember(ctx, h.actor, bob.ID, usa1.ID, true)
	require.NoError(t, err)
	require.Empty(t, user.PausedStacks, "joining a stack the user was not in enables the user there")
	_, err = h.svc.SetUserPause(ctx, h.actor, bob.ID, true, []string{"usa1"})
	require.NoError(t, err)
	user, err = h.svc.SetUserStackMember(ctx, h.actor, bob.ID, usa1.ID, true)
	require.NoError(t, err)
	require.Equal(t, []string{"usa1"}, user.PausedStacks, "joining again keeps an existing member paused")
	_, err = h.svc.SetUserStackMember(ctx, h.actor, bob.ID, usa1.ID, false)
	require.NoError(t, err)
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	usa1 = doc.Stacks[doc.StackByName("usa1")]

	for index := range usa1.Xray.Inbounds {
		usa1.Xray.Inbounds[index].Sub = false
	}
	_, err = h.svc.UpdateStack(ctx, h.actor, usa1.ID, usa1)
	require.NoError(t, err)
	_, err = h.svc.SetUserStackMember(ctx, h.actor, bob.ID, usa1.ID, true)
	var fieldErr *app.FieldError
	require.ErrorAs(t, err, &fieldErr)
	require.Contains(t, fieldErr.Message, "没有进入订阅的入站")
}

// TestLeavingPausedStackDoesNotResumeUser 验证移出一个已暂停且已应用的 stack 时，不会在应用前把用户当场加回代理端。
func TestLeavingPausedStackDoesNotResumeUser(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	bob, err := h.svc.CreateUser(ctx, h.actor, configdoc.User{Name: "bob", Enabled: true, ResetMode: "none"})
	require.NoError(t, err)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	usa1 := doc.Stacks[doc.StackByName("usa1")]
	_, err = h.svc.SetUserStackMember(ctx, h.actor, bob.ID, usa1.ID, true)
	require.NoError(t, err)
	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
	_, err = h.svc.SetUserPause(ctx, h.actor, bob.ID, true, []string{"usa1"})
	require.NoError(t, err)
	before := len(h.api.Calls)
	user, err := h.svc.SetUserStackMember(ctx, h.actor, bob.ID, usa1.ID, false)
	require.NoError(t, err)
	require.Equal(t, []string{"usa1"}, user.PausedStacks)
	require.Len(t, h.api.Calls, before, "no xray user is added back before the removal is applied")
}

// readyTarget 创建一个已部署且测试通过的订阅端。
func readyTarget(t *testing.T, h *harness, name, host string) int64 {
	t.Helper()
	ctx := context.Background()
	setup, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput(name, host))
	require.NoError(t, err)
	h.svc.SubDeployer = &deployClientFake{inspect: &subpublish.DeployStatus{
		OS: "linux", Arch: "amd64", Sudo: true, State: "installed", Version: "v0.2.0", ServiceActive: true, ServiceEnabled: true,
	}}
	_, err = h.svc.InspectPublishTargetDeploy(ctx, h.actor, setup.Target.ID)
	require.NoError(t, err)
	_, err = h.svc.TestPublishTarget(ctx, h.actor, setup.Target.ID)
	require.NoError(t, err)
	return setup.Target.ID
}

// TestAutoPublish 验证自动发布：内容变化或订阅端落后时发往全部就绪订阅端，内容不变时不发布，
// 失败后同样的内容在重试间隔内不重复发布，关闭后不再发布。
func TestAutoPublish(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	h.svc.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	fake := &publishClientFake{}
	h.svc.SubPublisher = fake

	taskID, err := h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.Zero(t, taskID, "no SSH identity and no targets")
	_, err = h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)
	_, err = h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("not-deployed", "sub-x.example.com"))
	require.NoError(t, err)
	taskID, err = h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.Zero(t, taskID, "targets that are not deployed and tested are skipped")

	first := readyTarget(t, h, "first", "sub-a.example.com")
	status, err := h.svc.SubscriptionBundleStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, status.AutoPublishTargets)
	taskID, err = h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.NotZero(t, taskID)
	task := h.waitTask(t, taskID)
	require.Equal(t, app.TaskSucceeded, task.Status, task.Error)
	require.Equal(t, app.AutoPublishActor.Name, task.Actor)
	taskID, err = h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.Zero(t, taskID, "nothing changed since the last publish")

	// 新就绪的订阅端落后于最近一次签发，内容不变也会发布，并且发往全部就绪订阅端。
	second := readyTarget(t, h, "second", "sub-b.example.com")
	taskID, err = h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.NotZero(t, taskID)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, taskID).Status)
	targets, err := h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	sequences := map[int64]int64{}
	for _, target := range targets {
		sequences[target.ID] = target.LastSequence
	}
	require.Equal(t, int64(2), sequences[first])
	require.Equal(t, int64(2), sequences[second])

	// 投递失败后，同样的内容在重试间隔内不再发布，间隔过后重试。
	fake.failHost = "sub-b.example.com"
	_, err = h.svc.CreateUser(ctx, h.actor, configdoc.User{Name: "bob", Enabled: true, ResetMode: "none"})
	require.NoError(t, err)
	taskID, err = h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.Equal(t, app.TaskFailed, h.waitTask(t, taskID).Status)
	taskID, err = h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.Zero(t, taskID, "retry waits for the retry delay")
	clock.Add(int64(11 * time.Minute))
	taskID, err = h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.Equal(t, app.TaskFailed, h.waitTask(t, taskID).Status)
	clock.Add(int64(11 * time.Minute))
	taskID, err = h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.Zero(t, taskID, "the retry delay doubles after each failure")
	clock.Add(int64(10 * time.Minute))
	fake.failHost = ""
	taskID, err = h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.NotZero(t, taskID)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, taskID).Status)

	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	doc.Settings.Subscription.ManualPublish = true
	_, err = h.svc.UpdateSettings(ctx, h.actor, *doc.Settings)
	require.NoError(t, err)
	_, err = h.svc.CreateUser(ctx, h.actor, configdoc.User{Name: "carol", Enabled: true, ResetMode: "none"})
	require.NoError(t, err)
	taskID, err = h.svc.AutoPublish(ctx)
	require.NoError(t, err)
	require.Zero(t, taskID, "manual publish disables auto publish")
	status, err = h.svc.SubscriptionBundleStatus(ctx)
	require.NoError(t, err)
	require.True(t, status.ManualPublish)
	require.Zero(t, status.AutoPublishTargets)
	require.True(t, status.Pending)
}
