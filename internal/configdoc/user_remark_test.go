package configdoc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
	subgen "proxystack-web/internal/generator/sub"
)

// TestUserRemarkDoesNotRenameSubscriptionNodes 验证管理备注不会覆盖跨 Stack 的订阅节点名。
func TestUserRemarkDoesNotRenameSubscriptionNodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*configdoc.Document)
	}{
		{name: "inbound remarks", mutate: func(*configdoc.Document) {}},
		{
			name: "generated names",
			mutate: func(doc *configdoc.Document) {
				for stackIndex := range doc.Stacks {
					for inboundIndex := range doc.Stacks[stackIndex].Xray.Inbounds {
						doc.Stacks[stackIndex].Xray.Inbounds[inboundIndex].Remark = ""
					}
				}
			},
		},
		{
			name: "user template",
			mutate: func(doc *configdoc.Document) {
				doc.Users[0].DisplayTemplate = "{{ .stack | toUpper }} {{ .remark }}"
			},
		},
		{
			name: "inbound template",
			mutate: func(doc *configdoc.Document) {
				doc.Stacks[1].Xray.Inbounds[1].DisplayTemplate = "{{ .stack | toUpper }} {{ .remark }}"
			},
		},
		{
			name: "explicit user override",
			mutate: func(doc *configdoc.Document) {
				doc.Stacks[1].Xray.Inbounds[1].Users[0].Remark = "usa1 personal vmess"
			},
		},
		{
			name: "named profile",
			mutate: func(doc *configdoc.Document) {
				doc.Users[0].Profiles = []configdoc.Profile{{
					Name: "mobile", UUID: doc.Users[0].UUID, SSPassword: doc.Users[0].SSPassword, Remark: "usa1 mobile vmess",
				}}
				doc.Stacks[1].Xray.Inbounds[1].Users[0].Profile = "mobile"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := loadExampleDoc(t)
			tc.mutate(doc)
			render := func() subgen.Input {
				t.Helper()
				require.Empty(t, configdoc.Validate(doc))
				set, err := configdoc.Compile(doc)
				require.NoError(t, err)
				input, err := subgen.RenderStackInputAt(set, "main", "2026-11-01T00:00:00Z")
				require.NoError(t, err)
				require.NotEmpty(t, input.Nodes)
				return input
			}
			before := render()
			doc.Users[0].Remark = "xiaochen"
			require.Equal(t, before, render())
		})
	}
}
