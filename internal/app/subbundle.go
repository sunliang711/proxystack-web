package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/generator/sub"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/subscription"
)

// ActionSubscriptionPublish 是发布订阅包的审计动作。
const ActionSubscriptionPublish = "subscription.publish"

// 订阅包签名私钥和发布记录在 settings 表中的 key。
const (
	settingSigningKey = "sub_signing_key"
	settingPublish    = "sub_publish"
)

// publishState 是最近一次发布的记录。
type publishState struct {
	Sequence    int64  `json:"sequence"`
	PublishedAt int64  `json:"published_at"`
	ContentHash string `json:"content_hash"`
	PublishedBy string `json:"published_by"`
}

// BundleStatus 是「发布订阅包」页的数据。
type BundleStatus struct {
	Source        string    `json:"source"`
	PublicKey     string    `json:"public_key"`
	Fingerprint   string    `json:"fingerprint"`
	Sequence      int64     `json:"sequence" doc:"最近一次发布的序号，0 表示从未发布"`
	PublishedAt   time.Time `json:"published_at"`
	PublishedBy   string    `json:"published_by"`
	Pending       bool      `json:"pending" doc:"当前订阅内容与最近一次发布的不同"`
	Users         int       `json:"users"`
	Nodes         int       `json:"nodes"`
	PublicBaseURL string    `json:"public_base_url"`
	BuildWarnings []string  `json:"build_warnings"`
}

// signingKey 读取订阅包签名私钥，不存在时生成并保存。
func (s *Service) signingKey(ctx context.Context) (ed25519.PrivateKey, error) {
	var seed []byte
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		err := store.GetJSON(ctx, q, settingSigningKey, &seed)
		if errors.Is(err, store.ErrNotFound) {
			_, key, genErr := ed25519.GenerateKey(rand.Reader)
			if genErr != nil {
				return genErr
			}
			seed = key.Seed()
			return store.PutJSON(ctx, q, settingSigningKey, seed)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("stored subscription signing key is invalid")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func (s *Service) publishState(ctx context.Context, q *dbq.Queries) (publishState, error) {
	var state publishState
	err := store.GetJSON(ctx, q, settingPublish, &state)
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	return state, err
}

// bundleSnapshot 返回要发布的订阅数据：去掉停用用户的节点，这样其他来源导入时不会为停用用户提供节点。
func bundleSnapshot(data *subscription.Snapshot) *subscription.Snapshot {
	out := *data
	disabled := map[string]bool{}
	for _, user := range data.Users {
		if !user.Enabled {
			disabled[user.Name] = true
		}
	}
	out.Nodes = slices.DeleteFunc(append([]sub.Node{}, data.Nodes...), func(node sub.Node) bool { return disabled[node.User] })
	return &out
}

// SubscriptionBundleStatus 返回发布状态：来源名、公钥、最近一次发布和是否有未发布的变更。
func (s *Service) SubscriptionBundleStatus(ctx context.Context) (*BundleStatus, error) {
	key, err := s.signingKey(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.subscription(ctx)
	if err != nil {
		return nil, err
	}
	data := bundleSnapshot(snapshot.data)
	hash, err := subscription.ContentHash(data)
	if err != nil {
		return nil, err
	}
	var state publishState
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		state, err = s.publishState(ctx, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	public := key.Public().(ed25519.PublicKey)
	status := &BundleStatus{
		Source: data.Source, PublicKey: subscription.FormatPublicKey(public), Fingerprint: subscription.KeyFingerprint(public),
		Sequence: state.Sequence, PublishedBy: state.PublishedBy, Pending: state.ContentHash != hash,
		Users: len(data.Users), Nodes: len(data.Nodes), PublicBaseURL: data.Settings.PublicBaseURL,
		BuildWarnings: append([]string{}, snapshot.warnings...),
	}
	if state.PublishedAt != 0 {
		status.PublishedAt = time.Unix(state.PublishedAt, 0)
	}
	return status, nil
}

// PublishedBundle 是一次发布生成的订阅包。
type PublishedBundle struct {
	Filename string
	Data     []byte
	Sequence int64
}

// PublishSubscriptionBundle 用下一个序号签发订阅包，记录发布状态并写审计日志。
func (s *Service) PublishSubscriptionBundle(ctx context.Context, actor Actor) (*PublishedBundle, error) {
	key, err := s.signingKey(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.subscription(ctx)
	if err != nil {
		return nil, err
	}
	data := bundleSnapshot(snapshot.data)
	hash, err := subscription.ContentHash(data)
	if err != nil {
		return nil, err
	}
	var bundle *PublishedBundle
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		state, err := s.publishState(ctx, q)
		if err != nil {
			return err
		}
		now := s.Now()
		data.Sequence, data.GeneratedAt = state.Sequence+1, now.UTC()
		sealed, err := subscription.Seal(data, key)
		if err != nil {
			return err
		}
		state = publishState{Sequence: data.Sequence, PublishedAt: now.Unix(), ContentHash: hash, PublishedBy: actor.Name}
		if err := store.PutJSON(ctx, q, settingPublish, state); err != nil {
			return err
		}
		bundle = &PublishedBundle{
			Filename: fmt.Sprintf("sub-%s-%06d.bundle", data.Source, data.Sequence), Data: sealed, Sequence: data.Sequence,
		}
		return audit.Write(ctx, q, now, audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubscriptionPublish, Object: data.Source,
			Summary: fmt.Sprintf("发布订阅包 #%d（%d 个用户，%d 个节点）", data.Sequence, len(data.Users), len(data.Nodes)),
		})
	})
	if err != nil {
		return nil, err
	}
	return bundle, nil
}

// SubscriptionIdentity 返回本机的来源名和订阅包验签公钥，供安装脚本配置同机的订阅端。
func (s *Service) SubscriptionIdentity(ctx context.Context) (source, publicKey string, err error) {
	key, err := s.signingKey(ctx)
	if err != nil {
		return "", "", err
	}
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return "", "", err
	}
	return doc.Settings.Subscription.SourceName, subscription.FormatPublicKey(key.Public().(ed25519.PublicKey)), nil
}
