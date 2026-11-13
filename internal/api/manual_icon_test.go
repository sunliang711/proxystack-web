package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/api"
	"proxystack-web/internal/deps"
	"proxystack-web/internal/favicon"
)

var testPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x10\x00\x00\x00\x10\x08\x06\x00\x00\x00")

// upload 用 multipart 上传手动图标，返回状态码和响应体。
func (h *harness) upload(kind, name string, data []byte) (int, string) {
	h.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	require.NoError(h.t, form.WriteField("kind", kind))
	require.NoError(h.t, form.WriteField("name", name))
	file, err := form.CreateFormFile("file", "icon")
	require.NoError(h.t, err)
	_, _ = file.Write(data)
	require.NoError(h.t, form.Close())
	req, err := http.NewRequest(http.MethodPost, h.base+"/api/access/icons/upload", &body)
	require.NoError(h.t, err)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set(api.CSRFHeader, h.csrf)
	resp, err := h.client.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (h *harness) getIcon(query, etag string) (*http.Response, []byte) {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.base+"/api/access/favicon?"+query, nil)
	require.NoError(h.t, err)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := h.client.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func TestManualIconsAPI(t *testing.T) {
	guest, _ := newHarness(t)
	guest.mustDo(http.MethodGet, "/api/access/icons", nil, http.StatusUnauthorized)
	h := newAdminHarness(t)

	// 上传主域名图标（子域名先归并为主域名）和服务图标（SVG）。
	status, body := h.upload("domain", "www.Example.com", testPNG)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, `"name":"example.com"`)
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	status, body = h.upload("service", "Instagram", svg)
	require.Equal(t, http.StatusOK, status, body)
	status, body = h.upload("domain", "example.org", []byte("<html>not an image</html>"))
	require.Equal(t, http.StatusUnprocessableEntity, status, body)
	status, _ = h.upload("domain", "example.org", append(append([]byte{}, testPNG...), make([]byte, favicon.MaxIcon)...))
	require.NotEqual(t, http.StatusOK, status)
	status, _ = h.upload("site", "example.org", testPNG)
	require.Equal(t, http.StatusUnprocessableEntity, status)
	for _, name := range []string{"example.com:443", "a@b.com", "例子.中国"} {
		status, body = h.upload("domain", name, testPNG)
		require.Equal(t, http.StatusUnprocessableEntity, status, name+": "+body)
	}

	// 取图标：手动图标优先，带 ETag，If-None-Match 命中时返回 304；SVG 返回时带禁止执行的响应头。
	resp, data := h.getIcon("domain=example.com", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, testPNG, data)
	require.Equal(t, "private, max-age=86400", resp.Header.Get("Cache-Control"))
	etag := resp.Header.Get("ETag")
	require.NotEmpty(t, etag)
	resp, data = h.getIcon("domain=example.com", etag)
	require.Equal(t, http.StatusNotModified, resp.StatusCode)
	require.Empty(t, data)
	resp, _ = h.getIcon("service=instagram", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "image/svg+xml", resp.Header.Get("Content-Type"))
	require.Contains(t, resp.Header.Get("Content-Security-Policy"), "sandbox")
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	resp, _ = h.getIcon("service=youtube", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, _ = h.getIcon("domain=example.com&service=x", "")
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	// 替换后 ETag 变化。
	status, _ = h.upload("domain", "example.com", append(append([]byte{}, testPNG...), 1))
	require.Equal(t, http.StatusOK, status)
	resp, _ = h.getIcon("domain=example.com", etag)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 从网址获取：经下载代理，内网和非网页端口被拒绝。
	h.svc.FetchIconURL = func(_ context.Context, cfg deps.ProxyConfig, target string) (*favicon.Icon, error) {
		require.Equal(t, deps.ProxyDirect, cfg.Kind)
		if strings.Contains(target, "intranet") {
			return nil, favicon.ErrForbidden
		}
		return &favicon.Icon{ContentType: "image/png", Data: testPNG}, nil
	}
	h.mustDo(http.MethodPost, "/api/access/icons/fetch", map[string]string{"kind": "domain", "name": "github.com", "url": "https://cdn.example.com/gh.png"}, http.StatusOK)
	h.mustDo(http.MethodPost, "/api/access/icons/fetch", map[string]string{"kind": "domain", "name": "github.com", "url": "https://intranet.example.com/x.png"}, http.StatusUnprocessableEntity)

	list := h.mustDoList(http.MethodGet, "/api/access/icons")
	require.Len(t, list, 3)

	// 批量删除，返回实际删除的个数；删除后服务图标返回 404。
	body2 := h.mustDo(http.MethodPost, "/api/access/icons/delete", map[string]any{"items": []map[string]string{
		{"kind": "service", "name": "INSTAGRAM"}, {"kind": "domain", "name": "github.com"}, {"kind": "domain", "name": "missing.com"},
	}}, http.StatusOK)
	require.Equal(t, float64(2), body2["deleted"])
	resp, _ = h.getIcon("service=instagram", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Len(t, h.mustDoList(http.MethodGet, "/api/access/icons"), 1)

	audit := h.mustDo(http.MethodGet, "/api/audit?action=access.icon_delete", nil, http.StatusOK)
	require.NotEmpty(t, audit["items"])
}

func (h *harness) mustDoList(method, path string) []any {
	h.t.Helper()
	req, err := http.NewRequest(method, h.base+path, nil)
	require.NoError(h.t, err)
	resp, err := h.client.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	require.Equal(h.t, http.StatusOK, resp.StatusCode)
	var out []any
	raw, _ := io.ReadAll(resp.Body)
	require.NoError(h.t, json.Unmarshal(raw, &out))
	return out
}
