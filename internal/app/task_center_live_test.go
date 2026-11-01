package app

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/paths"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
)

// 运行中日志取内存快照，列表只保留末行，详情保留完整内容。
func TestTaskCenterLiveLogs(t *testing.T) {
	layout := paths.New(t.TempDir())
	for _, dir := range layout.Dirs() {
		require.NoError(t, os.MkdirAll(dir, 0o750))
	}
	st, err := store.Open(layout.DBPath())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	s := New(st, layout)
	ctx := context.Background()
	_, err = s.Initialize(ctx, InitOptions{Port: 9443})
	require.NoError(t, err)
	require.NoError(t, s.seedJobs(ctx))
	var taskID, runID int64
	require.NoError(t, st.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		taskID, err = q.InsertTask(ctx, dbq.InsertTaskParams{Kind: "apply", Title: "apply", Actor: "admin", StartedAt: 100})
		if err != nil {
			return err
		}
		jobs, err := q.ListJobs(ctx)
		if err != nil {
			return err
		}
		runID, err = q.InsertJobRun(ctx, dbq.InsertJobRunParams{JobID: jobs[0].ID, StartedAt: 100, Status: RunRunning, TriggerBy: "manual"})
		return err
	}))
	s.tasks.running = map[int64]*Task{taskID: {Log: []string{"task first", "task second"}}}
	s.scheduler.logs = map[int64]string{runID: "job first\njob second\n"}
	page, err := s.TaskCenter(ctx, "all", "running", 20, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	for _, item := range page.Items {
		require.Equal(t, []string{item.Source + " second"}, item.Log)
		detail, err := s.TaskCenterDetail(ctx, item.Source, item.ID)
		require.NoError(t, err)
		require.Equal(t, []string{item.Source + " first", item.Source + " second"}, detail.Log)
	}
}
