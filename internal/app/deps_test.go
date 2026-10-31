package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	goruntime "runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/deps"
	"proxystack-web/internal/units"
)

// fakeXray 是一个冒充 xray 的脚本：version 输出版本号，run -test 总是通过。
func fakeXray(version string) string {
	return fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then echo 'Xray %s (Xray, Penetrates Everything.)'; fi\nexit 0\n", version)
}

// fakeGitHub 提供 xray 的 latest 发布信息、zip 资产和 .dgst 摘要。
func fakeGitHub(t *testing.T, version string) {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	w, err := writer.Create("xray")
	require.NoError(t, err)
	_, _ = w.Write([]byte(fakeXray(version)))
	require.NoError(t, writer.Close())
	archive := buf.Bytes()
	platform := map[string]string{"darwin/arm64": "macos-arm64-v8a", "darwin/amd64": "macos-64", "linux/amd64": "linux-64", "linux/arm64": "linux-arm64-v8a"}[goruntime.GOOS+"/"+goruntime.GOARCH]
	name := "Xray-" + platform + ".zip"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/XTLS/Xray-core/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v" + version,
				"assets": []map[string]any{
					{"name": name, "browser_download_url": server.URL + "/dl/" + name},
					{"name": name + ".dgst", "browser_download_url": server.URL + "/dl/" + name + ".dgst"},
				},
			})
		case "/dl/" + name:
			_, _ = w.Write(archive)
		case "/dl/" + name + ".dgst":
			_, _ = fmt.Fprintf(w, "MD5= 00\nSHA2-256= %s\n", deps.SHA256(archive))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	previous := deps.GitHubAPI
	deps.GitHubAPI = server.URL
	t.Cleanup(func() { deps.GitHubAPI = previous })
}

// TestInstallDepReplacesAndRollsBack 验证安装新版本后按顺序重启使用它的服务；新版本启动失败时自动回滚。
func TestInstallDepReplacesAndRollsBack(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeHarness(t)
	id, err := h.svc.Apply(ctx, h.actor, nil)
	require.NoError(t, err)
	require.Equal(t, app.TaskSucceeded, h.waitTask(t, id).Status)

	fakeGitHub(t, "9.1.0")
	id, err = h.svc.InstallDep(ctx, h.actor, app.InstallRequest{Component: deps.Xray, Restart: app.RestartSequential})
	require.NoError(t, err)
	task := h.waitTask(t, id)
	require.Equal(t, app.TaskSucceeded, task.Status, task.Error, task.Log)
	require.Contains(t, h.units.Calls, "restart "+units.Name(units.Xray, "usa1"))

	status, err := h.svc.DepsStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, "v9.1.0", status[0].Version)
	require.False(t, status[0].Previous)

	// 第二次安装的新版本让 usa2 的 xray 起不来：回滚到 9.1.0。
	fakeGitHub(t, "9.2.0")
	h.units.FailStart[units.Name(units.Xray, "usa2")] = true
	id, err = h.svc.InstallDep(ctx, h.actor, app.InstallRequest{Component: deps.Xray, Restart: app.RestartSequential})
	require.NoError(t, err)
	task = h.waitTask(t, id)
	require.Equal(t, app.TaskFailed, task.Status)
	require.Contains(t, task.Error, "rolled back")

	data, err := os.ReadFile(h.svc.DepFiles(deps.Xray)[deps.Xray])
	require.NoError(t, err)
	require.Contains(t, string(data), "9.1.0")
	status, err = h.svc.DepsStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, "rolled_back", status[0].History[0].Result)
	require.Equal(t, "succeeded", status[0].History[1].Result)
}
