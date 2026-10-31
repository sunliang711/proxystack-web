package api

import (
	"context"
	"net/http"
	"runtime"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
	"proxystack-web/internal/version"
)

// SystemInfo 是「系统」页显示的版本与面板信息。
type SystemInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
	PanelURL  string `json:"panel_url"`
	Listen    string `json:"listen"`
	Port      int    `json:"port"`
	DevMode   bool   `json:"dev_mode"`
}

type panelListenInput struct {
	Body app.PanelListen
}

type panelListenOutput struct {
	Body struct {
		// Restarting 为假时需要手工重启管理服务才生效。
		Restarting bool `json:"restarting"`
	}
}

type systemInfoOutput struct {
	Body SystemInfo
}

func registerSystem(api huma.API, s *Server) {
	huma.Register(api, secured(huma.Operation{
		OperationID: "getSystemInfo",
		Method:      http.MethodGet,
		Path:        "/api/system/info",
		Summary:     "系统信息",
		Tags:        []string{"system"},
	}), func(ctx context.Context, _ *struct{}) (*systemInfoOutput, error) {
		info, err := s.svc.Info(ctx)
		if err != nil {
			return nil, toHTTPError(err)
		}
		build := version.Current()
		return &systemInfoOutput{Body: SystemInfo{
			Version:   build.Version,
			Commit:    build.Commit,
			BuildDate: build.Date,
			GoVersion: runtime.Version(),
			Platform:  runtime.GOOS + "/" + runtime.GOARCH,
			PanelURL:  info.PanelURL,
			Listen:    info.Panel.Listen,
			Port:      info.Panel.Port,
			DevMode:   s.opts.Dev,
		}}, nil
	})

	huma.Register(api, secured(huma.Operation{
		OperationID: "updatePanelListen",
		Method:      http.MethodPut,
		Path:        "/api/system/panel",
		Summary:     "修改面板监听地址和端口并重启管理服务",
		Tags:        []string{"system"},
	}), func(ctx context.Context, in *panelListenInput) (*panelListenOutput, error) {
		if err := s.svc.UpdatePanelListen(ctx, actorFrom(ctx), in.Body); err != nil {
			return nil, toHTTPError(err)
		}
		out := &panelListenOutput{}
		out.Body.Restarting = s.svc.RestartSoon()
		return out, nil
	})
}
