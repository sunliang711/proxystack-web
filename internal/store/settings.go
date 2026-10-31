package store

import (
	"context"

	"proxystack-web/internal/store/dbq"
)

// 设置分组的 key。
const (
	SettingPanel = "panel"
	SettingBasic = "basic"
)

// PanelSettings 是管理面板的监听与访问设置。
type PanelSettings struct {
	Listen string `json:"listen"`
	Port   int    `json:"port"`
}

// BasicSettings 是基本设置。
type BasicSettings struct {
	ExternalHost string `json:"external_host"`
	Timezone     string `json:"timezone"`
	PanelURL     string `json:"panel_url,omitempty"`
}

// DefaultBasicSettings 返回基本设置的默认值。
func DefaultBasicSettings() BasicSettings {
	return BasicSettings{Timezone: "Asia/Shanghai"}
}

// GetPanelSettings 读取面板设置。
func GetPanelSettings(ctx context.Context, q *dbq.Queries) (PanelSettings, error) {
	var p PanelSettings
	err := GetJSON(ctx, q, SettingPanel, &p)
	return p, err
}

// GetBasicSettings 读取基本设置，不存在时返回默认值。
func GetBasicSettings(ctx context.Context, q *dbq.Queries) (BasicSettings, error) {
	b := DefaultBasicSettings()
	err := GetJSON(ctx, q, SettingBasic, &b)
	if err == ErrNotFound {
		return DefaultBasicSettings(), nil
	}
	return b, err
}
