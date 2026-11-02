package configdoc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
)

// TestOverlaySubscriptionNamesOnlyCopiesNamingFields 保证当前命名规则不会带入未应用的连接参数或凭据。
func TestOverlaySubscriptionNamesOnlyCopiesNamingFields(t *testing.T) {
	applied := loadExampleDoc(t)
	current := applied.Clone()
	current.Settings.Subscription.DisplayTemplate = "{{ .stack }} {{ .user }}"
	current.Users[0].DisplayTemplate = "{{ .user }}"
	current.Users[0].UUID = "22222222-2222-4222-8222-222222222222"
	currentInbound := &current.Stacks[1].Xray.Inbounds[1]
	currentInbound.Remark = "current remark"
	currentInbound.DisplayTemplate = "{{ .inbound }}"
	currentInbound.Port = 29999
	currentInbound.Users[0].Remark = "current user remark"
	currentInbound.Users[0].DisplayTemplate = "{{ .remark }}"

	originalUUID := applied.Users[0].UUID
	originalPort := applied.Stacks[1].Xray.Inbounds[1].Port
	configdoc.OverlaySubscriptionNames(applied, current)

	require.Equal(t, "{{ .stack }} {{ .user }}", applied.Settings.Subscription.DisplayTemplate)
	require.Equal(t, "{{ .user }}", applied.Users[0].DisplayTemplate)
	require.Equal(t, originalUUID, applied.Users[0].UUID)
	appliedInbound := applied.Stacks[1].Xray.Inbounds[1]
	require.Equal(t, "current remark", appliedInbound.Remark)
	require.Equal(t, "{{ .inbound }}", appliedInbound.DisplayTemplate)
	require.Equal(t, originalPort, appliedInbound.Port)
	require.Equal(t, "current user remark", appliedInbound.Users[0].Remark)
	require.Equal(t, "{{ .remark }}", appliedInbound.Users[0].DisplayTemplate)
}
