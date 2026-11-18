package app_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store/dbq"
)

// TestDeleteInboundUpstreamUser removes stale credentials through the complete service transaction.
func TestDeleteInboundUpstreamUser(t *testing.T) {
	for _, protocol := range []string{"vmess", "shadowsocks"} {
		t.Run(protocol, func(t *testing.T) {
			ctx := context.Background()
			h := newRuntimeHarness(t)
			bob, err := h.svc.CreateUser(ctx, h.actor, configdoc.User{Name: "bob", Enabled: true, ResetMode: "none"})
			require.NoError(t, err)
			doc, err := h.svc.LoadConfig(ctx)
			require.NoError(t, err)
			edge := doc.Stacks[doc.StackByName("usa1")]
			inbound := &edge.Xray.Inbounds[1]
			inbound.Sub = false
			inbound.Users = []configdoc.UserRef{{User: "bob"}}
			if protocol == "vmess" {
				inbound.Users = append(inbound.Users, configdoc.UserRef{User: "alice"})
			} else {
				inbound.Protocol, inbound.Network = "shadowsocks", ""
				inbound.Method, inbound.Password = "aes-128-gcm", "server-password"
			}
			_, err = h.svc.UpdateStack(ctx, h.actor, edge.ID, edge)
			require.NoError(t, err)
			auto := doc.Stacks[doc.StackByName("auto")]
			upstream := &auto.Mihomo.Upstreams[0]
			upstream.Type, upstream.Inbound, upstream.User = "xray-inbound", inbound.Name, "bob"
			_, err = h.svc.UpdateStack(ctx, h.actor, auto.ID, auto)
			require.NoError(t, err)
			require.NoError(t, h.svc.DeleteUser(ctx, h.actor, bob.ID))
			doc, err = h.svc.LoadConfig(ctx)
			require.NoError(t, err)
			require.Equal(t, -1, doc.UserByName("bob"))
			got := doc.Stacks[doc.StackByName("auto")].Mihomo.Upstreams[0]
			require.Empty(t, got.User)
			require.Empty(t, got.Profile)
			require.Empty(t, configdoc.Validate(doc))
			preview, err := h.svc.PreviewStack(ctx, auto.ID)
			require.NoError(t, err)
			require.Empty(t, preview.Errors)
		})
	}
}

// TestInboundUpstreamDefaultProfileSnapshot keeps mutation responses, revision JSON and reloads consistent.
func TestInboundUpstreamDefaultProfileSnapshot(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	auto := doc.Stacks[doc.StackByName("auto")]
	upstream := &auto.Mihomo.Upstreams[0]
	upstream.Type, upstream.Inbound, upstream.User, upstream.Profile = "xray-inbound", "vmess", "alice", "default"
	saved, err := h.svc.UpdateStack(ctx, h.actor, auto.ID, auto)
	require.NoError(t, err)
	require.Empty(t, saved.Mihomo.Upstreams[0].Profile)
	var snapshot configdoc.Document
	require.NoError(t, h.svc.Store.InTx(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListConfigRevisions(ctx, dbq.ListConfigRevisionsParams{Limit: 1})
		if err != nil {
			return err
		}
		require.Len(t, rows, 1)
		revision, err := q.GetConfigRevision(ctx, rows[0].ID)
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(revision.Snapshot), &snapshot)
	}))
	doc, err = h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	loaded := doc.Stacks[doc.StackByName("auto")].Mihomo.Upstreams[0]
	require.Equal(t, saved.Mihomo.Upstreams[0], loaded)
	require.Equal(t, snapshot.Stacks[snapshot.StackByName("auto")].Mihomo.Upstreams[0], loaded)
}
