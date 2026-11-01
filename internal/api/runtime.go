package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/units"
)

type pendingOutput struct {
	Body app.PendingView
}

type applyInput struct {
	Body struct {
		Stacks []string `json:"stacks" required:"false" doc:"为空时应用全部 stack"`
	}
}

type taskIDOutput struct {
	Body struct {
		TaskID int64 `json:"task_id"`
	}
}

type runtimeStatusOutput struct {
	Body struct {
		Stacks []app.StackRuntime `json:"stacks"`
	}
}

type unitActionInput struct {
	Body struct {
		Stack     string `json:"stack"`
		Component string `json:"component" enum:"all,xray,mihomo"`
		Action    string `json:"action" enum:"start,stop,restart"`
	}
}

type reconcileOutput struct {
	Body struct {
		Findings []string `json:"findings"`
	}
}

type taskOutput struct {
	Body app.Task
}

type taskListOutput struct {
	Body struct {
		Tasks []app.Task `json:"tasks"`
	}
}

type taskCenterInput struct {
	Source string `query:"source" enum:"all,task,job" default:"all"`
	Status string `query:"status" enum:"all,running,succeeded,failed,skipped,timeout" default:"all"`
	Limit  int64  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int64  `query:"offset" minimum:"0" default:"0"`
}

type taskCenterOutput struct {
	Body app.TaskCenterPage
}

type taskCenterDetailInput struct {
	Source string `path:"source" enum:"task,job"`
	ID     int64  `path:"id" minimum:"1"`
}

type taskCenterDetailOutput struct {
	Body app.CenterTask
}

type pauseInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Enabled      bool     `json:"enabled"`
		PausedStacks []string `json:"paused_stacks"`
	}
}

type pauseOutput struct {
	Body struct {
		User  configdoc.User `json:"user"`
		Notes []string       `json:"notes"`
	}
}

type logsInput struct {
	Stack     string `query:"stack"`
	Component string `query:"component" enum:"all,xray,mihomo" default:"all"`
	Lines     int    `query:"lines" default:"200" minimum:"1" maximum:"5000"`
}

// RuntimeEvent 是 /api/events 中 runtime 事件的数据：全部 stack 的运行状态。
type RuntimeEvent struct {
	Stacks []app.StackRuntime `json:"stacks"`
}

func registerRuntime(api huma.API, s *Server) {
	reg := func(op huma.Operation) huma.Operation {
		op.Tags = []string{"runtime"}
		return secured(op)
	}

	huma.Register(api, reg(huma.Operation{OperationID: "getPending", Method: http.MethodGet, Path: "/api/runtime/pending", Summary: "待应用变更"}),
		func(ctx context.Context, _ *struct{}) (*pendingOutput, error) {
			view, err := s.svc.Pending(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &pendingOutput{Body: *view}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "applyConfig", Method: http.MethodPost, Path: "/api/runtime/apply", Summary: "应用配置（后台作业）", DefaultStatus: http.StatusAccepted}),
		func(ctx context.Context, in *applyInput) (*taskIDOutput, error) {
			id, err := s.svc.Apply(ctx, actorFrom(ctx), in.Body.Stacks)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &taskIDOutput{}
			out.Body.TaskID = id
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getRuntimeStatus", Method: http.MethodGet, Path: "/api/runtime/status", Summary: "stack 运行状态"}),
		func(ctx context.Context, _ *struct{}) (*runtimeStatusOutput, error) {
			status, err := s.svc.RuntimeStatus(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &runtimeStatusOutput{}
			out.Body.Stacks = status
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "unitAction", Method: http.MethodPost, Path: "/api/runtime/units", Summary: "启动、停止或重启 stack 的服务", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *unitActionInput) (*emptyOutput, error) {
			return nil, toHTTPError(s.svc.UnitAction(ctx, actorFrom(ctx), in.Body.Stack, in.Body.Component, in.Body.Action))
		})
	huma.Register(api, reg(huma.Operation{OperationID: "reconcile", Method: http.MethodPost, Path: "/api/runtime/reconcile", Summary: "按期望运行状态纠正 unit"}),
		func(ctx context.Context, _ *struct{}) (*reconcileOutput, error) {
			findings, err := s.svc.Reconcile(ctx, true)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &reconcileOutput{}
			out.Body.Findings = append([]string{}, findings...)
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listTasks", Method: http.MethodGet, Path: "/api/tasks", Summary: "最近的后台作业"}),
		func(ctx context.Context, _ *struct{}) (*taskListOutput, error) {
			tasks, err := s.svc.ListTasks(ctx, 50)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &taskListOutput{}
			out.Body.Tasks = append([]app.Task{}, tasks...)
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getTask", Method: http.MethodGet, Path: "/api/tasks/{id}", Summary: "后台作业详情"}),
		func(ctx context.Context, in *idPath) (*taskOutput, error) {
			task, err := s.svc.GetTask(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &taskOutput{Body: *task}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listTaskCenter", Method: http.MethodGet, Path: "/api/task-center", Summary: "统一后台作业与定时任务执行记录"}),
		func(ctx context.Context, in *taskCenterInput) (*taskCenterOutput, error) {
			page, err := s.svc.TaskCenter(ctx, in.Source, in.Status, in.Limit, in.Offset)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &taskCenterOutput{Body: *page}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getTaskCenterDetail", Method: http.MethodGet, Path: "/api/task-center/{source}/{id}", Summary: "任务状态和完整日志"}),
		func(ctx context.Context, in *taskCenterDetailInput) (*taskCenterDetailOutput, error) {
			task, err := s.svc.TaskCenterDetail(ctx, in.Source, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &taskCenterDetailOutput{Body: *task}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "setUserPause", Method: http.MethodPut, Path: "/api/users/{id}/pause", Summary: "暂停或恢复用户（当场生效）"}),
		func(ctx context.Context, in *pauseInput) (*pauseOutput, error) {
			result, err := s.svc.SetUserPause(ctx, actorFrom(ctx), in.ID, in.Body.Enabled, in.Body.PausedStacks)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &pauseOutput{}
			out.Body.User = *result.User
			configdoc.MaskUser(&out.Body.User)
			out.Body.Notes = append([]string{}, result.Notes...)
			return out, nil
		})

	sse.Register(api, reg(huma.Operation{OperationID: "events", Method: http.MethodGet, Path: "/api/events", Summary: "运行状态与作业进度事件流"}),
		map[string]any{"runtime": RuntimeEvent{}, "task": app.TaskEvent{}, "job": app.JobTaskEvent{}, "traffic": app.LivePoint{}},
		func(ctx context.Context, _ *struct{}, send sse.Sender) {
			events, cancel := s.svc.Events.Subscribe()
			defer cancel()
			if status, err := s.svc.RuntimeStatus(ctx); err == nil {
				_ = send.Data(RuntimeEvent{Stacks: status})
			}
			keepalive := time.NewTicker(25 * time.Second)
			defer keepalive.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-keepalive.C:
					if send.Comment("keepalive") != nil {
						return
					}
				case event, ok := <-events:
					if !ok {
						return
					}
					var err error
					switch data := event.Data.(type) {
					case []app.StackRuntime:
						err = send.Data(RuntimeEvent{Stacks: data})
					case app.TaskEvent:
						err = send.Data(data)
					case app.JobTaskEvent:
						err = send.Data(data)
					case app.LivePoint:
						err = send.Data(data)
					}
					if err != nil {
						return
					}
				}
			}
		})
	sse.Register(api, reg(huma.Operation{OperationID: "streamLogs", Method: http.MethodGet, Path: "/api/runtime/logs", Summary: "stack 日志（实时）"}),
		map[string]any{"log": units.LogEntry{}},
		func(ctx context.Context, in *logsInput, send sse.Sender) {
			names := []string{}
			for _, component := range []string{units.Xray, units.Mihomo} {
				if in.Component == "all" || in.Component == component {
					names = append(names, units.Name(component, in.Stack))
				}
			}
			_ = s.svc.Logs.Read(ctx, names, in.Lines, true, func(entry units.LogEntry) {
				_ = send.Data(entry)
			})
		})
}
