package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/eagle/proxystack-web/internal/events"
	"github.com/eagle/proxystack-web/internal/store/dbq"
)

// 作业状态。
const (
	TaskRunning   = "running"
	TaskSucceeded = "succeeded"
	TaskFailed    = "failed"
)

// Task 是一个后台作业的状态。
type Task struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Actor      string    `json:"actor"`
	Status     string    `json:"status" enum:"running,succeeded,failed"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Log        []string  `json:"log"`
	Error      string    `json:"error"`
}

// TaskEvent 是作业进度事件：新的一行日志或状态变化。
type TaskEvent struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Line   string `json:"line,omitempty"`
	Error  string `json:"error,omitempty"`
}

type taskRegistry struct {
	mu      sync.Mutex
	running map[int64]*Task
}

// Logf 是作业内写日志的函数。
type Logf func(format string, args ...any)

// startTask 创建作业记录并在后台执行 fn；执行期间的日志和结果通过事件总线推送，结束后写回数据库。
func (s *Service) startTask(kind, title, actor string, fn func(ctx context.Context, logf Logf) error) (int64, error) {
	ctx := context.Background()
	var id int64
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		id, err = q.InsertTask(ctx, dbq.InsertTaskParams{Kind: kind, Title: title, Actor: actor, StartedAt: s.Now().Unix()})
		return err
	})
	if err != nil {
		return 0, err
	}
	task := &Task{ID: id, Kind: kind, Title: title, Actor: actor, Status: TaskRunning, StartedAt: s.Now(), Log: []string{}}
	s.tasks.mu.Lock()
	if s.tasks.running == nil {
		s.tasks.running = map[int64]*Task{}
	}
	s.tasks.running[id] = task
	s.tasks.mu.Unlock()
	s.publish("task", TaskEvent{ID: id, Status: TaskRunning})

	go func() {
		logf := func(format string, args ...any) {
			line := s.Now().Format("15:04:05") + " " + fmt.Sprintf(format, args...)
			s.tasks.mu.Lock()
			task.Log = append(task.Log, line)
			s.tasks.mu.Unlock()
			s.publish("task", TaskEvent{ID: id, Status: TaskRunning, Line: line})
		}
		runErr := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("internal error: %v", r)
				}
			}()
			return fn(ctx, logf)
		}()
		s.tasks.mu.Lock()
		task.FinishedAt = s.Now()
		task.Status = TaskSucceeded
		if runErr != nil {
			task.Status, task.Error = TaskFailed, runErr.Error()
		}
		logText := strings.Join(task.Log, "\n")
		delete(s.tasks.running, id)
		s.tasks.mu.Unlock()
		if err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
			return q.FinishTask(ctx, dbq.FinishTaskParams{
				Status: task.Status, FinishedAt: task.FinishedAt.Unix(), Log: logText, Error: task.Error, ID: id,
			})
		}); err != nil {
			slog.Error("save task result", "task", id, "err", err)
		}
		s.publish("task", TaskEvent{ID: id, Status: task.Status, Error: task.Error})
	}()
	return id, nil
}

// GetTask 返回作业状态，运行中的作业取内存中的最新日志。
func (s *Service) GetTask(ctx context.Context, id int64) (*Task, error) {
	s.tasks.mu.Lock()
	if task, ok := s.tasks.running[id]; ok {
		copy := *task
		copy.Log = append([]string(nil), task.Log...)
		s.tasks.mu.Unlock()
		return &copy, nil
	}
	s.tasks.mu.Unlock()
	var task *Task
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		row, err := q.GetTask(ctx, id)
		if err != nil {
			return ErrNotFound
		}
		task = taskFromRow(row)
		return nil
	})
	return task, err
}

// ListTasks 列出最近的作业。
func (s *Service) ListTasks(ctx context.Context, limit int) ([]Task, error) {
	var tasks []Task
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListTasks(ctx, int64(limit))
		if err != nil {
			return err
		}
		for _, row := range rows {
			task := taskFromRow(row)
			task.Log = []string{}
			tasks = append(tasks, *task)
		}
		return nil
	})
	return tasks, err
}

func taskFromRow(row dbq.Task) *Task {
	task := &Task{
		ID: row.ID, Kind: row.Kind, Title: row.Title, Actor: row.Actor, Status: row.Status,
		StartedAt: time.Unix(row.StartedAt, 0), Error: row.Error, Log: []string{},
	}
	if row.FinishedAt > 0 {
		task.FinishedAt = time.Unix(row.FinishedAt, 0)
	}
	if row.Log != "" {
		task.Log = strings.Split(row.Log, "\n")
	}
	return task
}

func (s *Service) publish(kind string, data any) {
	if s.Events != nil {
		s.Events.Publish(events.Event{Type: kind, Data: data})
	}
}
