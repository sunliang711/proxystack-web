package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
)

// 面板地址可以带 nginx 子路径，但不能误填成面板某个页面的地址，否则通知邮件里的链接无法打开。
func TestPanelURLRejectsPagePath(t *testing.T) {
	h := newRuntimeHarness(t)
	ctx := context.Background()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)

	for _, bad := range []string{"http://10.2.78.11:9443/templates/", "https://panel.example.com/panel/stacks"} {
		settings := *doc.Settings
		settings.Basic.PanelURL = bad
		_, err := h.svc.UpdateSettings(ctx, h.actor, settings)
		var validation *app.ValidationError
		require.True(t, errors.As(err, &validation), "%s: %v", bad, err)
		issues := validation.Translate()
		require.Len(t, issues, 1)
		require.Equal(t, "basic.panel_url", issues[0].Field)
		require.Contains(t, issues[0].Message, "面板首页地址")
	}

	for _, good := range []string{"", "http://10.2.78.11:9443/", "https://panel.example.com/panel/"} {
		settings := *doc.Settings
		settings.Basic.PanelURL = good
		_, err := h.svc.UpdateSettings(ctx, h.actor, settings)
		require.NoError(t, err, good)
	}
}
