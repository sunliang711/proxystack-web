package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"proxystack-web/internal/configdoc"
)

// safekeepResponseLimit 是读取 SafeKeep 响应体的上限。
const safekeepResponseLimit = 1 << 20

// validateSafekeepParams 校验 SafeKeep 备份任务的地址和口令。地址或 API Key 可以留空，此时任务跳过。
func validateSafekeepParams(params JobParams) error {
	if params.SafekeepURL != "" {
		parsed, err := url.Parse(params.SafekeepURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return &FieldError{Field: "params.safekeep_url", Message: "must be an http or https URL"}
		}
	}
	if params.Passphrase != "" && len(params.Passphrase) < 8 {
		return &FieldError{Field: "params.passphrase", Message: "must be at least 8 characters"}
	}
	return nil
}

// exportAllConfig 导出全部配置（与「导出全部配置」相同的 YAML）。口令非空时凭据加密，留空时明文导出。
func (s *Service) exportAllConfig(ctx context.Context, passphrase string) (*ExportResult, error) {
	secrets := configdoc.SecretsPlain
	if passphrase != "" {
		secrets = configdoc.SecretsEncrypted
	}
	return s.ExportConfig(ctx, SystemActor, ExportOptions{
		Scope: configdoc.ExportScope{Scope: configdoc.ScopeAll}, Format: configdoc.FormatYAML,
		Secrets: secrets, Passphrase: passphrase,
	})
}

// safekeepTask 是 SafeKeep 上传接口返回的任务。
type safekeepTask struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Deduplicated bool   `json:"deduplicated"`
	ErrorSummary string `json:"error_summary"`
	Items        []struct {
		UpstreamName string `json:"upstream_name"`
		RemotePath   string `json:"remote_path"`
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
	} `json:"items"`
}

// runSafekeepBackupJob 导出全部配置并同步上传到 SafeKeep。未配置地址或 API Key 时跳过；
// SafeKeep 同步等待超时转入后台时只记录任务 ID，结果在 SafeKeep 中查看。
func runSafekeepBackupJob(ctx context.Context, s *Service, params JobParams, logf Logf) error {
	if params.SafekeepURL == "" || params.SafekeepAPIKey == "" {
		logf("未配置 SafeKeep 地址或 API Key，跳过；请在任务的「编辑」中填写")
		return errJobSkipped
	}
	exported, err := s.exportAllConfig(ctx, params.Passphrase)
	if err != nil {
		return err
	}
	if params.Passphrase == "" {
		logf("已导出 %s（%d 字节，凭据未加密）", exported.Filename, len(exported.Data))
	} else {
		logf("已导出 %s（%d 字节，凭据已用任务口令加密）", exported.Filename, len(exported.Data))
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, exported.Filename))
	header.Set("Content-Type", exported.ContentType)
	part, err := form.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(exported.Data); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}

	query := url.Values{"mode": {"sync"}}
	if params.SafekeepPath != "" {
		query.Set("path", params.SafekeepPath)
	}
	if len(params.SafekeepTargets) > 0 {
		query.Set("targets", strings.Join(params.SafekeepTargets, ","))
	}
	endpoint := strings.TrimRight(params.SafekeepURL, "/") + "/api/v1/backups?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(exported.Data)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("X-API-Key", params.SafekeepAPIKey)
	request.Header.Set("X-Content-SHA256", hex.EncodeToString(sum[:]))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("upload to safekeep: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, safekeepResponseLimit))
	if err != nil {
		return fmt.Errorf("read safekeep response: %w", err)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		var problem struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &problem) == nil && problem.Error.Code != "" {
			return fmt.Errorf("safekeep returned %d %s: %s", response.StatusCode, problem.Error.Code, problem.Error.Message)
		}
		return fmt.Errorf("safekeep returned %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	var result struct {
		Task            *safekeepTask `json:"task"`
		TimedOutToAsync bool          `json:"timed_out_to_async"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Task == nil {
		return fmt.Errorf("unexpected safekeep response: %s", strings.TrimSpace(string(data)))
	}
	task := result.Task
	if response.StatusCode == http.StatusAccepted {
		logf("SafeKeep 已接收，转入后台上传（任务 %s），结果请在 SafeKeep 中查看", task.ID)
		return nil
	}
	var failures []string
	for _, item := range task.Items {
		if item.Status == "succeeded" {
			logf("已上传到 %s：%s", item.UpstreamName, item.RemotePath)
		} else {
			logf("上传到 %s 失败：%s", item.UpstreamName, item.ErrorMessage)
			failures = append(failures, item.UpstreamName)
		}
	}
	if task.Deduplicated {
		logf("内容与已有备份相同，SafeKeep 未重复上传")
	}
	if task.Status != "succeeded" {
		summary := task.ErrorSummary
		if summary == "" && len(failures) > 0 {
			summary = "failed upstreams: " + strings.Join(failures, ", ")
		}
		return fmt.Errorf("safekeep task %s %s: %s", task.ID, task.Status, summary)
	}
	logf("备份完成（SafeKeep 任务 %s）", task.ID)
	return nil
}
