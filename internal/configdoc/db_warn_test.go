package configdoc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
)

// TestDBWarnThreshold 验证数据库大小告警阈值缺省补齐，并限制在 1 MB 到上限之间。
func TestDBWarnThreshold(t *testing.T) {
	doc := loadExampleDoc(t)
	require.Equal(t, configdoc.DefaultDBWarnMB, doc.Settings.Notify.DBWarnMB)
	require.Empty(t, configdoc.Validate(doc))

	for _, value := range []int{-1, configdoc.MaxDBWarnMB + 1} {
		doc.Settings.Notify.DBWarnMB = value
		issues := configdoc.Validate(doc)
		require.NotEmpty(t, issues, value)
		require.Equal(t, "settings.notify.db_warn_mb", issues[0].Path, value)
	}
	doc.Settings.Notify.DBWarnMB = configdoc.MaxDBWarnMB
	require.Empty(t, configdoc.Validate(doc))
}
