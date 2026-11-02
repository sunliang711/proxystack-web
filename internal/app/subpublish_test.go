package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/subpublish"
	"proxystack-web/internal/subscription"
)

type recordedDelivery struct {
	target subpublish.Target
	bundle []byte
}

type publishClientFake struct {
	mu           sync.Mutex
	deliveries   []recordedDelivery
	failHost     string
	wrongHost    string
	probeStart   chan struct{}
	probeDone    chan struct{}
	deliverStart chan struct{}
	deliverDone  chan struct{}
	probeOnce    sync.Once
	deliverOnce  sync.Once
}

func (f *publishClientFake) Probe(ctx context.Context, _ subpublish.Identity, _ subpublish.Target) (*subpublish.ProbeResult, error) {
	if f.probeStart != nil {
		f.probeOnce.Do(func() { close(f.probeStart) })
		select {
		case <-f.probeDone:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &subpublish.ProbeResult{}, nil
}

func (f *publishClientFake) Deliver(ctx context.Context, _ subpublish.Identity, target subpublish.Target, bundle []byte) (*subpublish.DeliveryResult, error) {
	f.mu.Lock()
	f.deliveries = append(f.deliveries, recordedDelivery{target: target, bundle: append([]byte(nil), bundle...)})
	f.mu.Unlock()
	if f.deliverStart != nil {
		f.deliverOnce.Do(func() { close(f.deliverStart) })
		select {
		case <-f.deliverDone:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if target.SSHHost == f.failHost {
		return nil, errors.New("delivery failed")
	}
	header, err := subscription.ReadHeader(bundle)
	if err != nil {
		return nil, err
	}
	sequence := header.Sequence
	if target.SSHHost == f.wrongHost {
		sequence++
	}
	return &subpublish.DeliveryResult{Status: "imported", Source: target.Source, Sequence: sequence}, nil
}

func publishTargetInput(name, host string) app.PublishTargetInput {
	return app.PublishTargetInput{
		Name: name, SSHHost: host, SSHPort: 22, SSHUser: "proxystack-tunnel",
		HostKeyAlgorithm: "ssh-ed25519", HostKeyFingerprint: "SHA256:" + strings.Repeat("A", 43),
		RemotePort: 9444, Source: "main", Enabled: true,
	}
}

func TestSSHPublishIdentityAndTargetSecretsAreNotExposed(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)

	identity, err := h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)
	require.True(t, identity.Configured)
	require.NotEmpty(t, identity.PublicKey)
	encodedIdentity, err := json.Marshal(identity)
	require.NoError(t, err)
	require.NotContains(t, string(encodedIdentity), "seed")
	require.NotContains(t, string(encodedIdentity), "private")

	setup, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("primary", "sub-a.example.com"))
	require.NoError(t, err)
	require.NotEmpty(t, setup.Secret)
	require.Equal(t, configdoc.MaskedValue, setup.Target.Secret)
	require.True(t, setup.Target.SecretConfigured)
	require.Contains(t, setup.AuthorizedKeysEntry, `command="/bin/false"`)
	require.Contains(t, setup.AuthorizedKeysEntry, `permitopen="127.0.0.1:9444"`)
	require.Contains(t, setup.SSHDConfig, "PermitOpen 127.0.0.1:9444")
	require.Contains(t, setup.SSHDConfig, "ForceCommand /bin/false")
	require.Contains(t, setup.PublishTokenCommand, "set-publish-token main")

	targets, err := h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, configdoc.MaskedValue, targets[0].Secret)
	encodedTargets, err := json.Marshal(targets)
	require.NoError(t, err)
	require.NotContains(t, string(encodedTargets), setup.Secret)
	h.svc.SubPublisher = &publishClientFake{}
	_, err = h.svc.TestPublishTarget(ctx, h.actor, targets[0].ID)
	require.NoError(t, err)
	targets, err = h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.False(t, targets[0].LastTestAt.IsZero())

	rotated, err := h.svc.RotatePublishTargetSecret(ctx, h.actor, targets[0].ID)
	require.NoError(t, err)
	require.NotEmpty(t, rotated.Secret)
	require.NotEqual(t, setup.Secret, rotated.Secret)
	targets, err = h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.True(t, targets[0].LastTestAt.IsZero())
}

func TestPublishTargetRejectsInvalidNameAndSource(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	_, err := h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)

	input := publishTargetInput("line\nbreak", "sub-a.example.com")
	_, err = h.svc.CreatePublishTarget(ctx, h.actor, input)
	require.ErrorContains(t, err, "control characters")

	input = publishTargetInput("wrong-source", "sub-a.example.com")
	input.Source = "other"
	_, err = h.svc.CreatePublishTarget(ctx, h.actor, input)
	require.ErrorContains(t, err, "current subscription source")

	input = publishTargetInput("unsupported-key", "sub-a.example.com")
	input.HostKeyAlgorithm = "ecdsa-sha2-nistp384"
	_, err = h.svc.CreatePublishTarget(ctx, h.actor, input)
	require.ErrorContains(t, err, "host_key_algorithm")
}

func TestOnlinePublishRejectsTargetAfterSourceChanges(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	_, err := h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)
	setup, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("primary", "sub-a.example.com"))
	require.NoError(t, err)

	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Subscription.SourceName = "other"
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)

	_, err = h.svc.PublishSubscriptionBundleOnline(ctx, h.actor, []int64{setup.Target.ID})
	require.ErrorContains(t, err, "different subscription source")
}

func TestOnlinePublishSendsSameBundleAndKeepsPartialSuccess(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	_, err := h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)
	first, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("first", "sub-a.example.com"))
	require.NoError(t, err)
	second, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("second", "sub-b.example.com"))
	require.NoError(t, err)

	fake := &publishClientFake{failHost: "sub-b.example.com"}
	h.svc.SubPublisher = fake
	started, err := h.svc.PublishSubscriptionBundleOnline(ctx, h.actor, []int64{first.Target.ID, second.Target.ID})
	require.NoError(t, err)
	task := h.waitTask(t, started.TaskID)
	require.Equal(t, app.TaskFailed, task.Status)
	require.Contains(t, task.Error, "1 of 2 publish targets failed")

	fake.mu.Lock()
	deliveries := append([]recordedDelivery(nil), fake.deliveries...)
	fake.mu.Unlock()
	require.Len(t, deliveries, 2)
	require.True(t, bytes.Equal(deliveries[0].bundle, deliveries[1].bundle))

	targets, err := h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.Len(t, targets, 2)
	byName := map[string]app.PublishTargetView{targets[0].Name: targets[0], targets[1].Name: targets[1]}
	require.Equal(t, started.Sequence, byName["first"].LastSequence)
	require.Empty(t, byName["first"].LastPublishError)
	require.Zero(t, byName["second"].LastSequence)
	require.Equal(t, "delivery failed", byName["second"].LastPublishError)
}

func TestOnlinePublishRejectsUnexpectedSequence(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	_, err := h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)
	setup, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("primary", "sub-a.example.com"))
	require.NoError(t, err)

	h.svc.SubPublisher = &publishClientFake{wrongHost: "sub-a.example.com"}
	started, err := h.svc.PublishSubscriptionBundleOnline(ctx, h.actor, []int64{setup.Target.ID})
	require.NoError(t, err)
	task := h.waitTask(t, started.TaskID)
	require.Equal(t, app.TaskFailed, task.Status)
	require.Contains(t, task.Log[1], "unexpected sequence")

	targets, err := h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.Zero(t, targets[0].LastSequence)
	require.Equal(t, "subscription server returned an unexpected sequence", targets[0].LastPublishError)
}

func TestTargetTestDoesNotOverwriteStatusAfterConfigurationChanges(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	_, err := h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)
	setup, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("primary", "sub-a.example.com"))
	require.NoError(t, err)

	fake := &publishClientFake{probeStart: make(chan struct{}), probeDone: make(chan struct{})}
	h.svc.SubPublisher = fake
	testDone := make(chan error, 1)
	go func() {
		_, err := h.svc.TestPublishTarget(ctx, h.actor, setup.Target.ID)
		testDone <- err
	}()
	select {
	case <-fake.probeStart:
	case <-time.After(time.Second):
		t.Fatal("target test did not start")
	}
	_, err = h.svc.UpdatePublishTarget(ctx, h.actor, setup.Target.ID, publishTargetInput("primary", "sub-b.example.com"))
	require.NoError(t, err)
	close(fake.probeDone)
	require.NoError(t, <-testDone)

	targets, err := h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, "sub-b.example.com", targets[0].SSHHost)
	require.True(t, targets[0].LastTestAt.IsZero())
}

func TestOnlinePublishDoesNotOverwriteStatusAfterConfigurationChanges(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	_, err := h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)
	setup, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("primary", "sub-a.example.com"))
	require.NoError(t, err)

	fake := &publishClientFake{deliverStart: make(chan struct{}), deliverDone: make(chan struct{})}
	h.svc.SubPublisher = fake
	started, err := h.svc.PublishSubscriptionBundleOnline(ctx, h.actor, []int64{setup.Target.ID})
	require.NoError(t, err)
	select {
	case <-fake.deliverStart:
	case <-time.After(time.Second):
		t.Fatal("online publish did not start")
	}
	_, err = h.svc.UpdatePublishTarget(ctx, h.actor, setup.Target.ID, publishTargetInput("primary", "sub-b.example.com"))
	require.NoError(t, err)
	close(fake.deliverDone)
	task := h.waitTask(t, started.TaskID)
	require.Equal(t, app.TaskSucceeded, task.Status, task.Error)
	require.Contains(t, strings.Join(task.Log, "\n"), "configuration changed; delivery status was not updated")

	targets, err := h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, "sub-b.example.com", targets[0].SSHHost)
	require.True(t, targets[0].LastPublishAt.IsZero())
	require.Zero(t, targets[0].LastSequence)
}
