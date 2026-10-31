package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/eagle/proxystack-web/internal/app"
	"github.com/eagle/proxystack-web/internal/configdoc"
	"github.com/eagle/proxystack-web/internal/paths"
	"github.com/eagle/proxystack-web/internal/runtime"
	"github.com/eagle/proxystack-web/internal/store"
	"github.com/eagle/proxystack-web/internal/testutil"
	"github.com/eagle/proxystack-web/internal/units"
	"github.com/eagle/proxystack-web/internal/xrayapi"
)

type harness struct {
	svc   *app.Service
	units *units.Fake
	api   *xrayapi.Fake
	actor app.Actor
}

func newRuntimeHarness(t *testing.T) *harness {
	t.Helper()
	layout := paths.New(t.TempDir())
	for _, dir := range layout.Dirs() {
		require.NoError(t, os.MkdirAll(dir, 0o750))
	}
	st, err := store.Open(layout.DBPath())
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	svc := app.New(st, layout)
	_, err = svc.Initialize(context.Background(), app.InitOptions{TLSMode: store.TLSProxy, Port: 9443})
	require.NoError(t, err)
	fake := units.NewFake()
	api := &xrayapi.Fake{}
	svc.Units, svc.Logs, svc.XrayAPI, svc.PortChecker, svc.SettleDelay, svc.HealthWait = fake, fake, api, nil, time.Millisecond, time.Millisecond
	h := &harness{svc: svc, units: fake, api: api, actor: app.Actor{Name: "admin"}}

	data, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "tests", "fixtures", "example-doc.yaml"))
	require.NoError(t, err)
	preview, err := svc.ApplyImport(context.Background(), h.actor, app.ImportRequest{
		Filename: "example.yaml", Content: data,
		Options: configdoc.ImportOptions{Mode: configdoc.ImportMerge, Conflict: configdoc.ConflictSkip, Ports: configdoc.PortsKeep},
	})
	require.NoError(t, err)
	require.True(t, preview.Valid, preview.Issues)
	return h
}

func (h *harness) waitTask(t *testing.T, id int64) *app.Task {
	t.Helper()
	for i := 0; i < 200; i++ {
		task, err := h.svc.GetTask(context.Background(), id)
		require.NoError(t, err)
		if task.Status != app.TaskRunning {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("task did not finish")
	return nil
}

// TestApplyWritesFilesAndStartsUnitsInDependencyOrder 验证 apply 写入生成物、按依赖顺序启动，应用后没有待应用变更。
func TestApplyWritesFilesAndStartsUnitsInDependencyOrder(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)

	pending, err := h.svc.Pending(ctx)
	require.NoError(t, err)
	require.Len(t, pending.Changes, 6)

	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	task := h.waitTask(t, id)
	require.Equal(t, app.TaskSucceeded, task.Status, task.Error, task.Log)

	golden, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "tests", "golden", "xray", "usa1.json"))
	require.NoError(t, err)
	written, err := os.ReadFile(h.svc.Layout.GeneratedXray("usa1"))
	require.NoError(t, err)
	// 除了 email 按用户 ID 生成，生成物与参考项目逐字节一致。
	require.Equal(t, string(golden), strings.ReplaceAll(string(written), `"email": "u1"`, `"email": "alice"`))

	position := func(call string) int {
		for index, c := range h.units.Calls {
			if c == call {
				return index
			}
		}
		t.Fatalf("missing call %s in %v", call, h.units.Calls)
		return -1
	}
	// usa1 的 xray 依赖本 stack 的 mihomo；auto 的 mihomo 依赖 usa1 的 xray relay 入站。
	require.Less(t, position("start psw-mihomo@usa1.service"), position("start psw-xray@usa1.service"))
	require.Less(t, position("start psw-xray@usa1.service"), position("start psw-mihomo@auto.service"))

	pending, err = h.svc.Pending(ctx)
	require.NoError(t, err)
	require.Empty(t, pending.Changes)
	status, err := h.svc.RuntimeStatus(ctx)
	require.NoError(t, err)
	for _, stack := range status {
		require.False(t, stack.Drift)
		require.NotZero(t, stack.AppliedRevision)
	}
}

// TestPauseTakesEffectWithoutApply 验证暂停当场改写生成物并热移除用户，不把未应用的修改带上线。
func TestPauseTakesEffectWithoutApply(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)

	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	// 一处未应用的修改：改 usa1 vmess 入站的端口。
	usa1 := doc.Stacks[doc.StackByName("usa1")]
	usa1.Xray.Inbounds[1].Port = 24199
	_, err = h.svc.UpdateStack(ctx, h.actor, usa1.ID, usa1)
	require.NoError(t, err)

	alice := doc.Users[doc.UserByName("alice")]
	result, err := h.svc.SetUserPause(ctx, h.actor, alice.ID, true, []string{"usa1"})
	require.NoError(t, err)
	require.Empty(t, result.Notes)
	require.Contains(t, strings.Join(h.api.Calls, "\n"), "rmu 127.0.0.1:10091 vmess:24101:vmess u1")

	written, err := os.ReadFile(h.svc.Layout.GeneratedXray("usa1"))
	require.NoError(t, err)
	require.Contains(t, string(written), "psw-placeholder", "the only user of the vmess inbound is paused")
	require.NotContains(t, string(written), "24199", "the unapplied port change must not go live")

	pending, err := h.svc.Pending(ctx)
	require.NoError(t, err)
	require.Len(t, filterKind(pending.Changes, "xray", "usa1"), 1, "the port change is still pending")

	_, err = h.svc.SetUserPause(ctx, h.actor, alice.ID, true, nil)
	require.NoError(t, err)
	require.Contains(t, h.api.Calls[len(h.api.Calls)-1], "adu 127.0.0.1:10091")
	written, err = os.ReadFile(h.svc.Layout.GeneratedXray("usa1"))
	require.NoError(t, err)
	require.NotContains(t, string(written), "psw-placeholder")
}

func filterKind(changes []runtime.Change, component, stack string) []runtime.Change {
	out := []runtime.Change{}
	for _, change := range changes {
		if change.Component == component && change.Stack == stack {
			out = append(out, change)
		}
	}
	return out
}
