package api

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
)

// StaticFS 是前端构建产物的文件系统。
type StaticFS = fs.FS

// spaHandler 提供前端静态资源；不存在的路径回退到 index.html，并注入 <base href>。
type spaHandler struct {
	files fs.FS
	index []byte
}

const basePlaceholder = "<!--psw:base-->"

func newSPAHandler(files fs.FS, base string) http.Handler {
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		index = []byte("<!doctype html><title>proxystack-web</title><p>frontend not built</p>")
	}
	tag := `<base href="` + html.EscapeString(base) + `">`
	index = bytes.Replace(index, []byte(basePlaceholder), []byte(tag), 1)
	return &spaHandler{files: files, index: index}
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+chiWildcard(r)), "/")
	if name != "" && name != "index.html" {
		if stat, err := fs.Stat(h.files, name); err == nil && !stat.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, h.files, name)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(h.index)
}

func chiWildcard(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		return rctx.URLParam("*")
	}
	return r.URL.Path
}
