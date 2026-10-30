package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eagle/proxystack-web/internal/configdoc"
	"github.com/eagle/proxystack-web/internal/domain"
	"github.com/eagle/proxystack-web/internal/generator/sub"
	"github.com/eagle/proxystack-web/internal/store/dbq"
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
	key   string
	doc   *configdoc.Document
	index sub.Index
	// local 是本机 stack 节点（不含手工节点和远端节点），供节点导出使用。
	local []sub.Node
	// warnings 是构建时跳过的节点。
	warnings []string
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
	snapshot := &subSnapshot{doc: doc}

	// 本机节点：每个已应用的 stack 按它已应用的版本渲染，订阅与实际运行的配置一致；
	// 对外地址取当前设置。未应用过的 stack 没有节点。
	byRevision := map[int64][]string{}
	for _, row := range applied {
		byRevision[row.RevisionID] = append(byRevision[row.RevisionID], row.StackName)
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
			snapshot.warnings = append(snapshot.warnings, fmt.Sprintf("revision %d: %v", revision, err))
			continue
		}
		snapshotDoc.Settings.Basic.ExternalHost = doc.Settings.Basic.ExternalHost
		set, err := configdoc.Compile(snapshotDoc)
		if err != nil {
			snapshot.warnings = append(snapshot.warnings, fmt.Sprintf("revision %d: %v", revision, err))
			continue
		}
		set.Stacks = slices.DeleteFunc(set.Stacks, func(stack domain.Stack) bool { return !slices.Contains(stacks, stack.Name) })
		input, err := sub.RenderStackInputAt(set, "local", s.Now().UTC().Format(time.RFC3339))
		if err != nil {
			snapshot.warnings = append(snapshot.warnings, fmt.Sprintf("revision %d: %v", revision, err))
			continue
		}
		snapshot.local = append(snapshot.local, input.Nodes...)
	}
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
	sources := []string{"local"}
	for index, manual := range doc.ManualNodes {
		node, err := manual.SubNode(index)
		if err != nil {
			snapshot.warnings = append(snapshot.warnings, fmt.Sprintf("manual node %s: %v", manual.Remark, err))
			continue
		}
		nodes = append(nodes, node)
	}
	if len(doc.ManualNodes) > 0 {
		sources = append(sources, "manual")
	}
	nodes, sources = s.mergeRemoteNodes(doc, remoteStates, nodes, sources, &snapshot.warnings)
	index, err := sub.BuildIndex(nodes, sources, sub.Access{Type: "none"}, s.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	snapshot.index = index
	for _, warning := range snapshot.warnings {
		slog.Warn("subscription build", "warning", warning)
	}
	return snapshot, nil
}

// mergeRemoteNodes 合并远端节点源同步来的节点：只保留本机存在的用户的节点，
// 与已有节点 tag 或同用户代理名重复的跳过。
func (s *Service) mergeRemoteNodes(doc *configdoc.Document, states []dbq.RemoteSourceState, nodes []sub.Node, sources []string, warnings *[]string) ([]sub.Node, []string) {
	tags, names := map[string]bool{}, map[string]bool{}
	for _, node := range nodes {
		tags[node.Tag], names[node.User+"\x00"+node.Remark] = true, true
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
			if tags[node.Tag] || names[node.User+"\x00"+node.Remark] {
				*warnings = append(*warnings, fmt.Sprintf("remote source %s: skipped duplicate node %s", source.Name, node.Remark))
				continue
			}
			tags[node.Tag], names[node.User+"\x00"+node.Remark] = true, true
			nodes = append(nodes, node)
		}
		sources = append(sources, "remote:"+source.Name)
	}
	return nodes, sources
}

// SubscriptionStatus 是订阅监听器的状态。
type SubscriptionStatus struct {
	Enabled   bool   `json:"enabled"`
	Address   string `json:"address"`
	Listening bool   `json:"listening"`
	Error     string `json:"error"`
}

// ApplySubscriptionListener 让订阅监听器与当前设置一致。
func (s *Service) ApplySubscriptionListener(ctx context.Context) {
	if s.SubListener == nil {
		return
	}
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return
	}
	settings := doc.Settings.Subscription
	addr := net.JoinHostPort(settings.Listen, strconv.Itoa(settings.Port))
	status := SubscriptionStatus{Enabled: settings.Enabled, Address: addr}
	if err := s.SubListener.Apply(settings.Enabled, addr); err != nil {
		status.Error = err.Error()
		slog.Error("subscription listener", "addr", addr, "err", err)
	} else {
		status.Listening = settings.Enabled
	}
	s.subs.mu.Lock()
	s.subStatus = status
	s.subs.mu.Unlock()
}

// SubscriptionListenerStatus 返回订阅监听器的状态。
func (s *Service) SubscriptionListenerStatus() SubscriptionStatus {
	s.subs.mu.Lock()
	defer s.subs.mu.Unlock()
	return s.subStatus
}

// SubscriptionResponse 是一次订阅请求的结果。
type SubscriptionResponse struct {
	Status  int
	Body    []byte
	Headers map[string]string
	// Code 是错误时的 JSON 错误码（forbidden / not_found / template_error / index_unavailable）。
	Code string
}

// ServeSubscription 渲染 token 对应用户的订阅。requestURL 用于未设置对外地址时的 Surge managed 首行。
func (s *Service) ServeSubscription(ctx context.Context, token, format string, requestURL *url.URL) *SubscriptionResponse {
	snapshot, err := s.subscription(ctx)
	if err != nil {
		slog.Error("build subscription", "err", err)
		return &SubscriptionResponse{Status: http.StatusServiceUnavailable, Code: "index_unavailable"}
	}
	var user *configdoc.User
	for index := range snapshot.doc.Users {
		candidate := &snapshot.doc.Users[index]
		if subtle.ConstantTimeCompare([]byte(candidate.SubToken), []byte(token)) == 1 {
			user = candidate
		}
	}
	if user == nil || token == "" {
		return &SubscriptionResponse{Status: http.StatusForbidden, Code: "forbidden"}
	}
	settings := snapshot.doc.Settings.Subscription
	if !user.Enabled || userExpired(user, s.Now()) {
		if settings.DisabledResponse == configdoc.DisabledEmpty {
			return &SubscriptionResponse{Status: http.StatusOK, Body: []byte{}, Headers: subscriptionHeaders(user, format, settings)}
		}
		return &SubscriptionResponse{Status: http.StatusNotFound, Code: "not_found"}
	}
	if len(sub.NodesForUser(snapshot.index, user.Name)) == 0 {
		return &SubscriptionResponse{Status: http.StatusNotFound, Code: "not_found"}
	}
	managed := sub.SurgeManaged{Interval: settings.Interval, Strict: settings.Strict}
	if format == sub.FormatSurge && settings.SurgeManaged {
		managed.URL = s.subscriptionBaseURL(snapshot.doc, requestURL) + "/s/" + token + "/surge"
	}
	templates := snapshot.doc.Templates
	override := map[string]string{sub.FormatClash: templates.Clash, sub.FormatPremium: templates.Premium, sub.FormatSurge: templates.Surge}[format]
	body, err := sub.Render(format, snapshot.index, user.Name, override, managed)
	if err != nil {
		slog.Error("render subscription", "user", user.Name, "format", format, "err", err)
		var templateErr sub.TemplateError
		if errors.As(err, &templateErr) {
			return &SubscriptionResponse{Status: http.StatusServiceUnavailable, Code: "template_error"}
		}
		return &SubscriptionResponse{Status: http.StatusNotFound, Code: "not_found"}
	}
	return &SubscriptionResponse{Status: http.StatusOK, Body: []byte(body), Headers: subscriptionHeaders(user, format, settings)}
}

func userExpired(user *configdoc.User, now time.Time) bool {
	if user.ExpireAt == "" {
		return false
	}
	expire, err := time.Parse(time.RFC3339, user.ExpireAt)
	return err == nil && !now.Before(expire)
}

// subscriptionHeaders 返回客户端识别的响应头：文件名、更新间隔和额度信息。
func subscriptionHeaders(user *configdoc.User, format string, settings configdoc.SubscriptionSettings) map[string]string {
	extension := map[string]string{sub.FormatSurge: "conf"}[format]
	if extension == "" {
		extension = "yaml"
	}
	headers := map[string]string{
		"Content-Type":        "text/plain; charset=utf-8",
		"Content-Disposition": fmt.Sprintf("attachment; filename*=UTF-8''%s.%s", url.PathEscape(user.Name), extension),
		"Cache-Control":       "no-store",
	}
	if settings.Interval > 0 {
		hours := settings.Interval / 3600
		if hours < 1 {
			hours = 1
		}
		headers["Profile-Update-Interval"] = strconv.Itoa(hours)
	}
	info := []string{"upload=0", "download=0", "total=" + strconv.FormatInt(user.QuotaBytes, 10)}
	if user.ExpireAt != "" {
		if expire, err := time.Parse(time.RFC3339, user.ExpireAt); err == nil {
			info = append(info, "expire="+strconv.FormatInt(expire.Unix(), 10))
		}
	}
	headers["Subscription-Userinfo"] = strings.Join(info, "; ")
	return headers
}

// subscriptionBaseURL 返回订阅链接的前缀：优先用设置的对外地址，其次用请求地址，最后用 http://<对外地址>:<端口>。
func (s *Service) subscriptionBaseURL(doc *configdoc.Document, requestURL *url.URL) string {
	settings := doc.Settings.Subscription
	if settings.PublicBaseURL != "" {
		return strings.TrimSuffix(settings.PublicBaseURL, "/")
	}
	if requestURL != nil && requestURL.Host != "" {
		return requestURL.Scheme + "://" + requestURL.Host
	}
	host := doc.Settings.Basic.ExternalHost
	if host == "" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(settings.Port))
}

// SubscriptionLinks 是一个用户的三种订阅链接。
type SubscriptionLinks struct {
	BaseURL string            `json:"base_url"`
	Links   map[string]string `json:"links"`
	Nodes   int               `json:"nodes"`
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
	base := s.subscriptionBaseURL(snapshot.doc, nil)
	links := map[string]string{}
	for _, format := range []string{sub.FormatClash, sub.FormatPremium, sub.FormatSurge} {
		links[format] = base + "/s/" + user.SubToken + "/" + format
	}
	return &SubscriptionLinks{BaseURL: base, Links: links, Nodes: len(sub.NodesForUser(snapshot.index, user.Name))}, nil
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
	source := map[string]string{
		sub.FormatClash: snapshot.doc.Templates.Clash, sub.FormatPremium: snapshot.doc.Templates.Premium, sub.FormatSurge: snapshot.doc.Templates.Surge,
	}[format]
	if override != nil {
		source = *override
	}
	settings := snapshot.doc.Settings.Subscription
	managed := sub.SurgeManaged{Interval: settings.Interval, Strict: settings.Strict}
	if settings.SurgeManaged {
		managed.URL = s.subscriptionBaseURL(snapshot.doc, nil) + "/s/<token>/surge"
	}
	text, err := sub.Render(format, snapshot.index, snapshot.doc.Users[index].Name, source, managed)
	if err != nil {
		return "", &FieldError{Field: "content", Message: err.Error()}
	}
	return text, nil
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
	for _, user := range snapshot.doc.Users {
		enabled[user.Name] = user.Enabled && !userExpired(&user, s.Now())
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
