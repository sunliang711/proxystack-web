package subserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"proxystack-web/internal/generator/sub"
	"proxystack-web/internal/subscription"
)

var formats = map[string]bool{sub.FormatClash: true, sub.FormatPremium: true, sub.FormatSurge: true}

// 收件目录和数据目录的检查间隔；收件目录中的文件在最后一次修改后至少静置 settle 才导入，避免读到拷贝了一半的文件。
const (
	pollInterval = 2 * time.Second
	settle       = time.Second
)

// Server 是订阅端服务：持有当前可用的订阅数据，定期处理收件目录并在订阅包变化后重新加载。
type Server struct {
	Layout Layout
	Now    func() time.Time

	mu        sync.RWMutex
	config    Config
	view      *subscription.View
	sources   []SourceState
	lastError string
	signature string
}

// New 创建订阅端服务并加载配置和已导入的订阅包。
func New(layout Layout) (*Server, error) {
	config, err := LoadConfig(layout)
	if err != nil {
		return nil, err
	}
	s := &Server{Layout: layout, Now: time.Now, config: config}
	s.reload(config)
	return s, nil
}

// reload 重新读取订阅包并合并。主来源不可用时保留上一份可用数据，并记录错误。
func (s *Server) reload(config Config) {
	states := LoadSources(s.Layout, config)
	view, err := BuildView(states)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config, s.sources = config, states
	var problems []string
	for _, state := range states {
		if state.Error != "" {
			problems = append(problems, state.Name+": "+state.Error)
		}
	}
	if err != nil {
		problems = append(problems, err.Error())
	} else {
		s.view = view
		for _, warning := range view.Warnings {
			slog.Warn("subscription merge", "warning", warning)
		}
	}
	s.lastError = strings.Join(problems, "; ")
	if s.lastError != "" {
		slog.Error("subscription data", "err", s.lastError)
	}
	users, nodes := 0, 0
	if s.view != nil {
		users, nodes = len(s.view.Users), len(s.view.Index.Nodes)
	}
	slog.Info("subscription data loaded", "sources", len(states), "users", users, "nodes", nodes)
}

// Run 启动监听并处理收件目录，直到 ctx 结束。
func (s *Server) Run(ctx context.Context) error {
	s.mu.RLock()
	config := s.config
	s.mu.RUnlock()
	listener, err := net.Listen("tcp", config.Addr())
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("subscription server started", "addr", config.Addr(), "tls", config.TLSCert != "", "dir", s.Layout.Dir)
		if config.TLSCert != "" {
			errCh <- srv.ServeTLS(listener, config.TLSCert, config.TLSKey)
		} else {
			errCh <- srv.Serve(listener)
		}
	}()
	s.signature = s.dataSignature()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-errCh:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return srv.Shutdown(shutdownCtx)
		case <-ticker.C:
			s.poll(config)
		}
	}
}

// poll 导入收件目录中的订阅包，并在配置或数据目录变化后重新加载。
func (s *Server) poll(running Config) {
	s.processInbox()
	signature := s.dataSignature()
	if signature == s.signature {
		return
	}
	s.signature = signature
	config, err := LoadConfig(s.Layout)
	if err != nil {
		slog.Error("reload config", "err", err)
		s.mu.Lock()
		s.lastError = err.Error()
		s.mu.Unlock()
		return
	}
	if config.Addr() != running.Addr() || config.TLSCert != running.TLSCert || config.TLSKey != running.TLSKey {
		slog.Warn("listen address or certificate changed; restart proxystack-sub to apply")
	}
	s.reload(config)
}

// dataSignature 用配置文件和订阅包的修改时间与大小判断是否需要重新加载。
func (s *Server) dataSignature() string {
	var parts []string
	files := []string{s.Layout.ConfigPath()}
	matches, _ := filepath.Glob(filepath.Join(s.Layout.DataDir(), "*.bundle"))
	files = append(files, matches...)
	for _, name := range files {
		if info, err := os.Stat(name); err == nil {
			parts = append(parts, fmt.Sprintf("%s@%d/%d", name, info.ModTime().UnixNano(), info.Size()))
		}
	}
	return strings.Join(parts, "|")
}

// processInbox 导入收件目录中静置足够久的文件：成功后删除，失败的移到 rejected 并记录原因。
func (s *Server) processInbox() {
	entries, err := os.ReadDir(s.Layout.InboxDir())
	if err != nil {
		return
	}
	s.mu.RLock()
	config := s.config
	s.mu.RUnlock()
	// 配置可能刚被修改（例如新加来源后立刻放入该来源的订阅包），导入前重新读取。
	if fresh, err := LoadConfig(s.Layout); err == nil {
		config = fresh
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(s.Layout.InboxDir(), entry.Name())
		info, err := entry.Info()
		if err != nil || s.now().Sub(info.ModTime()) < settle {
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil {
			var result *ImportResult
			if result, err = Import(s.Layout, config, data, false); err == nil {
				slog.Info("bundle imported", "file", entry.Name(), "source", result.Snapshot.Source,
					"sequence", result.Snapshot.Sequence, "previous", result.Previous)
				_ = os.Remove(path)
				continue
			}
		}
		slog.Error("bundle rejected", "file", entry.Name(), "err", err)
		if mkErr := os.MkdirAll(s.Layout.RejectedDir(), 0o750); mkErr == nil {
			_ = os.WriteFile(filepath.Join(s.Layout.RejectedDir(), entry.Name()+".error"), []byte(err.Error()+"\n"), 0o640)
			_ = os.Rename(path, filepath.Join(s.Layout.RejectedDir(), entry.Name()))
		}
	}
}

func (s *Server) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Health 是 /health 的响应。不含用户名和 token。
type Health struct {
	Status string         `json:"status"`
	Users  int            `json:"users"`
	Nodes  int            `json:"nodes"`
	Source []SourceHealth `json:"sources"`
	Error  string         `json:"error,omitempty"`
	// Warnings 是合并时跳过的节点。
	Warnings []string `json:"warnings,omitempty"`
}

// SourceHealth 是一个来源的导入状态。
type SourceHealth struct {
	Name        string    `json:"name"`
	Primary     bool      `json:"primary"`
	Sequence    int64     `json:"sequence"`
	GeneratedAt time.Time `json:"generated_at,omitzero"`
	Error       string    `json:"error,omitempty"`
}

// Health 返回当前状态：有可用数据且没有错误时为 ok，有可用数据但存在错误时为 degraded，没有可用数据时为 unavailable。
func (s *Server) Health() Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	health := Health{Status: "ok", Source: []SourceHealth{}, Error: s.lastError}
	for _, state := range s.sources {
		item := SourceHealth{Name: state.Name, Primary: state.Primary, Error: state.Error}
		if state.Snapshot != nil {
			item.Sequence, item.GeneratedAt = state.Snapshot.Sequence, state.Snapshot.GeneratedAt
		}
		health.Source = append(health.Source, item)
	}
	switch {
	case s.view == nil:
		health.Status = "unavailable"
	case s.lastError != "":
		health.Status = "degraded"
	}
	if s.view != nil {
		health.Users, health.Nodes, health.Warnings = len(s.view.Users), len(s.view.Index.Nodes), s.view.Warnings
	}
	return health
}

// Handler 返回订阅端路由。日志中不记录 token。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		health := s.Health()
		status := http.StatusOK
		if health.Status == "unavailable" {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, health)
	})
	mux.HandleFunc("GET /s/{token}/{format}", func(w http.ResponseWriter, r *http.Request) {
		format := r.PathValue("format")
		if !formats[format] {
			writeError(w, http.StatusNotFound, "not_found", "unknown subscription format")
			return
		}
		s.mu.RLock()
		view := s.view
		s.mu.RUnlock()
		if view == nil {
			writeError(w, http.StatusServiceUnavailable, "index_unavailable", "subscription is not available")
			return
		}
		requestURL := &url.URL{Scheme: "http", Host: r.Host}
		if r.TLS != nil {
			requestURL.Scheme = "https"
		}
		result := view.Serve(r.PathValue("token"), format, requestURL, s.now())
		if result.Code != "" {
			writeError(w, result.Status, result.Code, map[string]string{
				"forbidden": "invalid subscription token", "not_found": "subscription not found",
				"template_error": "subscription template is not available",
			}[result.Code])
			return
		}
		for key, value := range result.Headers {
			w.Header().Set(key, value)
		}
		w.WriteHeader(result.Status)
		_, _ = w.Write(result.Body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// Refresh 立即处理收件目录并重新加载配置和订阅包。
func (s *Server) Refresh() {
	s.processInbox()
	config, err := LoadConfig(s.Layout)
	if err != nil {
		s.mu.Lock()
		s.lastError = err.Error()
		s.mu.Unlock()
		return
	}
	s.reload(config)
}
