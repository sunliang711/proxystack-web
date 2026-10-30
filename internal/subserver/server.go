// Package subserver 是订阅监听器：与管理端分开的端口，提供 /s/<token>/<格式>、节点导出接口和健康检查。
package subserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/eagle/proxystack-web/internal/app"
	"github.com/eagle/proxystack-web/internal/generator/sub"
)

var formats = map[string]bool{sub.FormatClash: true, sub.FormatPremium: true, sub.FormatSurge: true}

// Handler 返回订阅监听器的路由。日志中不记录 token。
func Handler(svc *app.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /s/{token}/{format}", func(w http.ResponseWriter, r *http.Request) {
		format := r.PathValue("format")
		if !formats[format] {
			writeError(w, http.StatusNotFound, "not_found", "unknown subscription format")
			return
		}
		requestURL := &url.URL{Scheme: "http", Host: r.Host}
		if r.TLS != nil {
			requestURL.Scheme = "https"
		}
		result := svc.ServeSubscription(r.Context(), r.PathValue("token"), format, requestURL)
		if result.Code != "" {
			writeError(w, result.Status, result.Code, map[string]string{
				"forbidden": "invalid subscription token", "not_found": "subscription not found",
				"template_error": "subscription template is not available", "index_unavailable": "subscription is not available",
			}[result.Code])
			return
		}
		for key, value := range result.Headers {
			w.Header().Set(key, value)
		}
		w.WriteHeader(result.Status)
		_, _ = w.Write(result.Body)
	})
	mux.HandleFunc("GET /export/nodes", func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "a bearer token is required")
			return
		}
		export, allowed, err := svc.ExportNodes(r.Context(), token)
		switch {
		case err != nil:
			slog.Error("export nodes", "err", err)
			writeError(w, http.StatusServiceUnavailable, "index_unavailable", "nodes are not available")
		case !allowed:
			writeError(w, http.StatusForbidden, "forbidden", "invalid token or node export is disabled")
		default:
			writeJSON(w, http.StatusOK, export)
		}
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

// Manager 按订阅设置启动、重新绑定或停止订阅监听器。
type Manager struct {
	Handler http.Handler
	mu      sync.Mutex
	srv     *http.Server
	addr    string
}

// Apply 让监听器与设置一致：关闭时停止，地址变化时重新绑定。绑定失败返回错误，原监听器保持不变。
func (m *Manager) Apply(enabled bool, addr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !enabled {
		m.stopLocked()
		return nil
	}
	if m.srv != nil && m.addr == addr {
		return nil
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	m.stopLocked()
	srv := &http.Server{Handler: m.Handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	m.srv, m.addr = srv, addr
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("subscription listener stopped", "addr", addr, "err", err)
		}
	}()
	slog.Info("subscription listener started", "addr", addr)
	return nil
}

func (m *Manager) stopLocked() {
	if m.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.srv.Shutdown(ctx)
	slog.Info("subscription listener stopped", "addr", m.addr)
	m.srv, m.addr = nil, ""
}

// Close 停止监听器。
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}
