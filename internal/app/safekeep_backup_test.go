package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
)

type safekeepUpload struct {
	apiKey   string
	sha      string
	query    map[string]string
	filename string
	data     []byte
}

// TestSafekeepBackupJob 验证 SafeKeep 备份任务：未配置地址或 API Key 时跳过；未设口令时明文导出、设口令时加密；
// API Key 读取时打码、原样提交占位时保留；路径和上游随请求发送；上游失败或接口报错记为失败，转入后台记为成功。
func TestSafekeepBackupJob(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	var mu sync.Mutex
	var uploads []safekeepUpload
	reply := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"task":{"id":"t1","status":"succeeded","items":[{"upstream_name":"s3","remote_path":"proxy/a.yaml","status":"succeeded"}]}}`)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/backups", r.URL.Path)
		file, header, err := r.FormFile("file")
		require.NoError(t, err)
		data, err := io.ReadAll(file)
		require.NoError(t, err)
		upload := safekeepUpload{apiKey: r.Header.Get("X-API-Key"), sha: r.Header.Get("X-Content-SHA256"), filename: header.Filename, data: data, query: map[string]string{}}
		for key := range r.URL.Query() {
			upload.query[key] = r.URL.Query().Get(key)
		}
		mu.Lock()
		uploads = append(uploads, upload)
		handler := reply
		mu.Unlock()
		handler(w)
	}))
	defer server.Close()

	runOnce := func(want string) app.JobRun {
		t.Helper()
		job := jobNamed(t, h, app.JobSafekeep)
		require.NoError(t, h.svc.RunJobNow(ctx, h.actor, job.ID))
		h.svc.WaitJobs()
		last := jobNamed(t, h, app.JobSafekeep).LastRun
		require.NotNil(t, last)
		require.Equal(t, want, last.Status, last.Log)
		return *last
	}
	update := func(params app.JobParams) error {
		job := jobNamed(t, h, app.JobSafekeep)
		return h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: job.Enabled, Timeout: job.Timeout, Params: params})
	}

	job := jobNamed(t, h, app.JobSafekeep)
	require.Equal(t, "0 5 * * *", job.Schedule)
	require.Contains(t, runOnce(app.RunSkipped).Log, "未配置 SafeKeep 地址或 API Key")

	require.Error(t, update(app.JobParams{SafekeepURL: "ftp://example.com", SafekeepAPIKey: "sk_x"}))
	require.Error(t, update(app.JobParams{SafekeepURL: server.URL, SafekeepAPIKey: "sk_x", Passphrase: "short"}))
	require.NoError(t, update(app.JobParams{
		SafekeepURL: " " + server.URL + "/ ", SafekeepAPIKey: "sk_test", SafekeepPath: "/proxy/ ", SafekeepTargets: []string{" s3 ", "", "dropbox"},
	}))
	job = jobNamed(t, h, app.JobSafekeep)
	require.Equal(t, configdoc.MaskedValue, job.Params.SafekeepAPIKey)
	require.Equal(t, server.URL+"/", job.Params.SafekeepURL)
	require.Equal(t, "proxy", job.Params.SafekeepPath)
	require.Equal(t, []string{"s3", "dropbox"}, job.Params.SafekeepTargets)
	require.Empty(t, job.Params.Passphrase)
	// 原样提交占位（例如只切换启用开关）时保留已保存的 API Key。
	require.NoError(t, update(job.Params))

	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	log := runOnce(app.RunSucceeded).Log
	require.Contains(t, log, "凭据未加密")
	require.Contains(t, log, "已上传到 s3：proxy/a.yaml")
	require.Len(t, uploads, 1)
	upload := uploads[0]
	require.Equal(t, "sk_test", upload.apiKey)
	require.Equal(t, map[string]string{"mode": "sync", "path": "proxy", "targets": "s3,dropbox"}, upload.query)
	require.True(t, strings.HasPrefix(upload.filename, "proxystack-all-"))
	sum := sha256.Sum256(upload.data)
	require.Equal(t, hex.EncodeToString(sum[:]), upload.sha)
	plain, err := configdoc.Parse(upload.data)
	require.NoError(t, err)
	require.Equal(t, configdoc.SecretsPlain, plain.Secrets)
	require.Equal(t, doc.Users[0].UUID, plain.Users[0].UUID)

	job = jobNamed(t, h, app.JobSafekeep)
	job.Params.Passphrase = "correct horse"
	require.NoError(t, update(job.Params))
	require.Contains(t, runOnce(app.RunSucceeded).Log, "凭据已用任务口令加密")
	encrypted, err := configdoc.Parse(uploads[1].data)
	require.NoError(t, err)
	require.Equal(t, configdoc.SecretsEncrypted, encrypted.Secrets)
	require.NoError(t, configdoc.Decrypt(encrypted, "correct horse"))

	setReply := func(handler func(http.ResponseWriter)) {
		mu.Lock()
		reply = handler
		mu.Unlock()
	}
	setReply(func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, `{"task":{"id":"t2","status":"partial_success","items":[{"upstream_name":"s3","status":"succeeded"},{"upstream_name":"dropbox","status":"failed","error_message":"token expired"}]}}`)
	})
	require.Contains(t, runOnce(app.RunFailed).Log, "上传到 dropbox 失败：token expired")

	setReply(func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]any{"id": "t3", "status": "running"}, "timed_out_to_async": true})
	})
	require.Contains(t, runOnce(app.RunSucceeded).Log, "转入后台上传（任务 t3）")

	setReply(func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_api_key","message":"API key is invalid"}}`)
	})
	require.Contains(t, runOnce(app.RunFailed).Log, "invalid_api_key")

	// 其他任务不保存 SafeKeep 参数。
	backupJob := jobNamed(t, h, app.JobBackup)
	backupJob.Params.SafekeepURL, backupJob.Params.SafekeepAPIKey = server.URL, "sk_test"
	require.NoError(t, h.svc.UpdateJob(ctx, h.actor, backupJob.ID, app.JobUpdate{Schedule: backupJob.Schedule, Enabled: true, Timeout: backupJob.Timeout, Params: backupJob.Params}))
	backupJob = jobNamed(t, h, app.JobBackup)
	require.Empty(t, backupJob.Params.SafekeepURL)
	require.Empty(t, backupJob.Params.SafekeepAPIKey)
}
