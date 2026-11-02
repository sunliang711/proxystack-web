package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/auth"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/subpublish"
	"proxystack-web/internal/subscription"
)

const (
	settingSubPublishIdentity = "sub_publish_ssh_identity"
	settingSubPublishTargets  = "sub_publish_targets"

	ActionSubPublishIdentity = "subscription.ssh_identity"
	ActionSubPublishTarget   = "subscription.publish_target"
	ActionSubPublishTest     = "subscription.publish_test"
	ActionSubPublishDeliver  = "subscription.deliver"

	defaultSSHPort       = 22
	defaultSubRemotePort = 9444
	maxPublishTargets    = 100
	publishConcurrency   = 4
	publishTargetTimeout = 2 * time.Minute
)

var (
	sshUserPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,31}$`)
	hostnamePattern     = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	hostFingerprintExpr = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
)

type sshIdentityState struct {
	Seed      []byte `json:"seed"`
	CreatedAt int64  `json:"created_at"`
}

// SSHPublishIdentityView 是页面可见的 SSH 客户端身份；私钥永不返回。
type SSHPublishIdentityView struct {
	Configured  bool      `json:"configured"`
	PublicKey   string    `json:"public_key"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

type publishTargetState struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	SSHHost            string `json:"ssh_host"`
	SSHPort            int    `json:"ssh_port"`
	SSHUser            string `json:"ssh_user"`
	HostKeyAlgorithm   string `json:"host_key_algorithm"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
	RemotePort         int    `json:"remote_port"`
	Source             string `json:"source"`
	Secret             string `json:"secret"`
	Enabled            bool   `json:"enabled"`
	LastTestAt         int64  `json:"last_test_at"`
	LastTestError      string `json:"last_test_error"`
	LastPublishAt      int64  `json:"last_publish_at"`
	LastSequence       int64  `json:"last_sequence"`
	LastPublishError   string `json:"last_publish_error"`
}

// PublishTargetInput 是创建或修改订阅端目标的输入。
type PublishTargetInput struct {
	Name               string `json:"name"`
	SSHHost            string `json:"ssh_host"`
	SSHPort            int    `json:"ssh_port" minimum:"0" maximum:"65535"`
	SSHUser            string `json:"ssh_user"`
	HostKeyAlgorithm   string `json:"host_key_algorithm"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
	RemotePort         int    `json:"remote_port" minimum:"0" maximum:"65535"`
	Source             string `json:"source"`
	Enabled            bool   `json:"enabled"`
}

// PublishTargetView 是目标列表返回的数据，不包含发布 Secret 明文。
type PublishTargetView struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	SSHHost            string    `json:"ssh_host"`
	SSHPort            int       `json:"ssh_port"`
	SSHUser            string    `json:"ssh_user"`
	HostKeyAlgorithm   string    `json:"host_key_algorithm"`
	HostKeyFingerprint string    `json:"host_key_fingerprint"`
	RemotePort         int       `json:"remote_port"`
	Source             string    `json:"source"`
	Secret             string    `json:"secret"`
	SecretConfigured   bool      `json:"secret_configured"`
	Enabled            bool      `json:"enabled"`
	LastTestAt         time.Time `json:"last_test_at"`
	LastTestError      string    `json:"last_test_error"`
	LastPublishAt      time.Time `json:"last_publish_at"`
	LastSequence       int64     `json:"last_sequence"`
	LastPublishError   string    `json:"last_publish_error"`
}

// PublishTargetSetup 是创建或轮换 Secret 后仅返回一次的配置材料。
type PublishTargetSetup struct {
	Target              PublishTargetView `json:"target"`
	Secret              string            `json:"secret"`
	AuthorizedKeysEntry string            `json:"authorized_keys_entry"`
	PublishTokenCommand string            `json:"publish_token_command"`
	SSHDConfig          string            `json:"sshd_config"`
}

// OnlinePublishStarted 是在线发布后台任务的入口结果。
type OnlinePublishStarted struct {
	TaskID   int64 `json:"task_id"`
	Sequence int64 `json:"sequence"`
}

func (s *Service) loadSSHIdentity(ctx context.Context, q *dbq.Queries) (sshIdentityState, error) {
	var state sshIdentityState
	err := store.GetJSON(ctx, q, settingSubPublishIdentity, &state)
	if errors.Is(err, store.ErrNotFound) {
		return sshIdentityState{}, nil
	}
	if err == nil && len(state.Seed) != 0 && len(state.Seed) != ed25519.SeedSize {
		return sshIdentityState{}, errors.New("stored SSH identity is invalid")
	}
	return state, err
}

func sshIdentityView(state sshIdentityState) (*SSHPublishIdentityView, error) {
	view := &SSHPublishIdentityView{}
	if len(state.Seed) == 0 {
		return view, nil
	}
	key, err := ssh.NewPublicKey(ed25519.NewKeyFromSeed(state.Seed).Public())
	if err != nil {
		return nil, err
	}
	view.Configured = true
	view.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	view.Fingerprint = ssh.FingerprintSHA256(key)
	view.CreatedAt = time.Unix(state.CreatedAt, 0)
	return view, nil
}

// SSHPublishIdentity 返回管理端用于在线发布的 SSH 公钥和指纹。
func (s *Service) SSHPublishIdentity(ctx context.Context) (*SSHPublishIdentityView, error) {
	var state sshIdentityState
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		state, err = s.loadSSHIdentity(ctx, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	return sshIdentityView(state)
}

// GenerateSSHPublishIdentity 首次生成全局 SSH Ed25519 客户端密钥。
func (s *Service) GenerateSSHPublishIdentity(ctx context.Context, actor Actor) (*SSHPublishIdentityView, error) {
	var state sshIdentityState
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		current, err := s.loadSSHIdentity(ctx, q)
		if err != nil {
			return err
		}
		if len(current.Seed) != 0 {
			state = current
			return nil
		}
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		state = sshIdentityState{Seed: private.Seed(), CreatedAt: s.Now().Unix()}
		if err := store.PutJSON(ctx, q, settingSubPublishIdentity, state); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubPublishIdentity, Summary: "生成 SSH 发布密钥",
		})
	})
	if err != nil {
		return nil, err
	}
	return sshIdentityView(state)
}

func loadPublishTargets(ctx context.Context, q *dbq.Queries) ([]publishTargetState, error) {
	targets := []publishTargetState{}
	err := store.GetJSON(ctx, q, settingSubPublishTargets, &targets)
	if errors.Is(err, store.ErrNotFound) {
		return targets, nil
	}
	return targets, err
}

func targetView(state publishTargetState) PublishTargetView {
	view := PublishTargetView{
		ID: state.ID, Name: state.Name, SSHHost: state.SSHHost, SSHPort: state.SSHPort, SSHUser: state.SSHUser,
		HostKeyAlgorithm: state.HostKeyAlgorithm, HostKeyFingerprint: state.HostKeyFingerprint,
		RemotePort: state.RemotePort, Source: state.Source, SecretConfigured: state.Secret != "", Enabled: state.Enabled,
		LastTestError: state.LastTestError, LastSequence: state.LastSequence, LastPublishError: state.LastPublishError,
	}
	if state.Secret != "" {
		view.Secret = configdoc.MaskedValue
	}
	if state.LastTestAt != 0 {
		view.LastTestAt = time.Unix(state.LastTestAt, 0)
	}
	if state.LastPublishAt != 0 {
		view.LastPublishAt = time.Unix(state.LastPublishAt, 0)
	}
	return view
}

func publishTargetViews(states []publishTargetState) []PublishTargetView {
	sort.Slice(states, func(i, j int) bool { return states[i].ID < states[j].ID })
	views := make([]PublishTargetView, 0, len(states))
	for _, state := range states {
		views = append(views, targetView(state))
	}
	return views
}

// ListPublishTargets 返回全部 SSH 在线发布目标。
func (s *Service) ListPublishTargets(ctx context.Context) ([]PublishTargetView, error) {
	var states []publishTargetState
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		states, err = loadPublishTargets(ctx, q)
		return err
	})
	return publishTargetViews(states), err
}

func validSSHHost(value string) bool {
	if net.ParseIP(value) != nil {
		return true
	}
	return len(value) <= 253 && hostnamePattern.MatchString(value) && !strings.Contains(value, "..")
}

func containsControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}

func normalizePublishTarget(input PublishTargetInput) (PublishTargetInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.SSHHost = strings.TrimSpace(input.SSHHost)
	input.SSHUser = strings.TrimSpace(input.SSHUser)
	input.HostKeyAlgorithm = strings.TrimSpace(input.HostKeyAlgorithm)
	input.HostKeyFingerprint = strings.TrimSpace(input.HostKeyFingerprint)
	input.Source = strings.TrimSpace(input.Source)
	if input.SSHPort == 0 {
		input.SSHPort = defaultSSHPort
	}
	if input.RemotePort == 0 {
		input.RemotePort = defaultSubRemotePort
	}
	switch {
	case input.Name == "" || len(input.Name) > 64:
		return input, &FieldError{Field: "name", Message: "must be between 1 and 64 characters"}
	case containsControl(input.Name):
		return input, &FieldError{Field: "name", Message: "must not contain control characters"}
	case !validSSHHost(input.SSHHost):
		return input, &FieldError{Field: "ssh_host", Message: "must be an IP address or hostname"}
	case input.SSHPort < 1 || input.SSHPort > 65535:
		return input, &FieldError{Field: "ssh_port", Message: "must be between 1 and 65535"}
	case !sshUserPattern.MatchString(input.SSHUser):
		return input, &FieldError{Field: "ssh_user", Message: "is not a valid SSH username"}
	case !subpublish.SupportedHostKeyType(input.HostKeyAlgorithm):
		return input, &FieldError{Field: "host_key_algorithm", Message: "must be ssh-ed25519, ecdsa-sha2-nistp256 or ssh-rsa"}
	case !hostFingerprintExpr.MatchString(input.HostKeyFingerprint):
		return input, &FieldError{Field: "host_key_fingerprint", Message: "must be an SHA256 SSH fingerprint"}
	case input.RemotePort < 1 || input.RemotePort > 65535:
		return input, &FieldError{Field: "remote_port", Message: "must be between 1 and 65535"}
	case !subscription.ValidSourceName(input.Source):
		return input, &FieldError{Field: "source", Message: "must use lowercase letters, numbers, underscores or hyphens"}
	}
	return input, nil
}

func (s *Service) validatePublishTargetSource(ctx context.Context, source string) error {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	if source != doc.Settings.Subscription.SourceName {
		return &FieldError{Field: "source", Message: "must match the current subscription source"}
	}
	return nil
}

func applyPublishTargetInput(state *publishTargetState, input PublishTargetInput) {
	state.Name, state.SSHHost, state.SSHPort, state.SSHUser = input.Name, input.SSHHost, input.SSHPort, input.SSHUser
	state.HostKeyAlgorithm, state.HostKeyFingerprint = input.HostKeyAlgorithm, input.HostKeyFingerprint
	state.RemotePort, state.Source, state.Enabled = input.RemotePort, input.Source, input.Enabled
}

func samePublishEndpoint(state publishTargetState, input PublishTargetInput) bool {
	return state.SSHHost == input.SSHHost && state.SSHPort == input.SSHPort && state.SSHUser == input.SSHUser &&
		state.HostKeyAlgorithm == input.HostKeyAlgorithm && state.HostKeyFingerprint == input.HostKeyFingerprint &&
		state.RemotePort == input.RemotePort && state.Source == input.Source
}

func samePublishTargetConfig(current, snapshot publishTargetState) bool {
	return current.ID == snapshot.ID && current.SSHHost == snapshot.SSHHost && current.SSHPort == snapshot.SSHPort &&
		current.SSHUser == snapshot.SSHUser && current.HostKeyAlgorithm == snapshot.HostKeyAlgorithm &&
		current.HostKeyFingerprint == snapshot.HostKeyFingerprint && current.RemotePort == snapshot.RemotePort &&
		current.Source == snapshot.Source && current.Secret == snapshot.Secret && current.Enabled == snapshot.Enabled
}

func ensureUniqueTargetName(targets []publishTargetState, id int64, name string) error {
	for _, target := range targets {
		if target.ID != id && strings.EqualFold(target.Name, name) {
			return &FieldError{Field: "name", Message: "already exists"}
		}
	}
	return nil
}

func nextPublishTargetID(targets []publishTargetState) int64 {
	var id int64
	for _, target := range targets {
		id = max(id, target.ID)
	}
	return id + 1
}

func (s *Service) targetSetup(ctx context.Context, state publishTargetState, secret string) (*PublishTargetSetup, error) {
	identity, err := s.SSHPublishIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if !identity.Configured {
		return nil, errors.New("SSH publish identity is not configured")
	}
	entry := fmt.Sprintf(`restrict,port-forwarding,command="/bin/false",permitopen="127.0.0.1:%d" %s proxystack-web`, state.RemotePort, identity.PublicKey)
	return &PublishTargetSetup{
		Target: targetView(state), Secret: secret, AuthorizedKeysEntry: entry,
		PublishTokenCommand: fmt.Sprintf("sudo proxystack-web sub source set-publish-token %s", state.Source),
		SSHDConfig:          fmt.Sprintf("Match User %s\n    AllowTcpForwarding local\n    PermitOpen 127.0.0.1:%d\n    ForceCommand /bin/false\n    PermitTTY no\n    X11Forwarding no\n    AllowAgentForwarding no\nMatch all", state.SSHUser, state.RemotePort),
	}, nil
}

// CreatePublishTarget 创建目标并生成只返回一次的发布 Secret。
func (s *Service) CreatePublishTarget(ctx context.Context, actor Actor, input PublishTargetInput) (*PublishTargetSetup, error) {
	input, err := normalizePublishTarget(input)
	if err != nil {
		return nil, err
	}
	if err := s.validatePublishTargetSource(ctx, input.Source); err != nil {
		return nil, err
	}
	if identity, err := s.SSHPublishIdentity(ctx); err != nil {
		return nil, err
	} else if !identity.Configured {
		return nil, &FieldError{Field: "ssh_host", Message: "generate the SSH publish identity first"}
	}
	secret := auth.RandomToken()
	var created publishTargetState
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		targets, err := loadPublishTargets(ctx, q)
		if err != nil {
			return err
		}
		if len(targets) >= maxPublishTargets {
			return &FieldError{Field: "name", Message: "too many publish targets"}
		}
		if err := ensureUniqueTargetName(targets, 0, input.Name); err != nil {
			return err
		}
		created = publishTargetState{ID: nextPublishTargetID(targets), Secret: secret}
		applyPublishTargetInput(&created, input)
		targets = append(targets, created)
		if err := store.PutJSON(ctx, q, settingSubPublishTargets, targets); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubPublishTarget, Object: fmt.Sprint(created.ID),
			Summary: "创建 SSH 发布目标 " + created.Name,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.targetSetup(ctx, created, secret)
}

// UpdatePublishTarget 修改目标连接信息，不改变发布 Secret。
func (s *Service) UpdatePublishTarget(ctx context.Context, actor Actor, id int64, input PublishTargetInput) (*PublishTargetView, error) {
	input, err := normalizePublishTarget(input)
	if err != nil {
		return nil, err
	}
	if err := s.validatePublishTargetSource(ctx, input.Source); err != nil {
		return nil, err
	}
	var updated publishTargetState
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		targets, err := loadPublishTargets(ctx, q)
		if err != nil {
			return err
		}
		if err := ensureUniqueTargetName(targets, id, input.Name); err != nil {
			return err
		}
		found := false
		for index := range targets {
			if targets[index].ID == id {
				endpointChanged := !samePublishEndpoint(targets[index], input)
				applyPublishTargetInput(&targets[index], input)
				if endpointChanged {
					targets[index].LastTestAt, targets[index].LastTestError = 0, ""
					targets[index].LastPublishAt, targets[index].LastSequence, targets[index].LastPublishError = 0, 0, ""
				}
				updated, found = targets[index], true
				break
			}
		}
		if !found {
			return ErrNotFound
		}
		if err := store.PutJSON(ctx, q, settingSubPublishTargets, targets); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubPublishTarget, Object: fmt.Sprint(id),
			Summary: "修改 SSH 发布目标 " + updated.Name,
		})
	})
	view := targetView(updated)
	return &view, err
}

// DeletePublishTarget 删除目标及其发布 Secret。
func (s *Service) DeletePublishTarget(ctx context.Context, actor Actor, id int64) error {
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		targets, err := loadPublishTargets(ctx, q)
		if err != nil {
			return err
		}
		kept := make([]publishTargetState, 0, len(targets))
		name := ""
		for _, target := range targets {
			if target.ID == id {
				name = target.Name
				continue
			}
			kept = append(kept, target)
		}
		if name == "" {
			return ErrNotFound
		}
		if err := store.PutJSON(ctx, q, settingSubPublishTargets, kept); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubPublishTarget, Object: fmt.Sprint(id),
			Summary: "删除 SSH 发布目标 " + name,
		})
	})
}

// RotatePublishTargetSecret 为目标生成新的发布 Secret；管理端立即改用新 Secret，订阅端更新后旧 Secret 才失效。
func (s *Service) RotatePublishTargetSecret(ctx context.Context, actor Actor, id int64) (*PublishTargetSetup, error) {
	secret := auth.RandomToken()
	var updated publishTargetState
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		targets, err := loadPublishTargets(ctx, q)
		if err != nil {
			return err
		}
		found := false
		for index := range targets {
			if targets[index].ID == id {
				targets[index].Secret, targets[index].LastTestAt, targets[index].LastTestError = secret, 0, ""
				updated, found = targets[index], true
				break
			}
		}
		if !found {
			return ErrNotFound
		}
		if err := store.PutJSON(ctx, q, settingSubPublishTargets, targets); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubPublishTarget, Object: fmt.Sprint(id),
			Summary: "轮换 SSH 发布目标 Secret " + updated.Name,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.targetSetup(ctx, updated, secret)
}

func (s *Service) publishTargetState(ctx context.Context, id int64) (sshIdentityState, publishTargetState, error) {
	var identity sshIdentityState
	var target publishTargetState
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		identity, err = s.loadSSHIdentity(ctx, q)
		if err != nil {
			return err
		}
		targets, err := loadPublishTargets(ctx, q)
		if err != nil {
			return err
		}
		for _, candidate := range targets {
			if candidate.ID == id {
				target = candidate
				return nil
			}
		}
		return ErrNotFound
	})
	if err == nil && len(identity.Seed) == 0 {
		err = errors.New("SSH publish identity is not configured")
	}
	return identity, target, err
}

func clientTarget(state publishTargetState) subpublish.Target {
	return subpublish.Target{
		SSHHost: state.SSHHost, SSHPort: state.SSHPort, SSHUser: state.SSHUser,
		HostKeyAlgorithm: state.HostKeyAlgorithm, HostKeyFingerprint: state.HostKeyFingerprint,
		RemotePort: state.RemotePort, Source: state.Source, Secret: state.Secret,
	}
}

// TestPublishTarget 分阶段测试 SSH、订阅端健康状态和发布 Secret。
func (s *Service) TestPublishTarget(ctx context.Context, actor Actor, id int64) (*subpublish.ProbeResult, error) {
	identity, target, err := s.publishTargetState(ctx, id)
	if err != nil {
		return nil, err
	}
	result, probeErr := s.SubPublisher.Probe(ctx, subpublish.Identity{Seed: identity.Seed}, clientTarget(target))
	errorText := ""
	if probeErr != nil {
		errorText = probeErr.Error()
	}
	updateErr := s.Store.InTx(context.WithoutCancel(ctx), func(q *dbq.Queries) error {
		targets, err := loadPublishTargets(context.WithoutCancel(ctx), q)
		if err != nil {
			return err
		}
		statusUpdated := false
		for index := range targets {
			if targets[index].ID == id && samePublishTargetConfig(targets[index], target) {
				targets[index].LastTestAt, targets[index].LastTestError = s.Now().Unix(), errorText
				statusUpdated = true
			}
		}
		if err := store.PutJSON(context.WithoutCancel(ctx), q, settingSubPublishTargets, targets); err != nil {
			return err
		}
		return audit.Write(context.WithoutCancel(ctx), q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubPublishTest, Object: fmt.Sprint(id),
			Summary: fmt.Sprintf("测试 SSH 发布目标 %s（成功=%t，状态更新=%t）", target.Name, probeErr == nil, statusUpdated),
		})
	})
	if updateErr != nil {
		return result, updateErr
	}
	return result, nil
}

func (s *Service) selectedPublishTargets(ctx context.Context, ids []int64) (sshIdentityState, []publishTargetState, error) {
	if len(ids) == 0 {
		return sshIdentityState{}, nil, &FieldError{Field: "target_ids", Message: "select at least one publish target"}
	}
	if len(ids) > maxPublishTargets {
		return sshIdentityState{}, nil, &FieldError{Field: "target_ids", Message: "too many publish targets"}
	}
	wanted := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || wanted[id] {
			return sshIdentityState{}, nil, &FieldError{Field: "target_ids", Message: "contains an invalid or duplicate target"}
		}
		wanted[id] = true
	}
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return sshIdentityState{}, nil, err
	}
	currentSource := doc.Settings.Subscription.SourceName
	var identity sshIdentityState
	selected := []publishTargetState{}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		identity, err = s.loadSSHIdentity(ctx, q)
		if err != nil {
			return err
		}
		targets, err := loadPublishTargets(ctx, q)
		if err != nil {
			return err
		}
		for _, target := range targets {
			if wanted[target.ID] {
				if !target.Enabled {
					return &FieldError{Field: "target_ids", Message: "contains a disabled target"}
				}
				if target.Source != currentSource {
					return &FieldError{Field: "target_ids", Message: "contains a target for a different subscription source"}
				}
				selected = append(selected, target)
			}
		}
		if len(selected) != len(wanted) {
			return &FieldError{Field: "target_ids", Message: "contains an unknown target"}
		}
		return nil
	})
	if err == nil && len(identity.Seed) == 0 {
		err = errors.New("SSH publish identity is not configured")
	}
	return identity, selected, err
}

type targetDelivery struct {
	target publishTargetState
	result *subpublish.DeliveryResult
	err    error
}

// PublishSubscriptionBundleOnline 签发一次订阅包，并在后台向所选目标投递同一份原始数据。
func (s *Service) PublishSubscriptionBundleOnline(ctx context.Context, actor Actor, targetIDs []int64) (*OnlinePublishStarted, error) {
	identity, targets, err := s.selectedPublishTargets(ctx, targetIDs)
	if err != nil {
		return nil, err
	}
	bundle, err := s.PublishSubscriptionBundle(ctx, actor)
	if err != nil {
		return nil, err
	}
	taskID, err := s.startTask("subscription-publish", fmt.Sprintf("发布订阅包 #%d", bundle.Sequence), actor.Name,
		func(ctx context.Context, logf Logf) error {
			logf("Publishing bundle #%d to %d target(s)", bundle.Sequence, len(targets))
			sem := make(chan struct{}, publishConcurrency)
			results := make(chan targetDelivery, len(targets))
			var wg sync.WaitGroup
			for _, target := range targets {
				target := target
				wg.Add(1)
				go func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					targetCtx, cancel := context.WithTimeout(ctx, publishTargetTimeout)
					defer cancel()
					result, err := s.SubPublisher.Deliver(targetCtx, subpublish.Identity{Seed: identity.Seed}, clientTarget(target), bundle.Data)
					if err == nil && (result == nil || result.Sequence != bundle.Sequence) {
						err = errors.New("subscription server returned an unexpected sequence")
					}
					results <- targetDelivery{target: target, result: result, err: err}
				}()
			}
			wg.Wait()
			close(results)

			deliveries := make([]targetDelivery, 0, len(targets))
			failures := 0
			for delivery := range results {
				deliveries = append(deliveries, delivery)
				if delivery.err != nil {
					failures++
					logf("Target %s failed: %s", delivery.target.Name, delivery.err.Error())
					continue
				}
				logf("Target %s: %s sequence #%d", delivery.target.Name, delivery.result.Status, delivery.result.Sequence)
			}
			staleTargets, err := s.saveDeliveryResults(context.WithoutCancel(ctx), actor, bundle.Sequence, deliveries)
			if err != nil {
				return err
			}
			for _, name := range staleTargets {
				logf("Target %s configuration changed; delivery status was not updated", name)
			}
			if failures > 0 {
				return fmt.Errorf("%d of %d publish targets failed", failures, len(targets))
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return &OnlinePublishStarted{TaskID: taskID, Sequence: bundle.Sequence}, nil
}

func (s *Service) saveDeliveryResults(ctx context.Context, actor Actor, sequence int64, deliveries []targetDelivery) ([]string, error) {
	staleTargets := []string{}
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		targets, err := loadPublishTargets(ctx, q)
		if err != nil {
			return err
		}
		byID := map[int64]targetDelivery{}
		for _, delivery := range deliveries {
			byID[delivery.target.ID] = delivery
		}
		for index := range targets {
			delivery, ok := byID[targets[index].ID]
			if !ok {
				continue
			}
			if !samePublishTargetConfig(targets[index], delivery.target) {
				staleTargets = append(staleTargets, delivery.target.Name)
				continue
			}
			targets[index].LastPublishAt = s.Now().Unix()
			if delivery.err != nil {
				targets[index].LastPublishError = delivery.err.Error()
			} else {
				targets[index].LastPublishError = ""
				targets[index].LastSequence = sequence
			}
		}
		if err := store.PutJSON(ctx, q, settingSubPublishTargets, targets); err != nil {
			return err
		}
		for _, delivery := range deliveries {
			summary := fmt.Sprintf("投递订阅包 #%d 到 %s（成功=%t）", sequence, delivery.target.Name, delivery.err == nil)
			if err := audit.Write(ctx, q, s.Now(), audit.Entry{
				Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubPublishDeliver, Object: fmt.Sprint(delivery.target.ID), Summary: summary,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return staleTargets, err
}
