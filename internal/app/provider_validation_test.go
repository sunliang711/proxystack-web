package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
)

// providerValidationGate models a slow native core without blocking unrelated database work.
type providerValidationGate struct {
	started chan string
	release chan struct{}
}

// Check exposes each validation namespace and waits until released or canceled.
func (c *providerValidationGate) Check(ctx context.Context, _, name string, _ []byte) error {
	select {
	case c.started <- name:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestProviderValidationAllowsWrites verifies slow validation releases SQLite and rejects stale saves.
func TestProviderValidationAllowsWrites(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "runtime write"
		if conflict {
			name = "configuration conflict"
		}
		t.Run(name, func(t *testing.T) {
			h := newRuntimeHarness(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			doc, err := h.svc.LoadConfig(ctx)
			require.NoError(t, err)
			stack := doc.Stacks[1]
			stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600, Filter: "HK"}}
			gate := &providerValidationGate{started: make(chan string, 1), release: make(chan struct{})}
			h.svc.Checker = gate
			result := make(chan error, 1)
			go func() { _, err := h.svc.UpdateStack(ctx, h.actor, stack.ID, stack); result <- err }()
			select {
			case <-gate.started:
			case <-ctx.Done():
				t.Fatal("native validation did not start")
			}
			written := make(chan error, 1)
			go func() {
				if conflict {
					user := doc.Users[0]
					user.Remark = "saved during native validation"
					_, err := h.svc.UpdateUser(ctx, h.actor, user.ID, user)
					written <- err
					return
				}
				written <- h.svc.Store.InTx(ctx, func(q *dbq.Queries) error { return store.PutJSON(ctx, q, "provider_validation_progress", 1) })
			}()
			select {
			case err = <-written:
			case <-time.After(2 * time.Second):
				err = context.DeadlineExceeded
			}
			close(gate.release)
			require.NoError(t, err, "writes must finish while native validation is still blocked")
			if conflict {
				require.ErrorContains(t, <-result, "configuration changed during filter validation")
			} else {
				require.NoError(t, <-result)
			}
			loaded, err := h.svc.LoadConfig(ctx)
			if !conflict {
				require.NoError(t, err)
				require.Len(t, loaded.Stacks[1].Mihomo.Providers, 1)
			} else {
				require.NoError(t, err)
				require.Equal(t, "saved during native validation", loaded.Users[0].Remark)
				require.Empty(t, loaded.Stacks[1].Mihomo.Providers)
			}
		})
	}
}

// TestProviderValidationNamespaces verifies parallel validations do not share temporary core files.
func TestProviderValidationNamespaces(t *testing.T) {
	h := newRuntimeHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	gate := &providerValidationGate{started: make(chan string, 2), release: make(chan struct{})}
	h.svc.Checker = gate
	result := make(chan error, 2)
	for _, original := range doc.Stacks[1:] {
		stack := original.Clone()
		stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600, Filter: "HK"}}
		go func() { _, err := h.svc.UpdateStack(ctx, h.actor, stack.ID, stack); result <- err }()
	}
	names := make([]string, 0, 2)
	for len(names) < 2 {
		select {
		case name := <-gate.started:
			names = append(names, name)
		case <-ctx.Done():
			close(gate.release)
			t.Fatal("parallel validation did not start")
		}
	}
	require.NotEqual(t, names[0], names[1])
	require.True(t, strings.HasPrefix(names[0], "provider-filter-validation-"))
	close(gate.release)
	first, second := <-result, <-result
	if first == nil {
		require.ErrorContains(t, second, "configuration changed")
	} else {
		require.NoError(t, second)
		require.ErrorContains(t, first, "configuration changed")
	}
}
