package app

import (
	"context"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/notify"
)

// configMailRecipients 返回导出邮件的收件人：任务参数没有填写时发给 SMTP 发件人地址。
func configMailRecipients(params JobParams, settings configdoc.NotifySettings) []string {
	if len(params.Recipients) > 0 {
		return params.Recipients
	}
	if settings.From == "" {
		return nil
	}
	return []string{settings.From}
}

// validateConfigMailParams 校验配置导出邮件任务的收件人和口令。口令可以留空，此时任务跳过。
func validateConfigMailParams(params JobParams) error {
	for index, recipient := range params.Recipients {
		if _, err := mail.ParseAddress(recipient); err != nil {
			return &FieldError{Field: fmt.Sprintf("params.recipients[%d]", index), Message: "invalid email address"}
		}
	}
	if params.Passphrase != "" && len(params.Passphrase) < 8 {
		return &FieldError{Field: "params.passphrase", Message: "must be at least 8 characters"}
	}
	return nil
}

// runConfigMailJob 导出全部配置（凭据用任务口令加密），作为附件通过 SMTP 发出。邮件通知未启用或未设置口令时跳过。
func runConfigMailJob(ctx context.Context, s *Service, params JobParams, logf Logf) error {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Notify.Enabled {
		logf("系统设置中的邮件通知未启用，跳过")
		return errJobSkipped
	}
	if params.Passphrase == "" {
		logf("未设置加密口令，跳过；请在任务的「编辑」中设置口令")
		return errJobSkipped
	}
	recipients := configMailRecipients(params, settings.Notify)
	if len(recipients) == 0 {
		return fmt.Errorf("no recipients: set recipients in the job or a sender address in the SMTP settings")
	}
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	exported, err := s.ExportConfig(ctx, SystemActor, ExportOptions{
		Scope: configdoc.ExportScope{Scope: configdoc.ScopeAll}, Format: configdoc.FormatYAML,
		Secrets: configdoc.SecretsEncrypted, Passphrase: params.Passphrase,
	})
	if err != nil {
		return err
	}
	logf("已导出 %s（%d 个用户，%d 个 Stack，%d 字节）", exported.Filename, len(doc.Users), len(doc.Stacks), len(exported.Data))
	hostname, _ := os.Hostname()
	at := s.Now().In(location(settings))
	lines := []string{
		"附件是本实例全部配置的导出文件：系统设置（不含面板监听和管理员）、全部用户和 Stack。",
		"凭据字段已用「配置导出邮件」任务中设置的口令加密。",
		"",
		"主机：" + hostname,
		"导出时间：" + at.Format(time.DateTime+" MST"),
		fmt.Sprintf("用户：%d 个，Stack：%d 个", len(doc.Users), len(doc.Stacks)),
	}
	if info, err := s.Info(ctx); err == nil {
		lines = append(lines, "面板："+info.PanelURL)
	}
	lines = append(lines, "", "恢复方法：在面板「备份与迁移」页点「导入配置」，上传附件并输入口令。流量数据、审计日志和管理员账号不在此文件中。")
	subject := fmt.Sprintf("[proxystack-web] 配置导出：%s %s", hostname, at.Format(time.DateOnly))
	config := smtpConfig(settings.Notify)
	config.To = recipients
	attachment := notify.Attachment{Name: exported.Filename, ContentType: exported.ContentType, Data: exported.Data}
	if err := s.sendMail(ctx, config, subject, strings.Join(lines, "\n")+"\n", attachment); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	logf("已发送到 %s", strings.Join(recipients, ", "))
	return nil
}
