package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/domain"
	"proxystack-web/internal/generator/sub"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/subscription"
)

// 节点导出文件的标识。
const (
	NodesSchema  = "proxystack-web.nodes"
	NodesVersion = 1
)

// NodesExport 是节点导出接口的响应。
type NodesExport struct {
	Schema      string     `json:"schema"`
	Version     int        `json:"version"`
	GeneratedAt time.Time  `json:"generated_at"`
	Nodes       []sub.Node `json:"nodes"`
}

// subSnapshot 是一次构建的订阅数据：当前配置加上各 stack 已应用版本渲染出的节点。
type subSnapshot struct {
	key string
	doc *configdoc.Document
	// data 是全部用户和节点（含停用用户的节点，供预览）；发布订阅包时再去掉停用用户的节点。
	data *subscription.Snapshot
	view *subscription.View
	// local 是本机 stack 节点（不含手工节点和远端节点），供节点导出使用。
	local []sub.Node
	// warnings 是构建时跳过的节点。
	warnings []string
	// applied 是各 stack 已应用的版本，skipped 是构建时整体跳过的版本及原因，
	// manualSkipped 是各用户无法生成的手工节点；三者用于说明用户为什么没有节点。
	applied       map[string]int64
	skipped       map[int64]string
	manualSkipped map[string][]string
}

type subCache struct {
	mu       sync.Mutex
	snapshot *subSnapshot
}

// subscriptionKey 用最新配置版本、各 stack 已应用版本和远端同步时间判断缓存是否仍然有效。
func (s *Service) subscriptionKey(ctx context.Context) (string, error) {
	var parts []string
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		revision, err := latestRevision(ctx, q)
		if err != nil {
			return err
		}
		parts = append(parts, strconv.FormatInt(revision, 10))
		applied, err := q.ListAppliedState(ctx)
		if err != nil {
			return err
		}
		for _, row := range applied {
			parts = append(parts, row.StackName+"@"+strconv.FormatInt(row.RevisionID, 10))
		}
		states, err := q.ListRemoteSourceStates(ctx)
		if err != nil {
			return err
		}
		for _, row := range states {
			parts = append(parts, fmt.Sprintf("r%d@%d", row.SourceID, row.LastSyncAt))
		}
		return nil
	})
	return strings.Join(parts, "|"), err
}

// subscription 返回缓存的订阅数据，配置、应用状态或远端节点变化后重建。
func (s *Service) subscription(ctx context.Context) (*subSnapshot, error) {
	key, err := s.subscriptionKey(ctx)
	if err != nil {
		return nil, err
	}
	s.subs.mu.Lock()
	defer s.subs.mu.Unlock()
	if s.subs.snapshot != nil && s.subs.snapshot.key == key {
		return s.subs.snapshot, nil
	}
	snapshot, err := s.buildSubscription(ctx)
	if err != nil {
		return nil, err
	}
	snapshot.key = key
	s.subs.snapshot = snapshot
	return snapshot, nil
}

func (s *Service) buildSubscription(ctx context.Context) (*subSnapshot, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	var applied []dbq.AppliedState
	var remoteStates []dbq.RemoteSourceState
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if applied, err = q.ListAppliedState(ctx); err != nil {
			return err
		}
		remoteStates, err = q.ListRemoteSourceStates(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	snapshot := &subSnapshot{doc: doc, applied: map[string]int64{}, skipped: map[int64]string{}, manualSkipped: map[string][]string{}}
	skip := func(revision int64, err error) {
		snapshot.warnings = append(snapshot.warnings, fmt.Sprintf("revision %d: %v", revision, err))
		snapshot.skipped[revision] = err.Error()
	}

	// 本机节点：每个已应用的 stack 按它已应用的版本渲染，订阅与实际运行的配置一致；
	// 对外地址取当前设置。未应用过的 stack 没有节点。
	byRevision := map[int64][]string{}
	for _, row := range applied {
		byRevision[row.RevisionID] = append(byRevision[row.RevisionID], row.StackName)
		snapshot.applied[row.StackName] = row.RevisionID
	}
	revisions := make([]int64, 0, len(byRevision))
	for revision := range byRevision {
		revisions = append(revisions, revision)
	}
	slices.Sort(revisions)
	for _, revision := range revisions {
		stacks := byRevision[revision]
		snapshotDoc, err := s.RevisionSnapshot(ctx, revision)
		if err != nil {
			skip(revision, err)
			continue
		}
		appliedNamesDoc := snapshotDoc.Clone()
		configdoc.OverlaySubscriptionNames(snapshotDoc, doc)
		render := func(candidate *configdoc.Document) (sub.Input, error) {
			candidate.Settings.Basic.ExternalHost = doc.Settings.Basic.ExternalHost
			set, err := configdoc.Compile(candidate)
			if err != nil {
				return sub.Input{}, err
			}
			set.Stacks = slices.DeleteFunc(set.Stacks, func(stack domain.Stack) bool { return !slices.Contains(stacks, stack.Name) })
			return sub.RenderStackInputAt(set, "local", s.Now().UTC().Format(time.RFC3339))
		}
		input, err := render(snapshotDoc)
		if err != nil {
			overlayErr := err
			input, err = render(appliedNamesDoc)
			if err != nil {
				skip(revision, err)
				continue
			}
			snapshot.warnings = append(snapshot.warnings, fmt.Sprintf("revision %d: current subscription naming overlay rejected; using applied names: %v", revision, overlayErr))
		}
		snapshot.local = append(snapshot.local, input.Nodes...)
	}
	// 直连不依赖本机进程，始终按当前授权生成，撤销授权无需重新应用运行配置。
	direct, err := configdoc.StackDirectNodes(doc)
	if err != nil {
		return nil, err
	}
	snapshot.local = append(snapshot.local, direct...)
	// 手动暂停：用户关闭时不提供订阅（由响应规则处理），逐 stack 暂停时去掉该 stack 的节点。
	paused := map[string][]string{}
	for _, user := range doc.Users {
		paused[user.Name] = user.PausedStacks
	}
	snapshot.local = slices.DeleteFunc(snapshot.local, func(node sub.Node) bool {
		stack, _, _ := strings.Cut(node.ID, ":")
		return slices.Contains(paused[node.User], stack)
	})

	nodes := append([]sub.Node{}, snapshot.local...)
	for index, manual := range doc.ManualNodes {
		node, err := manual.SubNode(index)
		if err != nil {
			snapshot.warnings = append(snapshot.warnings, fmt.Sprintf("manual node %s: %v", manual.Remark, err))
			snapshot.manualSkipped[manual.User] = append(snapshot.manualSkipped[manual.User], fmt.Sprintf("手工节点 %s 无法生成：%v", manual.Remark, err))
			continue
		}
		nodes = append(nodes, node)
	}
	nodes = s.mergeRemoteNodes(doc, remoteStates, nodes, &snapshot.warnings)
	snapshot.data = subscriptionData(doc, nodes, s.Now())
	if snapshot.view, err = subscription.Merge(snapshot.data, nil); err != nil {
		return nil, err
	}
	for _, warning := range snapshot.warnings {
		slog.Warn("subscription build", "warning", warning)
	}
	return snapshot, nil
}

// mergeRemoteNodes 合并远端节点源同步来的节点：只保留本机存在的用户的节点，
// 与同用户已有节点代理名重复的跳过。tag 不参与判重，不同机器使用默认端口时 tag 相同是正常情况。
func (s *Service) mergeRemoteNodes(doc *configdoc.Document, states []dbq.RemoteSourceState, nodes []sub.Node, warnings *[]string) []sub.Node {
	names := map[string]bool{}
	for _, node := range nodes {
		names[node.User+"\x00"+node.Remark] = true
	}
	stateByID := map[int64]dbq.RemoteSourceState{}
	for _, state := range states {
		stateByID[state.SourceID] = state
	}
	for _, source := range doc.RemoteSources {
		state, ok := stateByID[source.ID]
		if !source.Enabled || !ok {
			continue
		}
		var remote []sub.Node
		if err := json.Unmarshal([]byte(state.Nodes), &remote); err != nil {
			*warnings = append(*warnings, fmt.Sprintf("remote source %s: %v", source.Name, err))
			continue
		}
		for _, node := range remote {
			if doc.UserByName(node.User) < 0 {
				continue
			}
			node.ID = "remote:" + source.Name + ":" + node.ID
			if names[node.User+"\x00"+node.Remark] {
				*warnings = append(*warnings, fmt.Sprintf("remote source %s: skipped node %q of user %s, the name is already used", source.Name, node.Remark, node.User))
				continue
			}
			names[node.User+"\x00"+node.Remark] = true
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// subscriptionData 把配置中的用户和订阅设置连同节点组成本机的订阅数据。
func subscriptionData(doc *configdoc.Document, nodes []sub.Node, now time.Time) *subscription.Snapshot {
	settings := doc.Settings.Subscription
	data := &subscription.Snapshot{
		Source: settings.SourceName, GeneratedAt: now.UTC(),
		Settings: subscription.Settings{
			PublicBaseURL: settings.PublicBaseURL, SurgeManaged: settings.SurgeManaged, Interval: settings.Interval,
			Strict: settings.Strict, DisabledResponse: settings.DisabledResponse,
		},
		Templates: subscription.Templates{Clash: doc.Templates.Clash, Premium: doc.Templates.Premium, Surge: doc.Templates.Surge},
		Users:     []subscription.User{},
		Nodes:     nodes,
	}
	for _, user := range doc.Users {
		data.Users = append(data.Users, subscription.User{
			Name: user.Name, Token: user.SubToken, Enabled: user.Enabled, ExpireAt: user.ExpireAt,
		})
	}
	return data
}

// SubscriptionLinks 是一个用户的三种订阅链接。BaseURL 为空（未设置订阅端对外地址）时链接只有路径部分。
type SubscriptionLinks struct {
	BaseURL string            `json:"base_url"`
	Links   map[string]string `json:"links"`
	Nodes   int               `json:"nodes"`
	// NoNodesReason 在没有节点时说明原因，订阅端此时对该用户返回 not_found。
	NoNodesReason string `json:"no_nodes_reason,omitempty"`
}

// UserSubscriptionStatus 是用户当前订阅的节点情况，不含 token。
type UserSubscriptionStatus struct {
	Nodes int `json:"nodes"`
	// UnappliedStacks 是入站已引用该用户、但已应用的版本里还没有该用户节点的 stack，应用后节点才进入订阅。
	UnappliedStacks []string `json:"unapplied_stacks"`
	// NoNodesReason 在没有节点时说明原因。
	NoNodesReason string `json:"no_nodes_reason,omitempty"`
	// AutoPublish 为 true 时订阅内容变化后会自动发布到在线订阅端，不需要手动发布订阅包。
	AutoPublish bool `json:"auto_publish"`
}

// UserSubscriptionIssue 是发布订阅包前需要注意的用户：启用中但没有节点，或有 stack 尚未应用。
type UserSubscriptionIssue struct {
	User            string   `json:"user"`
	Nodes           int      `json:"nodes"`
	UnappliedStacks []string `json:"unapplied_stacks"`
	NoNodesReason   string   `json:"no_nodes_reason,omitempty"`
}

// userStatus 汇总某个用户的节点数、未应用的 stack 和没有节点的原因。
func (snapshot *subSnapshot) userStatus(user configdoc.User) UserSubscriptionStatus {
	status := UserSubscriptionStatus{
		Nodes:           len(sub.NodesForUser(snapshot.view.Index, user.Name)),
		UnappliedStacks: snapshot.unappliedStacks(user),
	}
	if status.Nodes == 0 {
		status.NoNodesReason = snapshot.explainNoNodes(user)
	}
	return status
}

// userIssues 列出启用且未到期、但没有节点或有 stack 尚未应用的用户。
func (snapshot *subSnapshot) userIssues(now time.Time) []UserSubscriptionIssue {
	issues := []UserSubscriptionIssue{}
	for _, user := range snapshot.data.Users {
		if !user.Enabled || user.Expired(now) {
			continue
		}
		index := snapshot.doc.UserByName(user.Name)
		if index < 0 {
			continue
		}
		status := snapshot.userStatus(snapshot.doc.Users[index])
		if status.Nodes == 0 || len(status.UnappliedStacks) > 0 {
			issues = append(issues, UserSubscriptionIssue{
				User: user.Name, Nodes: status.Nodes, UnappliedStacks: status.UnappliedStacks, NoNodesReason: status.NoNodesReason,
			})
		}
	}
	return issues
}

// UserSubscriptionStatus 返回用户当前订阅的节点情况，供用户页提示需要应用的 stack。
func (s *Service) UserSubscriptionStatus(ctx context.Context, id int64) (*UserSubscriptionStatus, error) {
	snapshot, err := s.subscription(ctx)
	if err != nil {
		return nil, err
	}
	index := snapshot.doc.UserByID(id)
	if index < 0 {
		return nil, ErrNotFound
	}
	status := snapshot.userStatus(snapshot.doc.Users[index])
	status.AutoPublish = s.autoPublishTargetCount(ctx) > 0 && snapshot.autoPublishBlocked() == ""
	return &status, nil
}

// UserSubscriptionLinks 返回用户的订阅链接（含 token），调用方负责记入审计。
func (s *Service) UserSubscriptionLinks(ctx context.Context, id int64) (*SubscriptionLinks, error) {
	snapshot, err := s.subscription(ctx)
	if err != nil {
		return nil, err
	}
	index := snapshot.doc.UserByID(id)
	if index < 0 {
		return nil, ErrNotFound
	}
	user := snapshot.doc.Users[index]
	base := snapshot.view.BaseURL(nil)
	links := map[string]string{}
	for _, format := range []string{sub.FormatClash, sub.FormatPremium, sub.FormatSurge} {
		links[format] = base + subscription.Path(user.Name, user.SubToken, format)
	}
	out := &SubscriptionLinks{BaseURL: base, Links: links, Nodes: len(sub.NodesForUser(snapshot.view.Index, user.Name))}
	if out.Nodes == 0 {
		out.NoNodesReason = snapshot.explainNoNodes(user)
	}
	return out, nil
}

// PreviewSubscription 用给定模板（为空时使用当前模板）渲染某个用户的订阅，供模板页和用户页预览。
func (s *Service) PreviewSubscription(ctx context.Context, userID int64, format string, override *string) (string, error) {
	snapshot, err := s.subscription(ctx)
	if err != nil {
		return "", err
	}
	index := snapshot.doc.UserByID(userID)
	if index < 0 {
		return "", ErrNotFound
	}
	user := snapshot.doc.Users[index]
	if !slices.ContainsFunc(snapshot.data.Nodes, func(node sub.Node) bool { return node.User == user.Name }) {
		return "", &FieldError{Field: "user_id", Message: snapshot.explainNoNodes(user)}
	}
	source := snapshot.view.Templates
	template := map[string]string{sub.FormatClash: source.Clash, sub.FormatPremium: source.Premium, sub.FormatSurge: source.Surge}[format]
	if override != nil {
		template = *override
	}
	base := snapshot.view.BaseURL(nil)
	if base == "" {
		base = "http://<订阅端地址>"
	}
	text, err := snapshot.view.Render(snapshot.doc.Users[index].Name, format, template, base+subscription.Path(user.Name, "<token>", sub.FormatSurge))
	if err != nil {
		return "", &FieldError{Field: "content", Message: err.Error()}
	}
	return text, nil
}

// explainNoNodes 说明用户在订阅中没有任何节点的原因：逐个检查把该用户列入订阅入站的 stack，
// 以及该用户无法生成的手工节点。
func (snapshot *subSnapshot) explainNoNodes(user configdoc.User) string {
	var reasons []string
	for _, stack := range snapshot.doc.Stacks {
		if slices.Contains(stack.DirectUsers, user.Name) {
			switch {
			case !stack.Enabled:
				reasons = append(reasons, fmt.Sprintf("stack %s 已停用，未提供直连节点", stack.Name))
			case slices.Contains(user.PausedStacks, stack.Name):
				reasons = append(reasons, fmt.Sprintf("该用户在 stack %s 上已暂停", stack.Name))
			case !slices.ContainsFunc(stack.Mihomo.Upstreams, func(upstream configdoc.Upstream) bool { return upstream.Type == "raw" }):
				reasons = append(reasons, fmt.Sprintf("stack %s 没有可直连的外部上游", stack.Name))
			}
		}
		if !servesUser(stack, user.Name) {
			continue
		}
		revision, applied := snapshot.applied[stack.Name]
		switch {
		case !stack.Enabled || !stack.Xray.Enabled:
			reasons = append(reasons, fmt.Sprintf("stack %s 已停用或未启用 Xray", stack.Name))
		case slices.Contains(user.PausedStacks, stack.Name):
			reasons = append(reasons, fmt.Sprintf("该用户在 stack %s 上已暂停", stack.Name))
		case !applied:
			reasons = append(reasons, fmt.Sprintf("stack %s 还没有应用过，应用后才会生成节点", stack.Name))
		case snapshot.skipped[revision] != "":
			reason := snapshot.skipped[revision]
			if strings.Contains(reason, "node.server is required") {
				reason += "（入站没有填 server，也没有设置「系统设置 → 基本 → 对外地址」；补上后重新应用）"
			}
			reasons = append(reasons, fmt.Sprintf("stack %s 已应用的版本 #%d 在构建订阅时被跳过：%s", stack.Name, revision, reason))
		default:
			reasons = append(reasons, fmt.Sprintf("stack %s 已应用的版本 #%d 里还没有该用户，修改入站后需要重新应用", stack.Name, revision))
		}
	}
	reasons = append(reasons, snapshot.manualSkipped[user.Name]...)
	if len(reasons) == 0 {
		return fmt.Sprintf("用户 %s 不在任何进入订阅的入站中：在 stack 的「入站」里把该用户加入用户列表并把「进入订阅」设为是，或在「上游」中授权直连，或在「手工节点」中为该用户添加节点", user.Name)
	}
	return fmt.Sprintf("用户 %s 目前没有订阅节点：%s", user.Name, strings.Join(reasons, "；"))
}

// servesUser 判断 stack 是否有进入订阅且引用该用户的入站。
func servesUser(stack configdoc.Stack, user string) bool {
	return slices.ContainsFunc(stack.Xray.Inbounds, func(inbound configdoc.Inbound) bool {
		return servesUserIn(inbound, user)
	})
}

func servesUserIn(inbound configdoc.Inbound, user string) bool {
	return inbound.Sub && slices.ContainsFunc(inbound.Users, func(ref configdoc.UserRef) bool { return ref.User == user })
}

// unappliedStacks 返回入站已引用该用户、但已应用的版本里还没有该用户节点的 stack：
// 从未应用过，或者用户是在最近一次应用之后才加入入站的。停用、暂停和构建时被跳过的 stack 另有原因，不在其中。
func (snapshot *subSnapshot) unappliedStacks(user configdoc.User) []string {
	out := []string{}
	for _, stack := range snapshot.doc.Stacks {
		if !stack.Enabled || !stack.Xray.Enabled || slices.Contains(user.PausedStacks, stack.Name) {
			continue
		}
		revision, applied := snapshot.applied[stack.Name]
		if applied && snapshot.skipped[revision] != "" {
			continue
		}
		missing := slices.ContainsFunc(stack.Xray.Inbounds, func(inbound configdoc.Inbound) bool {
			if !servesUserIn(inbound, user.Name) {
				return false
			}
			prefix := stack.Name + ":" + inbound.Name + ":"
			return !applied || !slices.ContainsFunc(snapshot.local, func(node sub.Node) bool {
				return node.User == user.Name && strings.HasPrefix(node.ID, prefix)
			})
		})
		if missing {
			out = append(out, stack.Name)
		}
	}
	return out
}

// ExportNodes 返回本机 stack 节点，供其他实例的远端节点源拉取。token 不匹配或未开启时返回 false。
func (s *Service) ExportNodes(ctx context.Context, token string) (*NodesExport, bool, error) {
	snapshot, err := s.subscription(ctx)
	if err != nil {
		return nil, false, err
	}
	export := snapshot.doc.Settings.NodeExport
	if !export.Enabled || export.Token == "" || subtle.ConstantTimeCompare([]byte(export.Token), []byte(token)) != 1 {
		return nil, false, nil
	}
	enabled := map[string]bool{}
	for _, user := range snapshot.data.Users {
		enabled[user.Name] = user.Enabled && !user.Expired(s.Now())
	}
	nodes := []sub.Node{}
	for _, node := range snapshot.local {
		if enabled[node.User] {
			nodes = append(nodes, node)
		}
	}
	return &NodesExport{Schema: NodesSchema, Version: NodesVersion, GeneratedAt: s.Now().UTC(), Nodes: nodes}, true, nil
}

// RemoteSourceStatus 是远端节点源的同步状态。
type RemoteSourceStatus struct {
	ID         int64     `json:"id"`
	LastSyncAt time.Time `json:"last_sync_at"`
	LastError  string    `json:"last_error"`
	NodeCount  int64     `json:"node_count"`
}

// RemoteSourceStatuses 返回全部远端节点源的同步状态。
func (s *Service) RemoteSourceStatuses(ctx context.Context) (map[int64]RemoteSourceStatus, error) {
	out := map[int64]RemoteSourceStatus{}
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListRemoteSourceStates(ctx)
		for _, row := range rows {
			out[row.SourceID] = RemoteSourceStatus{
				ID: row.SourceID, LastSyncAt: time.Unix(row.LastSyncAt, 0), LastError: row.LastError, NodeCount: row.NodeCount,
			}
		}
		return err
	})
	return out, err
}

// SyncRemoteSources 拉取启用的远端节点源；only 非 0 时只同步该源。失败时保留上次成功的节点并记录错误。
func (s *Service) SyncRemoteSources(ctx context.Context, only int64) error {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	var failures []string
	for _, source := range doc.RemoteSources {
		if (only != 0 && source.ID != only) || (only == 0 && !source.Enabled) {
			continue
		}
		nodes, fetchErr := fetchRemoteNodes(ctx, client, source)
		now := s.Now().Unix()
		err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
			if fetchErr != nil {
				return q.SetRemoteSourceError(ctx, dbq.SetRemoteSourceErrorParams{SourceID: source.ID, LastSyncAt: now, LastError: fetchErr.Error()})
			}
			data, err := json.Marshal(nodes)
			if err != nil {
				return err
			}
			return q.PutRemoteSourceState(ctx, dbq.PutRemoteSourceStateParams{
				SourceID: source.ID, LastSyncAt: now, LastError: "", NodeCount: int64(len(nodes)), Nodes: string(data),
			})
		})
		switch {
		case err != nil:
			failures = append(failures, source.Name+": "+err.Error())
		case fetchErr != nil:
			failures = append(failures, source.Name+": "+fetchErr.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func fetchRemoteNodes(ctx context.Context, client *http.Client, source configdoc.RemoteSource) ([]sub.Node, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+source.Token)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote returned %s", response.Status)
	}
	var export NodesExport
	decoder := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 16<<20))
	if err := decoder.Decode(&export); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	if export.Schema != NodesSchema || export.Version != NodesVersion {
		return nil, fmt.Errorf("the remote is not a proxystack-web node export")
	}
	for _, node := range export.Nodes {
		if err := node.Validate(); err != nil {
			return nil, fmt.Errorf("remote node %s: %w", node.ID, err)
		}
	}
	sort.SliceStable(export.Nodes, func(i, j int) bool { return export.Nodes[i].ID < export.Nodes[j].ID })
	return export.Nodes, nil
}
