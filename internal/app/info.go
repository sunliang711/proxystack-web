package app

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/eagle/proxystack-web/internal/store"
	"github.com/eagle/proxystack-web/internal/tlsutil"
)

// InstanceInfo 是 `info` 命令和系统页显示的实例信息。
type InstanceInfo struct {
	PanelURL    string
	Fingerprint string
	Panel       store.PanelSettings
}

// Info 返回面板地址和证书指纹。
func (s *Service) Info(ctx context.Context) (*InstanceInfo, error) {
	panel, err := store.GetPanelSettings(ctx, s.Store.Q)
	if err != nil {
		return nil, fmt.Errorf("read panel settings: %w", err)
	}
	basic, err := store.GetBasicSettings(ctx, s.Store.Q)
	if err != nil {
		return nil, err
	}
	info := &InstanceInfo{Panel: panel, PanelURL: s.panelURL(panel, basic)}
	if panel.TLSMode == store.TLSSelfSigned {
		if fp, err := tlsutil.Fingerprint(s.Layout.TLSCert()); err == nil {
			info.Fingerprint = fp
		}
	}
	return info, nil
}

func (s *Service) panelURL(panel store.PanelSettings, basic store.BasicSettings) string {
	scheme := "https"
	if panel.TLSMode == store.TLSProxy {
		scheme = "http"
	}
	host := basic.ExternalHost
	if host == "" {
		host = panel.Listen
		if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			host = tlsutil.PrimaryIP()
		}
		if host == "" {
			host = "127.0.0.1"
		}
	}
	return fmt.Sprintf("%s://%s/%s/", scheme, net.JoinHostPort(host, strconv.Itoa(panel.Port)), panel.Path)
}
