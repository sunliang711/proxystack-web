package api

import (
	"context"
	"net/http"
	"runtime"

	"github.com/danielgtaylor/huma/v2"

	"github.com/eagle/proxystack-web/internal/version"
)

// SystemInfo 是「系统」页显示的版本与面板信息。
type SystemInfo struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildDate   string `json:"build_date"`
	GoVersion   string `json:"go_version"`
	Platform    string `json:"platform"`
	PanelURL    string `json:"panel_url"`
	TLSMode     string `json:"tls_mode"`
	Fingerprint string `json:"fingerprint,omitempty"`
	DevMode     bool   `json:"dev_mode"`
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
		return &systemInfoOutput{Body: SystemInfo{
			Version:     version.Version,
			Commit:      version.Commit,
			BuildDate:   version.Date,
			GoVersion:   runtime.Version(),
			Platform:    runtime.GOOS + "/" + runtime.GOARCH,
			PanelURL:    info.PanelURL,
			TLSMode:     info.Panel.TLSMode,
			Fingerprint: info.Fingerprint,
			DevMode:     s.opts.Dev,
		}}, nil
	})
}
