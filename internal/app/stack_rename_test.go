package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
)

// TestRenameStackThenReuseOldName 验证改名后入站显示名跟着换成新名字，旧名字可以再用来新建 stack；
// 显示名仍与其他 stack 重名时，新建失败并指出冲突的 stack 和入站。
func TestRenameStackThenReuseOldName(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	create := func(name string) (*configdoc.Stack, error) {
		return h.svc.CreateStack(ctx, h.actor, configdoc.NewStackOptions{Template: configdoc.TemplatePair, Name: name})
	}

	east, err := create("east1")
	require.NoError(t, err)
	require.Equal(t, "east1 vmess", east.Xray.Inbounds[1].Remark)
	renamed := *east
	renamed.Name = "east2"
	renamed.Xray.Inbounds[0].Remark = "custom relay"
	saved, err := h.svc.UpdateStack(ctx, h.actor, east.ID, renamed)
	require.NoError(t, err)
	require.Equal(t, "east2 vmess", saved.Xray.Inbounds[1].Remark, "generated remarks follow the new name")
	require.Equal(t, "custom relay", saved.Xray.Inbounds[0].Remark, "hand-written remarks are kept")
	_, err = create("east1")
	require.NoError(t, err, "the old name can be reused")

	west, err := create("west1")
	require.NoError(t, err)
	edited := *west
	edited.Xray.Inbounds[1].Remark = "west2 vmess"
	_, err = h.svc.UpdateStack(ctx, h.actor, west.ID, edited)
	require.NoError(t, err)
	_, err = create("west2")
	var fieldErr *app.FieldError
	require.True(t, errors.As(err, &fieldErr), err)
	require.Equal(t, "name", fieldErr.Field)
	require.Contains(t, fieldErr.Message, "「west2 vmess」已被 stack west1 的入站 vmess 使用")

	_, err = h.svc.CloneStack(ctx, h.actor, west.ID, "west2")
	require.True(t, errors.As(err, &fieldErr), err)
	require.Contains(t, fieldErr.Message, "stack west1")
}
