package api

import (
	"context"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/deps"
)

type componentPath struct {
	Component string `path:"component" enum:"xray,mihomo,geo"`
}

type depsOutput struct {
	Body struct {
		Components []app.DepStatus `json:"components"`
	}
}

type releasesInput struct {
	Component  string `path:"component" enum:"xray,mihomo,geo"`
	Prerelease bool   `query:"prerelease"`
}

type releasesOutput struct {
	Body struct {
		Releases []deps.Release `json:"releases"`
	}
}

type installInput struct {
	Component string `path:"component" enum:"xray,mihomo,geo"`
	Body      struct {
		Version string `json:"version" required:"false" doc:"为空时按依赖源设置安装"`
		Restart string `json:"restart" enum:"sequential,all,none"`
	}
}

type rollbackInput struct {
	Component string `path:"component" enum:"xray,mihomo,geo"`
	Body      struct {
		Restart string `json:"restart" enum:"sequential,all,none"`
	}
}

type uploadInput struct {
	Component string `path:"component" enum:"xray,mihomo,geo"`
	RawBody   huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true" doc:"release 归档或二进制"`
	}]
}

type proxyTestOutput struct {
	Body app.ProxyTestResult
}

type selfVersionOutput struct {
	Body app.SelfVersion
}

type selfUpgradeScheduleInput struct {
	Body app.SelfUpgradeRequest
}

type selfUpgradeScheduleOutput struct {
	Body app.SelfUpgradeSchedule
}

func registerDeps(api huma.API, s *Server) {
	reg := func(op huma.Operation) huma.Operation {
		op.Tags = []string{"deps"}
		return secured(op)
	}
	taskResult := func(id int64, err error) (*taskIDOutput, error) {
		if err != nil {
			return nil, toHTTPError(err)
		}
		out := &taskIDOutput{}
		out.Body.TaskID = id
		return out, nil
	}

	huma.Register(api, reg(huma.Operation{OperationID: "getDeps", Method: http.MethodGet, Path: "/api/deps", Summary: "xray、mihomo、geo 的安装状态"}),
		func(ctx context.Context, _ *struct{}) (*depsOutput, error) {
			status, err := s.svc.DepsStatus(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &depsOutput{}
			out.Body.Components = status
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "checkDepUpdates", Method: http.MethodPost, Path: "/api/deps/check", Summary: "检查各组件的最新版本", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, _ *struct{}) (*emptyOutput, error) {
			return nil, toHTTPError(s.svc.CheckDepUpdates(ctx))
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listDepReleases", Method: http.MethodGet, Path: "/api/deps/{component}/releases", Summary: "上游版本列表"}),
		func(ctx context.Context, in *releasesInput) (*releasesOutput, error) {
			releases, err := s.svc.DepReleases(ctx, in.Component, in.Prerelease)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &releasesOutput{}
			out.Body.Releases = append([]deps.Release{}, releases...)
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "installDep", Method: http.MethodPost, Path: "/api/deps/{component}/install", Summary: "安装或升级组件（后台作业）", DefaultStatus: http.StatusAccepted}),
		func(ctx context.Context, in *installInput) (*taskIDOutput, error) {
			return taskResult(s.svc.InstallDep(ctx, actorFrom(ctx), app.InstallRequest{
				Component: in.Component, Version: in.Body.Version, Restart: in.Body.Restart,
			}))
		})
	huma.Register(api, reg(huma.Operation{
		OperationID: "uploadDep", Method: http.MethodPost, Path: "/api/deps/{component}/upload",
		Summary: "上传安装包安装（后台作业）", DefaultStatus: http.StatusAccepted, MaxBodyBytes: deps.MaxDownload,
	}),
		func(ctx context.Context, in *uploadInput) (*taskIDOutput, error) {
			file := in.RawBody.Data().File
			data, err := io.ReadAll(io.LimitReader(file, deps.MaxDownload))
			if err != nil {
				return nil, toHTTPError(err)
			}
			value := func(key string) string {
				if values := in.RawBody.Form.Value[key]; len(values) > 0 {
					return values[0]
				}
				return ""
			}
			restart := value("restart")
			if restart == "" {
				restart = app.RestartSequential
			}
			return taskResult(s.svc.InstallDep(ctx, actorFrom(ctx), app.InstallRequest{
				Component: in.Component, Restart: restart,
				Upload: &app.Upload{Filename: file.Filename, Data: data, SHA256: value("sha256")},
			}))
		})
	huma.Register(api, reg(huma.Operation{OperationID: "rollbackDep", Method: http.MethodPost, Path: "/api/deps/{component}/rollback", Summary: "回滚到上一个版本（后台作业）", DefaultStatus: http.StatusAccepted}),
		func(ctx context.Context, in *rollbackInput) (*taskIDOutput, error) {
			return taskResult(s.svc.RollbackDep(ctx, actorFrom(ctx), in.Component, in.Body.Restart))
		})
	huma.Register(api, reg(huma.Operation{OperationID: "testDownloadProxy", Method: http.MethodPost, Path: "/api/config/download-proxy/test", Summary: "测试下载代理（无需先保存）"}),
		func(ctx context.Context, in *struct {
			Body configdoc.DownloadProxySettings
		}) (*proxyTestOutput, error) {
			result, err := s.svc.TestDownloadProxy(ctx, in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &proxyTestOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "checkSelfUpdate", Method: http.MethodGet, Path: "/api/system/update", Summary: "检查管理服务新版本", Tags: []string{"system"}}),
		func(ctx context.Context, _ *struct{}) (*selfVersionOutput, error) {
			result, err := s.svc.CheckSelfUpdate(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &selfVersionOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getSelfUpgradeSchedule", Method: http.MethodGet, Path: "/api/system/upgrade-schedule", Summary: "查看管理服务预约升级状态", Tags: []string{"system"}}),
		func(ctx context.Context, _ *struct{}) (*selfUpgradeScheduleOutput, error) {
			result, err := s.svc.SelfUpgradeSchedule(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &selfUpgradeScheduleOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "scheduleSelfUpgrade", Method: http.MethodPost, Path: "/api/system/upgrade-schedule", Summary: "预约管理服务升级", DefaultStatus: http.StatusAccepted, Tags: []string{"system"}}),
		func(ctx context.Context, in *selfUpgradeScheduleInput) (*selfUpgradeScheduleOutput, error) {
			result, err := s.svc.ScheduleSelfUpgrade(ctx, actorFrom(ctx), in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &selfUpgradeScheduleOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "cancelSelfUpgradeSchedule", Method: http.MethodDelete, Path: "/api/system/upgrade-schedule", Summary: "取消管理服务预约升级", Tags: []string{"system"}}),
		func(ctx context.Context, _ *struct{}) (*selfUpgradeScheduleOutput, error) {
			result, err := s.svc.CancelSelfUpgrade(ctx, actorFrom(ctx))
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &selfUpgradeScheduleOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "selfUpdate", Method: http.MethodPost, Path: "/api/system/update", Summary: "更新管理服务（后台作业）", DefaultStatus: http.StatusAccepted, Tags: []string{"system"}}),
		func(ctx context.Context, _ *struct{}) (*taskIDOutput, error) {
			return taskResult(s.svc.SelfUpdate(ctx, actorFrom(ctx)))
		})
}
