package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/store/dbq"
)

func TestTaskCenterSourcesAndPagination(t *testing.T) {
	h := newRuntimeHarness(t)
	ctx := context.Background()
	jobs, err := h.svc.ListJobs(ctx)
	require.NoError(t, err)
	var taskID, runID int64
	require.NoError(t, h.svc.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		taskID, err = q.InsertTask(ctx, dbq.InsertTaskParams{Kind: "apply", Title: "应用配置", Actor: "admin", StartedAt: 100})
		if err != nil {
			return err
		}
		runID, err = q.InsertJobRun(ctx, dbq.InsertJobRunParams{JobID: jobs[0].ID, StartedAt: 200, FinishedAt: 210, Status: "failed", TriggerBy: "manual", Log: "first line\nbackup failed\n"})
		return err
	}))
	page, err := h.svc.TaskCenter(ctx, "all", "all", "all", false, 1, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total)
	require.EqualValues(t, 1, page.Running)
	require.Len(t, page.Items, 1)
	require.Equal(t, "task", page.Items[0].Source)
	require.Equal(t, taskID, page.Items[0].ID)
	page, err = h.svc.TaskCenter(ctx, "all", "all", "all", false, 1, 1)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "job", page.Items[0].Source)
	require.Equal(t, "数据库备份", page.Items[0].Title)
	require.Equal(t, []string{"backup failed"}, page.Items[0].Log)
	require.Equal(t, "backup failed", page.Items[0].Error)
	page, err = h.svc.TaskCenter(ctx, "job", "failed", "all", false, 20, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.EqualValues(t, 1, page.Running)
	page, err = h.svc.TaskCenter(ctx, "all", "all", "apply", false, 20, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, "apply", page.Items[0].Kind)
	page, err = h.svc.TaskCenter(ctx, "all", "all", jobs[0].Kind, false, 20, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, "job", page.Items[0].Source)
	for _, source := range []string{"job", "task"} {
		id := taskID
		if source == "job" {
			id = runID
		}
		detail, err := h.svc.TaskCenterDetail(ctx, source, id)
		require.NoError(t, err)
		require.Equal(t, source, detail.Source)
		if source == "job" {
			require.Equal(t, []string{"first line", "backup failed"}, detail.Log)
		}
	}
	_, err = h.svc.TaskCenterDetail(ctx, "job", 99999)
	require.ErrorIs(t, err, app.ErrNotFound)
	_, err = h.svc.TaskCenterDetail(ctx, "invalid", taskID)
	require.ErrorIs(t, err, app.ErrNotFound)
	page, err = h.svc.TaskCenter(ctx, "all", "timeout", "all", false, 20, 0)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.NotNil(t, page.Items)
}

// 隐藏例行记录时只去掉按计划触发且成功或跳过的定时任务，失败、手动执行和后台作业照常返回。
func TestTaskCenterHidesScheduledSuccess(t *testing.T) {
	h := newRuntimeHarness(t)
	ctx := context.Background()
	jobs, err := h.svc.ListJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, h.svc.Store.InTx(ctx, func(q *dbq.Queries) error {
		if _, err := q.InsertTask(ctx, dbq.InsertTaskParams{Kind: "apply", Title: "应用配置", Actor: "admin", StartedAt: 100}); err != nil {
			return err
		}
		runs := []dbq.InsertJobRunParams{
			{JobID: jobs[0].ID, StartedAt: 200, FinishedAt: 201, Status: "succeeded", TriggerBy: "schedule"},
			{JobID: jobs[0].ID, StartedAt: 300, FinishedAt: 301, Status: "skipped", TriggerBy: "schedule"},
			{JobID: jobs[0].ID, StartedAt: 400, FinishedAt: 401, Status: "failed", TriggerBy: "schedule"},
			{JobID: jobs[0].ID, StartedAt: 500, FinishedAt: 501, Status: "succeeded", TriggerBy: "manual"},
		}
		for _, run := range runs {
			if _, err := q.InsertJobRun(ctx, run); err != nil {
				return err
			}
		}
		return nil
	}))
	page, err := h.svc.TaskCenter(ctx, "all", "all", "all", false, 20, 0)
	require.NoError(t, err)
	require.EqualValues(t, 5, page.Total)
	page, err = h.svc.TaskCenter(ctx, "all", "all", "all", true, 20, 0)
	require.NoError(t, err)
	require.EqualValues(t, 3, page.Total)
	require.Len(t, page.Items, 3)
	for _, item := range page.Items {
		if item.Source == "job" {
			require.True(t, item.Status == "failed" || item.Actor == "manual", "%+v", item)
		}
	}
}

// 读取任务计划不能把尚在执行的任务误标记为服务重启中断。
func TestListJobsPreservesRunningExecutions(t *testing.T) {
	h := newRuntimeHarness(t)
	ctx := context.Background()
	jobs, err := h.svc.ListJobs(ctx)
	require.NoError(t, err)
	var id int64
	require.NoError(t, h.svc.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		id, err = q.InsertJobRun(ctx, dbq.InsertJobRunParams{JobID: jobs[0].ID, StartedAt: 100, Status: "running", TriggerBy: "manual"})
		return err
	}))
	_, err = h.svc.ListJobs(ctx)
	require.NoError(t, err)
	item, err := h.svc.TaskCenterDetail(ctx, "job", id)
	require.NoError(t, err)
	require.Equal(t, "running", item.Status)
	require.True(t, item.FinishedAt.IsZero())
}
