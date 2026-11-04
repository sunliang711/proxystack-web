package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"proxystack-web/internal/store/dbq"
)

// CenterTask 统一后台作业和定时任务执行记录；来源与 ID 共同标识一条记录。
type CenterTask struct {
	Source     string    `json:"source" enum:"task,job"`
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Actor      string    `json:"actor"`
	Status     string    `json:"status" enum:"running,succeeded,failed,skipped,timeout"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Log        []string  `json:"log"`
	Error      string    `json:"error"`
	JobID      int64     `json:"job_id"`
}

// TaskCenterPage 包含筛选后的分页结果及全部来源的运行中数量。
type TaskCenterPage struct {
	Items   []CenterTask `json:"items"`
	Total   int64        `json:"total"`
	Running int64        `json:"running"`
}

// JobTaskEvent 通知定时任务执行状态或日志已更新，客户端重新读取快照防止重复日志。
type JobTaskEvent struct {
	ID     int64  `json:"id"`
	Status string `json:"status" enum:"running,succeeded,failed,skipped,timeout"`
}

func centerTask(row dbq.ListTaskCenterRow) CenterTask {
	item := CenterTask{Source: row.Source, ID: row.ID, Kind: row.Kind, Title: row.Title, Actor: row.Actor, Status: row.Status,
		StartedAt: time.Unix(row.StartedAt, 0), Error: row.Error, JobID: row.JobID, Log: []string{}}
	if row.FinishedAt > 0 {
		item.FinishedAt = time.Unix(row.FinishedAt, 0)
	}
	if row.Log != "" {
		item.Log = strings.Split(strings.TrimSuffix(row.Log, "\n"), "\n")
	}
	if row.Source == "job" {
		if kind := jobTypeOf(row.Kind); kind != nil {
			item.Title = kind.Name
		}
		if (row.Status == RunFailed || row.Status == RunTimeout) && len(item.Log) > 0 {
			item.Error = item.Log[len(item.Log)-1]
		}
	}
	return item
}

func (s *Service) liveCenterTask(item *CenterTask) {
	if item.Status != TaskRunning {
		return
	}
	if item.Source == "task" {
		s.tasks.mu.Lock()
		if running := s.tasks.running[item.ID]; running != nil {
			item.Log = append([]string{}, running.Log...)
		}
		s.tasks.mu.Unlock()
	} else {
		s.scheduler.mu.Lock()
		if text := s.scheduler.logs[item.ID]; text != "" {
			item.Log = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		}
		s.scheduler.mu.Unlock()
	}
}

// TaskCenter 查询历史记录，运行中的任务优先显示，筛选和分页在数据库中完成。
// hideScheduledOK 为真时不返回按计划触发且成功或跳过的定时任务记录，失败和手动执行的记录照常返回。
func (s *Service) TaskCenter(ctx context.Context, source, status, kind string, hideScheduledOK bool, limit, offset int64) (*TaskCenterPage, error) {
	out := &TaskCenterPage{Items: []CenterTask{}}
	var quiet int64
	if hideScheduledOK {
		quiet = 1
	}
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListTaskCenter(ctx, dbq.ListTaskCenterParams{Source: source, Status: status, Kind: kind, Quiet: quiet, PageSize: limit, PageOffset: offset})
		if err != nil {
			return err
		}
		for _, row := range rows {
			out.Items = append(out.Items, centerTask(row))
		}
		out.Total, err = q.CountTaskCenter(ctx, dbq.CountTaskCenterParams{Source: source, Status: status, Kind: kind, Quiet: quiet})
		if err != nil {
			return err
		}
		out.Running, err = q.CountTaskCenter(ctx, dbq.CountTaskCenterParams{Source: "all", Status: TaskRunning, Kind: "all"})
		return err
	})
	if err != nil {
		return nil, err
	}
	for index := range out.Items {
		s.liveCenterTask(&out.Items[index])
		// 列表只携带最新一行，完整日志按需从详情接口读取。
		if log := out.Items[index].Log; len(log) > 1 {
			out.Items[index].Log = log[len(log)-1:]
		}
	}
	return out, nil
}

// TaskCenterDetail 读取单条任务的完整日志，来源避免两张表的相同 ID 冲突。
func (s *Service) TaskCenterDetail(ctx context.Context, source string, id int64) (*CenterTask, error) {
	var row dbq.ListTaskCenterRow
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if source == "task" {
			task, err := q.GetTask(ctx, id)
			row = dbq.ListTaskCenterRow{Source: source, ID: task.ID, Kind: task.Kind, Title: task.Title, Actor: task.Actor,
				Status: task.Status, StartedAt: task.StartedAt, FinishedAt: task.FinishedAt, Log: task.Log, Error: task.Error}
			return err
		}
		if source != "job" {
			return ErrNotFound
		}
		run, err := q.GetTaskCenterJob(ctx, id)
		row = dbq.ListTaskCenterRow{Source: source, ID: run.ID, Kind: run.Kind, Title: run.Kind, Actor: run.TriggerBy,
			Status: run.Status, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Log: run.Log, JobID: run.JobID}
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	item := centerTask(row)
	s.liveCenterTask(&item)
	return &item, nil
}
