package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/notify"
)

type sentMail struct {
	to          []string
	subject     string
	body        string
	attachments []notify.Attachment
}

// TestConfigMailJob 验证配置导出邮件任务：邮件通知未启用或未设口令时跳过；收件人默认是发件人地址，可以改；
// 附件能用任务口令解开；口令读取时打码，原样提交占位时保留原值；发送失败记为失败。
func TestConfigMailJob(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	var mu sync.Mutex
	var mails []sentMail
	var sendErr error
	h.svc.SendMail = func(_ context.Context, config notify.Config, subject, body string, attachments ...notify.Attachment) error {
		mu.Lock()
		defer mu.Unlock()
		if sendErr != nil {
			return sendErr
		}
		mails = append(mails, sentMail{to: config.To, subject: subject, body: body, attachments: attachments})
		return nil
	}
	runOnce := func(want string) app.JobRun {
		t.Helper()
		job := jobNamed(t, h, app.JobConfigMail)
		require.NoError(t, h.svc.RunJobNow(ctx, h.actor, job.ID))
		h.svc.WaitJobs()
		last := jobNamed(t, h, app.JobConfigMail).LastRun
		require.NotNil(t, last)
		require.Equal(t, want, last.Status, last.Log)
		return *last
	}
	update := func(params app.JobParams) error {
		job := jobNamed(t, h, app.JobConfigMail)
		return h.svc.UpdateJob(ctx, h.actor, job.ID, app.JobUpdate{Schedule: job.Schedule, Enabled: job.Enabled, Timeout: job.Timeout, Params: params})
	}

	job := jobNamed(t, h, app.JobConfigMail)
	require.Equal(t, "30 4 1,15 * *", job.Schedule)
	require.True(t, job.Enabled)
	require.Contains(t, runOnce(app.RunSkipped).Log, "邮件通知未启用")

	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Notify.Enabled = true
	settings.Notify.Host, settings.Notify.Port = "smtp.example.com", 587
	settings.Notify.From, settings.Notify.To = "Panel <panel@example.com>", []string{"admin@example.com"}
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)
	require.Contains(t, runOnce(app.RunSkipped).Log, "未设置加密口令")
	require.Empty(t, mails)

	require.Error(t, update(app.JobParams{Passphrase: "short"}))
	require.Error(t, update(app.JobParams{Passphrase: "correct horse", Recipients: []string{"not an address"}}))
	require.NoError(t, update(app.JobParams{Passphrase: "correct horse", Recipients: []string{" ", ""}}))
	job = jobNamed(t, h, app.JobConfigMail)
	require.Equal(t, configdoc.MaskedValue, job.Params.Passphrase)
	require.Empty(t, job.Params.Recipients)
	// 原样提交占位（例如只切换启用开关）时保留已保存的口令。
	require.NoError(t, update(job.Params))

	require.Contains(t, runOnce(app.RunSucceeded).Log, "已发送到 Panel <panel@example.com>")
	require.Len(t, mails, 1)
	mail := mails[0]
	require.Equal(t, []string{"Panel <panel@example.com>"}, mail.to)
	require.Contains(t, mail.subject, "配置导出")
	require.Contains(t, mail.body, "导入配置")
	require.Len(t, mail.attachments, 1)
	require.True(t, strings.HasPrefix(mail.attachments[0].Name, "proxystack-all-"))
	require.True(t, strings.HasSuffix(mail.attachments[0].Name, ".yaml"))
	exported, err := configdoc.Parse(mail.attachments[0].Data)
	require.NoError(t, err)
	require.Equal(t, configdoc.SecretsEncrypted, exported.Secrets)
	require.Error(t, configdoc.Decrypt(exported, "wrong passphrase"))
	require.NoError(t, configdoc.Decrypt(exported, "correct horse"))
	require.Len(t, exported.Users, len(doc.Users))
	require.Equal(t, doc.Users[0].UUID, exported.Users[0].UUID)

	require.NoError(t, update(app.JobParams{Passphrase: configdoc.MaskedValue, Recipients: []string{"ops@example.com", "backup@example.com"}}))
	runOnce(app.RunSucceeded)
	require.Equal(t, []string{"ops@example.com", "backup@example.com"}, mails[1].to)

	sendErr = errors.New("connection refused")
	require.Contains(t, runOnce(app.RunFailed).Log, "connection refused")

	// 其他任务不保存邮件参数。
	backupJob := jobNamed(t, h, app.JobBackup)
	backupJob.Params.Passphrase, backupJob.Params.Recipients = "correct horse", []string{"ops@example.com"}
	require.NoError(t, h.svc.UpdateJob(ctx, h.actor, backupJob.ID, app.JobUpdate{Schedule: backupJob.Schedule, Enabled: true, Timeout: backupJob.Timeout, Params: backupJob.Params}))
	backupJob = jobNamed(t, h, app.JobBackup)
	require.Empty(t, backupJob.Params.Passphrase)
	require.Empty(t, backupJob.Params.Recipients)
}
