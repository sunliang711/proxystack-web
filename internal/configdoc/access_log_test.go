package configdoc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/generator/xray"
)

// TestXrayAccessLogInheritance 验证全局默认、Stack 覆盖及旧配置缺省行为，并确保错误日志级别不受影响。
func TestXrayAccessLogInheritance(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name   string
		global *bool
		stack  string
		access string
	}{
		{name: "legacy"},
		{name: "global on", global: &on, stack: configdoc.Inherit},
		{name: "global off", global: &off, stack: configdoc.Inherit, access: "none"},
		{name: "stack on", global: &off, stack: configdoc.On},
		{name: "stack off", global: &on, stack: configdoc.Off, access: "none"},
		{name: "legacy global stack off", stack: configdoc.Off, access: "none"},
	} {
		for _, level := range []string{"warning", "error"} {
			t.Run(tc.name+"/"+level, func(t *testing.T) {
				doc := loadExampleDoc(t)
				doc.Settings.Defaults.Xray.AccessLog = tc.global
				doc.Settings.Defaults.Xray.LogLevel = level
				doc.Stacks[doc.StackByName("usa1")].Xray.AccessLog = tc.stack
				doc.Normalize()
				for _, format := range []string{configdoc.FormatJSON, configdoc.FormatYAML} {
					data, err := configdoc.Marshal(doc, format)
					require.NoError(t, err)
					roundTrip, err := configdoc.Parse(data)
					require.NoError(t, err)
					generated, err := xray.RenderConfig(compileResolved(t, roundTrip), "usa1")
					require.NoError(t, err)
					require.Equal(t, tc.access, generated.Log.Access)
					require.Equal(t, level, generated.Log.LogLevel)
					require.NotNil(t, generated.Stats, "access logs do not control traffic statistics")
				}
			})
		}
	}
}

// TestXrayAccessLogValidation 验证 Stack 只能使用已有三态枚举。
func TestXrayAccessLogValidation(t *testing.T) {
	doc := loadExampleDoc(t)
	doc.Stacks[1].Xray.AccessLog = "invalid"
	require.Contains(t, configdoc.Validate(doc).Error(), "stacks[1].xray.access_log")
}

// TestLegacyAccessLogDefaults 验证旧文档导入后的全局开关为开启，各 Stack 继承全局。
func TestLegacyAccessLogDefaults(t *testing.T) {
	doc := loadExampleDoc(t)
	require.NotNil(t, doc.Settings.Defaults.Xray.AccessLog)
	require.True(t, *doc.Settings.Defaults.Xray.AccessLog)
	for _, stack := range doc.Stacks {
		require.Equal(t, configdoc.Inherit, stack.Xray.AccessLog)
	}
}
