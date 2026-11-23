package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/mihomoapi"
	"proxystack-web/internal/runtime"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/units"
)

const providerUpdateTimeout = 45 * time.Second

// ProviderView combines saved scheduling settings with applied runtime metadata.
type ProviderView struct {
	ID            int64             `json:"id"`
	Name          string            `json:"name"`
	Running       bool              `json:"running"`
	Applied       bool              `json:"applied"`
	Pending       bool              `json:"pending"`
	Updating      bool              `json:"updating"`
	NodeCount     int               `json:"node_count"`
	Nodes         []mihomoapi.Proxy `json:"nodes"`
	LastAttemptAt int64             `json:"last_attempt_at"`
	LastSuccessAt int64             `json:"last_success_at"`
	LastError     string            `json:"last_error"`
}

// providerFlight coalesces concurrent manual and scheduled updates of the same provider.
type providerFlight struct {
	done chan struct{}
	err  error
}

// providerUpdates tracks in-flight work and the scheduler's bounded background pass.
type providerUpdates struct {
	mu       sync.Mutex
	flights  map[int64]*providerFlight
	ticking  bool
	stopping bool
	wg       sync.WaitGroup
}

// appliedProviderConfig is decoded from the generated file, never from pending credentials.
type appliedProviderConfig struct {
	Controller string                    `yaml:"external-controller"`
	Secret     string                    `yaml:"secret"`
	Providers  map[string]map[string]any `yaml:"proxy-providers"`
	Proxies    []map[string]any          `yaml:"proxies"`
}

// providerRuntime reads applied controller credentials and a fingerprint of the generated configuration.
func (s *Service) providerRuntime(stack configdoc.Stack) (appliedProviderConfig, string, error) {
	var applied appliedProviderConfig
	content, err := os.ReadFile(s.Layout.GeneratedMihomo(stack.Name))
	if errors.Is(err, os.ErrNotExist) {
		return applied, "", nil
	}
	if err != nil {
		return applied, "", err
	}
	if err := yaml.Unmarshal(content, &applied); err != nil {
		return applied, "", fmt.Errorf("cannot read applied Mihomo configuration")
	}
	return applied, runtime.Hash(content), nil
}

// providerClient uses the credentials that the running core actually received.
func providerClient(applied appliedProviderConfig) mihomoapi.Client {
	return mihomoapi.Client{Base: "http://" + applied.Controller, Secret: applied.Secret, HTTP: &http.Client{Timeout: providerUpdateTimeout}}
}

// ListStackProviders exposes names and status without publishing subscription URLs or node credentials.
func (s *Service) ListStackProviders(ctx context.Context, stackID int64) ([]ProviderView, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	stack, err := stackByID(doc, stackID)
	if err != nil {
		return nil, err
	}
	if err := s.lockProviderRuntime(ctx); err != nil {
		return nil, err
	}
	applied, _, err := s.providerRuntime(*stack)
	s.runtimeMu.Unlock()
	if err != nil {
		return nil, err
	}
	states, err := s.Units.States(ctx, []string{units.Name(units.Mihomo, stack.Name)})
	if err != nil {
		return nil, err
	}
	running := states[units.Name(units.Mihomo, stack.Name)].Running()
	var rows []dbq.MihomoProviderState
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) { rows, err = q.ListMihomoProviderState(ctx); return err })
	if err != nil {
		return nil, err
	}
	stored := map[int64]dbq.MihomoProviderState{}
	for _, row := range rows {
		stored[row.ProviderID] = row
	}
	native := map[string]mihomoapi.Provider{}
	var controllerErr error
	if running && applied.Controller != "" && len(stack.Mihomo.Providers) > 0 {
		native, controllerErr = providerClient(applied).Providers(ctx)
	}
	// Compare provider fields and the private download node; unrelated rule edits do not hide status.
	desired := appliedProviderConfig{}
	if rendered, renderErr := runtime.Render(doc, nil); renderErr == nil {
		if err := yaml.Unmarshal(rendered.Files[stack.Name].Mihomo, &desired); err != nil {
			return nil, err
		}
	}
	out := make([]ProviderView, 0, len(stack.Mihomo.Providers))
	for _, source := range stack.Mihomo.Providers {
		name := source.RuntimeName()
		config, exists := applied.Providers[name]
		row := stored[source.ID]
		if row.ConfigHash != providerFingerprint(applied, name) {
			row = dbq.MihomoProviderState{}
		}
		view := ProviderView{ID: source.ID, Name: source.Name, Running: running, Applied: exists, Nodes: []mihomoapi.Proxy{},
			Pending:       !exists || !providerConfigEqual(config, desired.Providers[name]) || !providerConfigEqual(downloadNode(applied), downloadNode(desired)),
			LastAttemptAt: row.LastAttemptAt, LastSuccessAt: row.LastSuccessAt, LastError: row.LastError}
		if status, ok := native[name]; ok {
			view.Nodes = append(view.Nodes, status.Proxies...)
			view.NodeCount = len(view.Nodes)
			for index := range view.Nodes {
				view.Nodes[index].Name = source.Name + " | " + strings.TrimPrefix(view.Nodes[index].Name, name+" | ")
			}
			if view.NodeCount == 0 && view.LastError == "" {
				view.LastError = "subscription has no usable nodes; try a manual refresh"
			}
			if updated := status.UpdatedAt.Unix(); !status.UpdatedAt.IsZero() && updated > view.LastSuccessAt {
				view.LastSuccessAt = updated
			}
		} else if controllerErr != nil {
			view.LastError = controllerErr.Error()
		} else if running && exists {
			view.LastError = "provider is not initialized; check Mihomo logs"
		}
		s.providerUpdates.mu.Lock()
		view.Updating = s.providerUpdates.flights[source.ID] != nil
		s.providerUpdates.mu.Unlock()
		out = append(out, view)
	}
	return out, nil
}

// providerConfigEqual compares deterministic JSON instead of map iteration order.
func providerConfigEqual(a, b map[string]any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && string(left) == string(right)
}

// providerFingerprint invalidates stale status when a URL, filter or download proxy changes.
func providerFingerprint(config appliedProviderConfig, name string) string {
	data, err := json.Marshal([]any{config.Providers[name], downloadNode(config)})
	if err != nil {
		return ""
	}
	return runtime.Hash(data)
}

// downloadNode returns only the reserved proxy, which must never enter a business group.
func downloadNode(config appliedProviderConfig) map[string]any {
	for _, node := range config.Proxies {
		if node["name"] == configdoc.ProviderDownloadProxy {
			return node
		}
	}
	return nil
}

// UpdateStackProvider refreshes an applied collection, sharing work with concurrent callers.
func (s *Service) UpdateStackProvider(ctx context.Context, actor Actor, stackID, providerID int64) error {
	// Verify ownership before joining a flight so a foreign stack cannot observe another update.
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	stack, err := stackByID(doc, stackID)
	if err != nil {
		return err
	}
	found := false
	for _, provider := range stack.Mihomo.Providers {
		found = found || provider.ID == providerID
	}
	if !found {
		return ErrNotFound
	}
	s.providerUpdates.mu.Lock()
	if s.providerUpdates.stopping {
		s.providerUpdates.mu.Unlock()
		return context.Canceled
	}
	if flight := s.providerUpdates.flights[providerID]; flight != nil {
		s.providerUpdates.mu.Unlock()
		select {
		case <-flight.done:
			return flight.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.providerUpdates.flights == nil {
		s.providerUpdates.flights = map[int64]*providerFlight{}
	}
	flight := &providerFlight{done: make(chan struct{})}
	s.providerUpdates.flights[providerID] = flight
	s.providerUpdates.wg.Add(1)
	s.providerUpdates.mu.Unlock()
	defer func() {
		defer s.providerUpdates.wg.Done()
		s.providerUpdates.mu.Lock()
		delete(s.providerUpdates.flights, providerID)
		close(flight.done)
		s.providerUpdates.mu.Unlock()
	}()
	flight.err = s.updateStackProvider(ctx, actor, stackID, providerID)
	return flight.err
}

// updateStackProvider serializes core refreshes with apply and records only sanitized results.
func (s *Service) updateStackProvider(ctx context.Context, actor Actor, stackID, providerID int64) error {
	ctx, cancel := context.WithTimeout(ctx, providerUpdateTimeout)
	defer cancel()
	if err := s.lockProviderRuntime(ctx); err != nil {
		return err
	}
	defer s.runtimeMu.Unlock()
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	stack, err := stackByID(doc, stackID)
	if err != nil {
		return err
	}
	var source *configdoc.Provider
	for index := range stack.Mihomo.Providers {
		if stack.Mihomo.Providers[index].ID == providerID {
			source = &stack.Mihomo.Providers[index]
		}
	}
	if source == nil {
		return ErrNotFound
	}
	if err := s.requireRunning(ctx, stack.Name); err != nil {
		return err
	}
	applied, _, err := s.providerRuntime(*stack)
	if err != nil {
		return err
	}
	if _, exists := applied.Providers[source.RuntimeName()]; !exists || applied.Controller == "" {
		return &FieldError{Field: "provider", Message: "save and apply this provider before updating"}
	}
	hash := providerFingerprint(applied, source.RuntimeName())
	previousSuccess := int64(0)
	if native, readErr := providerClient(applied).Providers(ctx); readErr == nil {
		if status := native[source.RuntimeName()]; !status.UpdatedAt.IsZero() {
			previousSuccess = status.UpdatedAt.Unix()
		}
	}
	started := s.Now().Unix()
	updateErr := providerClient(applied).UpdateProvider(ctx, source.RuntimeName())
	var syncedDirectNodes map[string]configdoc.RawMapping
	appliedProviderCfg := applied.Providers[source.RuntimeName()]
	if updateErr == nil && appliedProviderCfg != nil {
		if native, nativeErr := providerClient(applied).Providers(ctx); nativeErr == nil {
			if nativeProvider, ok := native[source.RuntimeName()]; ok && len(nativeProvider.Proxies) > 0 {
				if configs, cacheErr := readProviderNodeCache(s.Layout.MihomoWorkDir(stack.Name), appliedProviderCfg); cacheErr == nil {
					candidate := make(map[string]configdoc.RawMapping, len(nativeProvider.Proxies))
					for _, node := range nativeProvider.Proxies {
						key := strings.TrimPrefix(node.Name, source.RuntimeName()+" | ")
						rawMap, ok := configs[key]
						if !ok {
							continue
						}
						mapping, err := rawMappingFromMap(rawMap)
						if err != nil || configdoc.CheckDirectRawConfig(mapping) != nil {
							continue
						}
						candidate[key] = mapping
					}
					if len(candidate) > 0 {
						syncedDirectNodes = candidate
					}
				}
			}
		}
	}
	finish, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	directNodesChanged := false
	err = s.Store.InTx(finish, func(q *dbq.Queries) error {
		// Configuration saves can delete a provider while the core is downloading.
		providers, err := q.ListMihomoProviders(finish)
		if err != nil {
			return err
		}
		exists := false
		for _, provider := range providers {
			exists = exists || (provider.ID == providerID && provider.StackID == stackID)
		}
		if !exists {
			return nil
		}
		states, err := q.ListMihomoProviderState(finish)
		if err != nil {
			return err
		}
		state := dbq.PutMihomoProviderStateParams{ProviderID: providerID, ConfigHash: hash, LastAttemptAt: started, LastSuccessAt: previousSuccess}
		for _, previous := range states {
			if previous.ProviderID == providerID && previous.ConfigHash == hash {
				state.LastSuccessAt = max(state.LastSuccessAt, previous.LastSuccessAt)
			}
		}
		if updateErr != nil {
			state.LastError = updateErr.Error()
		} else {
			state.LastSuccessAt = s.Now().Unix()
		}
		if err := q.PutMihomoProviderState(finish, state); err != nil {
			return err
		}
		if err := audit.Write(finish, q, s.Now(), audit.Entry{Actor: actor.Name, SourceIP: actor.IP, Action: "runtime.provider_update", Object: fmt.Sprintf("provider:%d", providerID), Summary: "Refresh Mihomo upstream subscription"}); err != nil {
			return err
		}
		if syncedDirectNodes != nil {
			latestDoc, err := store.LoadDocument(finish, q)
			if err != nil {
				return err
			}
			synced, syncErr := syncProviderDirectNodes(latestDoc, stackID, providerID, appliedProviderCfg, syncedDirectNodes)
			if syncErr != nil {
				slog.Warn("sync provider direct nodes", "stack_id", stackID, "provider_id", providerID, "err", syncErr)
				if err := audit.Write(finish, q, s.Now(), audit.Entry{Actor: actor.Name, SourceIP: actor.IP, Action: ActionConfigStack, Object: "stack:" + stack.Name, Summary: "同步上游订阅直连节点失败：" + syncErr.Error()}); err != nil {
					return err
				}
			} else if synced {
				if err := store.SaveDocument(finish, q, latestDoc); err != nil {
					return err
				}
				summary := "同步上游订阅直连节点 " + source.Name
				if err := s.writeRevision(finish, q, actor, summary, latestDoc); err != nil {
					return err
				}
				if err := audit.Write(finish, q, s.Now(), audit.Entry{Actor: actor.Name, SourceIP: actor.IP, Action: ActionConfigStack, Object: "stack:" + stack.Name, Summary: summary}); err != nil {
					return err
				}
				directNodesChanged = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if directNodesChanged {
		s.kickAutoPublish()
	}
	return updateErr
}

// providerMatchesApplied checks whether the saved provider URL and filters match the applied Mihomo config.
func providerMatchesApplied(provider configdoc.Provider, appliedCfg map[string]any) bool {
	if appliedCfg == nil {
		return false
	}
	appliedURL, _ := appliedCfg["url"].(string)
	appliedFilter, _ := appliedCfg["filter"].(string)
	appliedExclude, _ := appliedCfg["exclude-filter"].(string)
	return provider.URL == appliedURL && provider.Filter == appliedFilter && provider.ExcludeFilter == appliedExclude
}

// syncProviderDirectNodes removes missing/renamed nodes and refreshes configurations against the fetched subscription.
func syncProviderDirectNodes(doc *configdoc.Document, stackID, providerID int64, appliedCfg map[string]any, active map[string]configdoc.RawMapping) (bool, error) {
	stackIdx := doc.StackByID(stackID)
	if stackIdx < 0 {
		return false, nil
	}
	stack := &doc.Stacks[stackIdx]
	pIdx := slices.IndexFunc(stack.Mihomo.Providers, func(p configdoc.Provider) bool { return p.ID == providerID })
	if pIdx < 0 {
		return false, nil
	}
	provider := &stack.Mihomo.Providers[pIdx]
	if !providerMatchesApplied(*provider, appliedCfg) {
		return false, nil
	}
	if len(provider.DirectNodes) == 0 {
		return false, nil
	}
	changed := false
	kept := make([]configdoc.ProviderDirectNode, 0, len(provider.DirectNodes))
	for _, dn := range provider.DirectNodes {
		freshCfg, ok := active[dn.Name]
		if !ok {
			changed = true
			continue
		}
		if !rawMappingEqual(dn.Config, freshCfg) {
			dn.Config = freshCfg
			changed = true
		}
		kept = append(kept, dn)
	}
	if !changed {
		return false, nil
	}
	provider.DirectNodes = kept
	doc.Normalize()
	if issues := configdoc.Validate(doc); len(issues) > 0 {
		return false, fmt.Errorf("validation failed: %s", issues.Error())
	}
	return true, nil
}

func rawMappingEqual(a, b configdoc.RawMapping) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && string(left) == string(right)
}

// providerTick runs at most one scheduler pass and respects cancellation during shutdown.
func (s *Service) providerTick(ctx context.Context) {
	s.providerUpdates.mu.Lock()
	if s.providerUpdates.ticking || s.providerUpdates.stopping || ctx.Err() != nil {
		s.providerUpdates.mu.Unlock()
		return
	}
	s.providerUpdates.ticking = true
	s.providerUpdates.wg.Add(1)
	s.scheduler.wg.Add(1)
	s.providerUpdates.mu.Unlock()
	go func() {
		defer s.providerUpdates.wg.Done()
		defer s.scheduler.wg.Done()
		defer func() { s.providerUpdates.mu.Lock(); s.providerUpdates.ticking = false; s.providerUpdates.mu.Unlock() }()
		// Avoid loading the full user/stack document when no subscription is scheduled.
		var sources []dbq.MihomoProvider
		err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) { sources, err = q.ListMihomoProviders(ctx); return err })
		if err != nil {
			return
		}
		wanted := false
		for _, row := range sources {
			var provider configdoc.Provider
			if err := json.Unmarshal([]byte(row.Config), &provider); err == nil && provider.AutoUpdate {
				wanted = true
			}
		}
		if !wanted {
			return
		}
		doc, err := s.LoadConfig(ctx)
		if err != nil {
			return
		}
		for _, stack := range doc.Stacks {
			if ctx.Err() != nil {
				return
			}
			wanted := false
			for _, provider := range stack.Mihomo.Providers {
				wanted = wanted || provider.AutoUpdate
			}
			if !wanted || !stack.Enabled || !stack.Mihomo.Enabled || !stack.DesiredRunning {
				continue
			}
			views, err := s.ListStackProviders(ctx, stack.ID)
			if err != nil {
				continue
			}
			for _, provider := range stack.Mihomo.Providers {
				for _, view := range views {
					last := max(view.LastAttemptAt, view.LastSuccessAt)
					if provider.ID != view.ID || !provider.AutoUpdate || !view.Running || !view.Applied || view.Updating || s.Now().Unix()-last < int64(provider.Interval) {
						continue
					}
					// Failures are persisted by the shared update flow; never log remote credentials.
					_ = s.UpdateStackProvider(ctx, SystemActor, stack.ID, provider.ID)
				}
			}
		}
	}()
}

// lockProviderRuntime lets shutdown and request deadlines interrupt waits behind long-running applies.
func (s *Service) lockProviderRuntime(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.runtimeMu.TryLock() {
		return nil
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := ctx.Err(); err != nil {
				return err
			}
			if s.runtimeMu.TryLock() {
				return nil
			}
		}
	}
}

// stopProviderUpdates closes admission before waiting, without changing existing manual job shutdown semantics.
func (s *Service) stopProviderUpdates() {
	s.providerUpdates.mu.Lock()
	s.providerUpdates.stopping = true
	s.providerUpdates.mu.Unlock()
	s.providerUpdates.wg.Wait()
}
