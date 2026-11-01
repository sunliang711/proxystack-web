package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/xrayapi"
)

// legacyEmailAPI 模拟升级前启动、只认识旧 email 的 Xray 实例。
type legacyEmailAPI struct {
	xrayapi.API
	removed []string
	added   []string
	fail    bool
}

func (a *legacyEmailAPI) RemoveUser(ctx context.Context, server, tag, email string) error {
	a.removed = append(a.removed, email)
	if email != "u1" || a.fail {
		return fmt.Errorf("user %s was not found", email)
	}
	return a.API.RemoveUser(ctx, server, tag, email)
}

func (a *legacyEmailAPI) AddUser(ctx context.Context, server, patch string) error {
	a.added = append(a.added, patch)
	return a.API.AddUser(ctx, server, patch)
}

// TestPauseLegacyStatsEmail 验证升级后无需重启 Xray 就可暂停旧用户，恢复时使用可读的新标识。
func TestPauseLegacyStatsEmail(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%t", fail), func(t *testing.T) {
			ctx := context.Background()
			h := newRuntimeHarness(t)
			id, err := h.svc.Apply(ctx, h.actor, nil)
			require.NoError(t, err)
			require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)
			api := &legacyEmailAPI{API: h.api, fail: fail}
			h.svc.XrayAPI = api
			doc, err := h.svc.LoadConfig(ctx)
			require.NoError(t, err)
			alice := doc.Users[doc.UserByName("alice")]
			result, err := h.svc.SetUserPause(ctx, h.actor, alice.ID, true, []string{"usa1"})
			require.NoError(t, err)
			require.Equal(t, []string{"alice.u1", "u1"}, api.removed)
			if fail {
				require.NotEmpty(t, result.Notes)
				return
			}
			require.Empty(t, result.Notes)
			result, err = h.svc.SetUserPause(ctx, h.actor, alice.ID, true, nil)
			require.NoError(t, err)
			require.Empty(t, result.Notes)
			require.Len(t, api.added, 1)
			var patch struct {
				Inbounds []struct {
					Settings struct {
						Clients []struct {
							Email string `json:"email"`
						} `json:"clients"`
					} `json:"settings"`
				} `json:"inbounds"`
			}
			require.NoError(t, json.Unmarshal([]byte(api.added[0]), &patch))
			require.Equal(t, "alice.u1", patch.Inbounds[0].Settings.Clients[0].Email)
		})
	}
}
