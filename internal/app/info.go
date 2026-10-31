package app

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
)

// InstanceInfo 是 `info` 命令和系统页显示的实例信息。
type InstanceInfo struct {
	PanelURL string
	Panel    store.PanelSettings
}

// Info 返回面板地址。
func (s *Service) Info(ctx context.Context) (*InstanceInfo, error) {
	panel, err := store.GetPanelSettings(ctx, s.Store.Q)
	if err != nil {
		return nil, fmt.Errorf("read panel settings: %w", err)
	}
	basic, err := store.GetBasicSettings(ctx, s.Store.Q)
	if err != nil {
		return nil, err
	}
	return &InstanceInfo{Panel: panel, PanelURL: s.panelURL(panel, basic)}, nil
}

// panelURL 返回面板地址：设置了经 nginx 访问的地址时用它，否则是管理服务自己的监听地址。
func (s *Service) panelURL(panel store.PanelSettings, basic store.BasicSettings) string {
	if basic.PanelURL != "" {
		return strings.TrimSuffix(basic.PanelURL, "/") + "/" + panel.Path + "/"
	}
	host := panel.Listen
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = primaryIP()
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s/%s/", net.JoinHostPort(host, strconv.Itoa(panel.Port)), panel.Path)
}

// primaryIP 返回第一个非回环的 IPv4 地址。
func primaryIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return ""
}

// ActionPanelListen 是修改面板监听的审计动作。
const ActionPanelListen = "system.panel_listen"

// PanelListen 是面板的监听地址和端口。
type PanelListen struct {
	Listen string `json:"listen"`
	Port   int    `json:"port" minimum:"1" maximum:"65535"`
}

// UpdatePanelListen 修改面板的监听地址和端口，重启管理服务后生效。
func (s *Service) UpdatePanelListen(ctx context.Context, actor Actor, listen PanelListen) error {
	if net.ParseIP(listen.Listen) == nil {
		return &FieldError{Field: "listen", Message: "must be an IP address"}
	}
	if listen.Port < 1 || listen.Port > 65535 {
		return &FieldError{Field: "port", Message: "must be between 1 and 65535"}
	}
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	for index := range doc.Stacks {
		if configdoc.StackPorts(&doc.Stacks[index])[listen.Port] {
			return &FieldError{Field: "port", Message: "port is used by stack " + doc.Stacks[index].Name}
		}
	}
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		panel, err := store.GetPanelSettings(ctx, q)
		if err != nil {
			return err
		}
		panel.Listen, panel.Port = listen.Listen, listen.Port
		if err := store.PutJSON(ctx, q, store.SettingPanel, panel); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionPanelListen, Object: "panel",
			Summary: "面板监听改为 " + net.JoinHostPort(listen.Listen, strconv.Itoa(listen.Port)),
		})
	})
}

// panelRestartDelay 留出时间让修改监听的请求先返回。
const panelRestartDelay = 500 * time.Millisecond

// RestartSoon 稍后重启管理服务，返回能否重启（开发模式和命令行下不能）。
func (s *Service) RestartSoon() bool {
	if s.RequestRestart == nil {
		return false
	}
	time.AfterFunc(panelRestartDelay, s.RequestRestart)
	return true
}
