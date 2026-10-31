package configdoc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
)

// TestDisplayTemplateFieldErrors 确保所有模板入口都在保存时校验，包含未被引用的用户与档案。
func TestDisplayTemplateFieldErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		mutate func(*configdoc.Document)
	}{
		{
			name: "unused user", path: "users[0].display_template",
			mutate: func(doc *configdoc.Document) {
				doc.Stacks = nil
				doc.Users[0].DisplayTemplate = ".stack|toUpper"
			},
		},
		{
			name: "unused profile", path: "users[0].profiles[0].display_template",
			mutate: func(doc *configdoc.Document) {
				doc.Users[0].Profiles = []configdoc.Profile{{Name: "mobile", DisplayTemplate: ".stack | toUpper"}}
			},
		},
		{
			name: "inbound", path: "stacks[0].xray.inbounds[0].display_template",
			mutate: func(doc *configdoc.Document) {
				doc.Stacks[0].Xray.Inbounds[0].DisplayTemplate = ".inbound"
			},
		},
		{
			name: "inbound user", path: "stacks[0].xray.inbounds[0].users[0].display_template",
			mutate: func(doc *configdoc.Document) {
				doc.Stacks[0].Xray.Inbounds[0].Users[0].DisplayTemplate = ".remark|trim"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := loadExampleDoc(t)
			tc.mutate(doc)
			issues := configdoc.Validate(doc)
			require.Len(t, issues, 1)
			require.Equal(t, tc.path, issues[0].Path)
			require.Contains(t, issues[0].Message, "{{ ... }}")
		})
	}
}

func TestDisplayTemplateUniqueNames(t *testing.T) {
	doc := loadExampleDoc(t)
	doc.Users[0].DisplayTemplate = "{{ .stack | toUpper }} {{ .inbound }}"
	require.Empty(t, configdoc.Validate(doc))

	// 同一 Stack 有多个订阅入站时，仅使用 Stack 名仍应被重名校验拦截。
	doc.Users[0].DisplayTemplate = "{{ .stack | toUpper }}"
	require.Contains(t, configdoc.Validate(doc).Error(), "duplicate proxy name")

	doc.Stacks = nil
	doc.Users[0].DisplayTemplate = "美国节点"
	require.Empty(t, configdoc.Validate(doc), "固定文字模板仍可保存")
}
