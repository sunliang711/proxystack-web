package api

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/eagle/proxystack-web/internal/app"
	"github.com/eagle/proxystack-web/internal/mihomoapi"
)

// maxRestoreBytes 是迁移包上传的大小上限。
const maxRestoreBytes = 32 << 30

type jobsOutput struct {
	Body []app.JobView
}

type jobPath struct {
	ID int64 `path:"id"`
}

type jobUpdateInput struct {
	ID   int64 `path:"id"`
	Body app.JobUpdate
}

type jobRunsOutput struct {
	Body []app.JobRun
}

type schedulePreviewInput struct {
	Body struct {
		Schedule string `json:"schedule"`
	}
}

type schedulePreviewOutput struct {
	Body []time.Time
}

type backupFilesOutput struct {
	Body []app.BackupFile
}

type backupNamePath struct {
	Name string `path:"name"`
}

type backupExportInput struct {
	Body app.ExportRequest
}

type restoreUploadInput struct {
	RawBody multipart.Form
}

type restorePreviewOutput struct {
	Body app.RestorePreview
}

type restoreExecuteInput struct {
	Body app.RestoreOptions
}

type restoreStatusOutput struct {
	Body app.RestoreStatus
}

type healthOutput struct {
	Body []app.HealthCheck
}

type egressInput struct {
	ID      int64  `path:"id"`
	Family  string `query:"family" enum:"all,ipv4,ipv6" default:"all"`
	Timeout int    `query:"timeout" minimum:"1" maximum:"60" default:"8" doc:"单个数据源的超时（秒）"`
}

type egressOutput struct {
	Body []app.EgressResult
}

type proxiesOutput struct {
	Body []mihomoapi.Proxy
}

type delayInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Names   []string `json:"names" doc:"为空时测试全部"`
		URL     string   `json:"url" required:"false" doc:"为空时使用默认测速地址"`
		Timeout int      `json:"timeout" required:"false" minimum:"0" maximum:"60" doc:"秒"`
	}
}

type delayOutput struct {
	Body []app.DelayResult
}

type auditInput struct {
	Action string `query:"action" doc:"动作前缀，例如 user 或 config.user"`
	Actor  string `query:"actor"`
	Limit  int64  `query:"limit" minimum:"1" maximum:"500" default:"50"`
	Offset int64  `query:"offset" minimum:"0"`
}

type auditOutput struct {
	Body app.AuditPage
}

func registerOps(api huma.API, s *Server) {
	reg := func(op huma.Operation) huma.Operation {
		op.Tags = []string{"ops"}
		return secured(op)
	}

	huma.Register(api, reg(huma.Operation{OperationID: "listJobs", Method: http.MethodGet, Path: "/api/jobs", Summary: "定时任务"}),
		func(ctx context.Context, _ *struct{}) (*jobsOutput, error) {
			jobs, err := s.svc.ListJobs(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &jobsOutput{Body: jobs}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "previewSchedule", Method: http.MethodPost, Path: "/api/jobs/preview", Summary: "校验计划并返回未来 5 次执行时间"}),
		func(ctx context.Context, in *schedulePreviewInput) (*schedulePreviewOutput, error) {
			times, err := s.svc.JobSchedulePreview(ctx, in.Body.Schedule, 5)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &schedulePreviewOutput{Body: times}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "updateJob", Method: http.MethodPut, Path: "/api/jobs/{id}", Summary: "修改定时任务", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *jobUpdateInput) (*struct{}, error) {
			if err := s.svc.UpdateJob(ctx, actorFrom(ctx), in.ID, in.Body); err != nil {
				return nil, toHTTPError(err)
			}
			return nil, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "runJob", Method: http.MethodPost, Path: "/api/jobs/{id}/run", Summary: "立即执行定时任务", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *jobPath) (*struct{}, error) {
			if err := s.svc.RunJobNow(ctx, actorFrom(ctx), in.ID); err != nil {
				return nil, toHTTPError(err)
			}
			return nil, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listJobRuns", Method: http.MethodGet, Path: "/api/jobs/{id}/runs", Summary: "定时任务的执行记录"}),
		func(ctx context.Context, in *jobPath) (*jobRunsOutput, error) {
			runs, err := s.svc.ListJobRuns(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &jobRunsOutput{Body: runs}, nil
		})

	huma.Register(api, reg(huma.Operation{OperationID: "listBackups", Method: http.MethodGet, Path: "/api/backup/files", Summary: "备份文件"}),
		func(ctx context.Context, _ *struct{}) (*backupFilesOutput, error) {
			files, err := s.svc.ListBackups()
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &backupFilesOutput{Body: files}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "downloadBackup", Method: http.MethodGet, Path: "/api/backup/files/{name}/download", Summary: "下载备份文件（含全部凭据，记入审计日志）"}),
		func(ctx context.Context, in *backupNamePath) (*huma.StreamResponse, error) {
			file, size, err := s.svc.OpenBackup(ctx, actorFrom(ctx), in.Name)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &huma.StreamResponse{Body: func(stream huma.Context) {
				defer file.Close()
				stream.SetHeader("Content-Type", "application/octet-stream")
				stream.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, in.Name))
				stream.SetHeader("Content-Length", strconv.FormatInt(size, 10))
				_, _ = io.Copy(stream.BodyWriter(), file)
			}}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "deleteBackup", Method: http.MethodDelete, Path: "/api/backup/files/{name}", Summary: "删除备份文件", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *backupNamePath) (*struct{}, error) {
			if err := s.svc.DeleteBackup(ctx, actorFrom(ctx), in.Name); err != nil {
				return nil, toHTTPError(err)
			}
			return nil, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "exportPackage", Method: http.MethodPost, Path: "/api/backup/export", Summary: "在后台生成迁移包"}),
		func(ctx context.Context, in *backupExportInput) (*taskIDOutput, error) {
			id, err := s.svc.ExportPackage(ctx, actorFrom(ctx), in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &taskIDOutput{}
			out.Body.TaskID = id
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{
		OperationID: "uploadRestore", Method: http.MethodPost, Path: "/api/backup/restore/upload", Summary: "上传迁移包并预览",
		MaxBodyBytes: maxRestoreBytes, BodyReadTimeout: -1,
	}),
		func(ctx context.Context, in *restoreUploadInput) (*restorePreviewOutput, error) {
			files := in.RawBody.File["file"]
			if len(files) == 0 {
				return nil, huma.Error422UnprocessableEntity("a migration package is required")
			}
			file, err := files[0].Open()
			if err != nil {
				return nil, toHTTPError(err)
			}
			defer file.Close()
			passphrase := strings.Join(in.RawBody.Value["passphrase"], "")
			preview, err := s.svc.StageRestore(ctx, file, passphrase)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &restorePreviewOutput{Body: *preview}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "executeRestore", Method: http.MethodPost, Path: "/api/backup/restore/execute", Summary: "执行恢复：替换数据库并重启管理服务", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *restoreExecuteInput) (*struct{}, error) {
			if err := s.svc.ExecuteRestore(ctx, actorFrom(ctx), in.Body); err != nil {
				return nil, toHTTPError(err)
			}
			return nil, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getRestoreStatus", Method: http.MethodGet, Path: "/api/backup/restore/status", Summary: "恢复后的收尾状态"}),
		func(ctx context.Context, _ *struct{}) (*restoreStatusOutput, error) {
			status, err := s.svc.RestoreStatusData(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &restoreStatusOutput{Body: *status}, nil
		})

	huma.Register(api, reg(huma.Operation{OperationID: "getHealthChecks", Method: http.MethodGet, Path: "/api/health-checks", Summary: "健康检查"}),
		func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
			return &healthOutput{Body: s.svc.HealthChecks(ctx)}, nil
		})

	huma.Register(api, reg(huma.Operation{OperationID: "getStackEgress", Method: http.MethodGet, Path: "/api/stacks/{id}/egress", Summary: "经 stack 的 mihomo 查询出口 IP"}),
		func(ctx context.Context, in *egressInput) (*egressOutput, error) {
			results, err := s.svc.StackEgress(ctx, in.ID, in.Family, time.Duration(in.Timeout)*time.Second)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &egressOutput{Body: results}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listStackProxies", Method: http.MethodGet, Path: "/api/stacks/{id}/proxies", Summary: "stack 中可以测速的代理"}),
		func(ctx context.Context, in *idPath) (*proxiesOutput, error) {
			proxies, err := s.svc.StackProxies(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &proxiesOutput{Body: proxies}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "testStackDelay", Method: http.MethodPost, Path: "/api/stacks/{id}/delay", Summary: "测试 stack 中代理的延迟"}),
		func(ctx context.Context, in *delayInput) (*delayOutput, error) {
			results, err := s.svc.StackDelay(ctx, in.ID, in.Body.Names, in.Body.URL, time.Duration(in.Body.Timeout)*time.Second)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &delayOutput{Body: results}, nil
		})

	huma.Register(api, reg(huma.Operation{OperationID: "listAudit", Method: http.MethodGet, Path: "/api/audit", Summary: "审计日志"}),
		func(ctx context.Context, in *auditInput) (*auditOutput, error) {
			page, err := s.svc.ListAudit(ctx, in.Action, in.Actor, in.Limit, in.Offset)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &auditOutput{Body: *page}, nil
		})
}
