package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/deps"
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

type deployClientFake struct {
	inspect       *subpublish.DeployStatus
	rollback      *subpublish.DeployResult
	err           error
	rollbackStart chan struct{}
	rollbackDone  chan struct{}
	rollbackOnce  sync.Once
	tokens        []string
}

func (f *deployClientFake) InspectDeploy(context.Context, subpublish.Identity, subpublish.Target) (*subpublish.DeployStatus, error) {
	return f.inspect, f.err
}

func (f *deployClientFake) Deploy(context.Context, subpublish.Identity, subpublish.Target, subpublish.DeployRequest) (*subpublish.DeployResult, error) {
	return nil, errors.New("unexpected deploy call")
}

func (f *deployClientFake) ConfigurePublishToken(_ context.Context, _ subpublish.Identity, _ subpublish.Target, source, secret string) error {
	f.tokens = append(f.tokens, source+":"+secret)
	return f.err
}

func (f *deployClientFake) RollbackDeploy(context.Context, subpublish.Identity, subpublish.Target) (*subpublish.DeployResult, error) {
	if f.rollbackStart != nil {
		f.rollbackOnce.Do(func() { close(f.rollbackStart) })
		<-f.rollbackDone
	}
	return f.rollback, f.err
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
	require.Contains(t, setup.AuthorizedKeysEntry, `permitopen="127.0.0.1:9444"`)
	require.NotContains(t, setup.AuthorizedKeysEntry, `command=`)
	require.Contains(t, setup.SSHDConfig, "PermitOpen 127.0.0.1:9444")
	require.NotContains(t, setup.SSHDConfig, "ForceCommand")
	require.Contains(t, setup.PublishTokenCommand, "set-publish-token main")

	targets, err := h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, configdoc.MaskedValue, targets[0].Secret)
	encodedTargets, err := json.Marshal(targets)
	require.NoError(t, err)
	require.NotContains(t, string(encodedTargets), setup.Secret)
	viewed, err := h.svc.GetPublishTargetSetup(ctx, h.actor, targets[0].ID)
	require.NoError(t, err)
	require.Equal(t, setup.Secret, viewed.Secret)
	require.Equal(t, setup.AuthorizedKeysEntry, viewed.AuthorizedKeysEntry)
	viewedAgain, err := h.svc.GetPublishTargetSetup(ctx, h.actor, targets[0].ID)
	require.NoError(t, err)
	require.Equal(t, setup.Secret, viewedAgain.Secret)
	auditPage, err := h.svc.ListAudit(ctx, app.ActionConfigSecretView, h.actor.Name, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), auditPage.Total)
	require.Equal(t, "subscription_publish_target:primary", auditPage.Items[0].Object)
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

func TestSubscriptionDeploymentInspectAndRollbackUpdateTargetState(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	_, err := h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)
	setup, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("primary", "sub-a.example.com"))
	require.NoError(t, err)

	h.svc.SubDeployer = &deployClientFake{inspect: &subpublish.DeployStatus{
		OS: "linux", Arch: "amd64", Sudo: true, State: "installed", Version: "v0.2.0", PreviousVersion: "v0.1.0",
		ServiceActive: true, ServiceEnabled: true,
	}}
	status, err := h.svc.InspectPublishTargetDeploy(ctx, h.actor, setup.Target.ID)
	require.NoError(t, err)
	require.Equal(t, "v0.2.0", status.Version)
	targets, err := h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.Equal(t, "installed", targets[0].DeployState)
	require.Equal(t, "v0.2.0", targets[0].CurrentVersion)
	require.Equal(t, "v0.1.0", targets[0].PreviousVersion)
	require.True(t, targets[0].LastDeployError == "")

	h.svc.SubDeployer = &deployClientFake{rollback: &subpublish.DeployResult{
		Action: "rolled_back", Version: "v0.1.0", PreviousVersion: "v0.2.0",
	}}
	taskID, err := h.svc.RollbackPublishTarget(ctx, h.actor, setup.Target.ID)
	require.NoError(t, err)
	task := h.waitTask(t, taskID)
	require.Equal(t, app.TaskSucceeded, task.Status)
	targets, err = h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.Equal(t, "v0.1.0", targets[0].CurrentVersion)
	require.Equal(t, "v0.2.0", targets[0].PreviousVersion)
}

func TestSubscriptionDeploymentSkipsInstalledLatestRelease(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	_, err := h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)
	setup, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("primary", "sub-a.example.com"))
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+deps.Repo(deps.Self)+"/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.2.0", "assets": []map[string]any{}})
	}))
	t.Cleanup(server.Close)
	previous := deps.GitHubAPI
	deps.GitHubAPI = server.URL
	t.Cleanup(func() { deps.GitHubAPI = previous })

	for _, installed := range []string{"v0.2.0", "0.2.0", "v0.3.0"} {
		fake := &deployClientFake{inspect: &subpublish.DeployStatus{
			OS: "linux", Arch: "amd64", Sudo: true, State: "installed", Version: installed, PreviousVersion: "v0.1.0",
			ServiceActive: true, ServiceEnabled: true,
		}}
		h.svc.SubDeployer = fake
		taskID, err := h.svc.DeployPublishTarget(ctx, h.actor, setup.Target.ID, app.SubDeployRequest{})
		require.NoError(t, err)
		task := h.waitTask(t, taskID)
		require.Equal(t, app.TaskSucceeded, task.Status, installed)
		require.Equal(t, []string{"main:" + setup.Secret}, fake.tokens, installed)
		targets, err := h.svc.ListPublishTargets(ctx)
		require.NoError(t, err)
		require.Equal(t, installed, targets[0].CurrentVersion)
		require.Equal(t, "v0.1.0", targets[0].PreviousVersion)
	}

	for _, status := range []*subpublish.DeployStatus{
		{OS: "linux", Arch: "amd64", Sudo: true, State: "installed", Version: "v0.1.0", ServiceActive: true},
		{OS: "linux", Arch: "amd64", Sudo: true, State: "installed", Version: "v0.2.0", ServiceActive: false},
		{OS: "linux", Arch: "amd64", Sudo: true, State: "installed", Version: "dev", ServiceActive: true},
	} {
		fake := &deployClientFake{inspect: status}
		h.svc.SubDeployer = fake
		taskID, err := h.svc.DeployPublishTarget(ctx, h.actor, setup.Target.ID, app.SubDeployRequest{})
		require.NoError(t, err)
		task := h.waitTask(t, taskID)
		require.Equal(t, app.TaskFailed, task.Status, status.Version)
		require.Empty(t, fake.tokens, status.Version)
	}
}

func TestSubscriptionTargetCannotChangeDuringDeployment(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	_, err := h.svc.GenerateSSHPublishIdentity(ctx, h.actor)
	require.NoError(t, err)
	setup, err := h.svc.CreatePublishTarget(ctx, h.actor, publishTargetInput("primary", "sub-a.example.com"))
	require.NoError(t, err)

	fake := &deployClientFake{
		rollback:      &subpublish.DeployResult{Action: "rolled_back", Version: "v0.1.0", PreviousVersion: "v0.2.0"},
		rollbackStart: make(chan struct{}), rollbackDone: make(chan struct{}),
	}
	h.svc.SubDeployer = fake
	started, err := h.svc.RollbackPublishTarget(ctx, h.actor, setup.Target.ID)
	require.NoError(t, err)
	select {
	case <-fake.rollbackStart:
	case <-time.After(time.Second):
		t.Fatal("rollback did not start")
	}
	_, err = h.svc.RotatePublishTargetSecret(ctx, h.actor, setup.Target.ID)
	require.ErrorContains(t, err, "cannot be changed")
	_, err = h.svc.UpdatePublishTarget(ctx, h.actor, setup.Target.ID, publishTargetInput("primary", "sub-b.example.com"))
	require.ErrorContains(t, err, "cannot be changed")
	err = h.svc.DeletePublishTarget(ctx, h.actor, setup.Target.ID)
	require.ErrorContains(t, err, "cannot be changed")
	_, err = h.svc.PublishSubscriptionBundleOnline(ctx, h.actor, []int64{setup.Target.ID})
	require.ErrorContains(t, err, "publish or deployment")
	close(fake.rollbackDone)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, started).Status)
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

func TestSubscriptionTargetCannotChangeOrDeployDuringOnlinePublish(t *testing.T) {
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
	require.ErrorContains(t, err, "cannot be changed")
	_, err = h.svc.RollbackPublishTarget(ctx, h.actor, setup.Target.ID)
	require.ErrorContains(t, err, "publish or deployment")
	close(fake.deliverDone)
	task := h.waitTask(t, started.TaskID)
	require.Equal(t, app.TaskSucceeded, task.Status, task.Error)

	targets, err := h.svc.ListPublishTargets(ctx)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, "sub-a.example.com", targets[0].SSHHost)
	require.False(t, targets[0].LastPublishAt.IsZero())
	require.Equal(t, started.Sequence, targets[0].LastSequence)
}
