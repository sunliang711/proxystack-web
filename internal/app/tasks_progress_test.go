package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/events"
	"proxystack-web/internal/paths"
	"proxystack-web/internal/store"
)

// 作业上报的阶段进度通过事件推送，运行中可从详情读取快照，结束后清空。
func TestTaskProgress(t *testing.T) {
	layout := paths.New(t.TempDir())
	for _, dir := range layout.Dirs() {
		require.NoError(t, os.MkdirAll(dir, 0o750))
	}
	st, err := store.Open(layout.DBPath())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	s := New(st, layout)
	s.Events = events.New()
	stream, cancel := s.Events.Subscribe()
	t.Cleanup(cancel)
	ctx := context.Background()
	_, err = s.Initialize(ctx, InitOptions{Port: 9443})
	require.NoError(t, err)

	reported := make(chan struct{})
	release := make(chan struct{})
	id, err := s.startTask("self-update", "更新", "admin", func(ctx context.Context, logf Logf) error {
		reportTaskProgress(ctx, TaskProgress{Stage: "download", Done: 512, Total: 2048})
		close(reported)
		<-release
		return nil
	})
	require.NoError(t, err)
	<-reported

	running, err := s.GetTask(ctx, id)
	require.NoError(t, err)
	require.Equal(t, &TaskProgress{Stage: "download", Done: 512, Total: 2048}, running.Progress)

	var progress *TaskProgress
	deadline := time.After(5 * time.Second)
	for progress == nil {
		select {
		case event := <-stream:
			if task, ok := event.Data.(TaskEvent); ok && task.ID == id && task.Progress != nil {
				progress = task.Progress
			}
		case <-deadline:
			t.Fatal("no progress event")
		}
	}
	require.Equal(t, "download", progress.Stage)
	require.Equal(t, int64(2048), progress.Total)

	close(release)
	require.Eventually(t, func() bool {
		task, err := s.GetTask(ctx, id)
		return err == nil && task.Status == TaskSucceeded && task.Progress == nil
	}, 5*time.Second, 20*time.Millisecond)

	// 不在后台作业里调用时直接忽略。
	reportTaskProgress(ctx, TaskProgress{Stage: "download"})
}
