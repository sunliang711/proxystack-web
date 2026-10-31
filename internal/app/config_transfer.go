package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
)

// Revision 是配置历史中的一个版本。
type Revision struct {
	ID        int64
	CreatedAt time.Time
	Actor     string
	Summary   string
}

// ListRevisions 分页列出配置修订版本，新的在前。
func (s *Service) ListRevisions(ctx context.Context, limit, offset int) ([]Revision, int64, error) {
	var revisions []Revision
	var total int64
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListConfigRevisions(ctx, dbq.ListConfigRevisionsParams{Limit: int64(limit), Offset: int64(offset)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			revisions = append(revisions, Revision{ID: row.ID, CreatedAt: time.Unix(row.CreatedAt, 0), Actor: row.Actor, Summary: row.Summary})
		}
		total, err = q.CountConfigRevisions(ctx)
		return err
	})
	return revisions, total, err
}

// RevisionSnapshot 读取某个版本的完整配置。
func (s *Service) RevisionSnapshot(ctx context.Context, id int64) (*configdoc.Document, error) {
	var doc *configdoc.Document
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		row, err := q.GetConfigRevision(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		doc = &configdoc.Document{}
		if err := json.Unmarshal([]byte(row.Snapshot), doc); err != nil {
			return fmt.Errorf("revision %d snapshot is corrupt: %w", id, err)
		}
		doc.Normalize()
		return nil
	})
	return doc, err
}

// DiffRevisions 比较两个版本。to 为 0 表示当前配置。
func (s *Service) DiffRevisions(ctx context.Context, from, to int64) ([]configdoc.DiffEntry, error) {
	left, err := s.RevisionSnapshot(ctx, from)
	if err != nil {
		return nil, err
	}
	var right *configdoc.Document
	if to == 0 {
		right, err = s.LoadConfig(ctx)
	} else {
		right, err = s.RevisionSnapshot(ctx, to)
	}
	if err != nil {
		return nil, err
	}
	return configdoc.Diff(left, right), nil
}

// RollbackRevision 把配置恢复为指定版本的内容，并生成一个新版本。
func (s *Service) RollbackRevision(ctx context.Context, actor Actor, id int64) error {
	snapshot, err := s.RevisionSnapshot(ctx, id)
	if err != nil {
		return err
	}
	change := &ConfigChange{Action: ActionConfigRollback, Object: fmt.Sprintf("revision:%d", id), Summary: fmt.Sprintf("回滚到版本 #%d", id)}
	_, err = s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		*doc = *snapshot
		return nil
	})
	return err
}

// ExportOptions 是导出选项。
type ExportOptions struct {
	Scope      configdoc.ExportScope
	RevisionID int64
	Format     string
	Secrets    string
	Passphrase string
}

// ExportResult 是导出的文件。
type ExportResult struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ExportConfig 按范围导出配置文件，写入审计日志。
func (s *Service) ExportConfig(ctx context.Context, actor Actor, opts ExportOptions) (*ExportResult, error) {
	var source *configdoc.Document
	var err error
	if opts.RevisionID != 0 {
		source, err = s.RevisionSnapshot(ctx, opts.RevisionID)
	} else {
		source, err = s.LoadConfig(ctx)
	}
	if err != nil {
		return nil, err
	}
	stamp := s.Now().UTC().Format("20060102-150405")
	result := &ExportResult{}
	if opts.Format == "csv" {
		if opts.Scope.Scope != configdoc.ScopeUsers {
			return nil, &FieldError{Field: "format", Message: "CSV is only available for users"}
		}
		subset, err := configdoc.Subset(source, opts.Scope, s.Now())
		if err != nil {
			return nil, &FieldError{Field: "scope", Message: err.Error()}
		}
		if opts.Secrets == configdoc.SecretsOmitted {
			for index := range subset.Users {
				subset.Users[index].UUID, subset.Users[index].SSPassword = "", ""
			}
		} else if opts.Secrets == configdoc.SecretsEncrypted {
			return nil, &FieldError{Field: "secrets", Message: "CSV cannot be encrypted; choose plain or omitted"}
		}
		if result.Data, err = configdoc.UsersToCSV(subset.Users); err != nil {
			return nil, err
		}
		result.Filename, result.ContentType = "proxystack-users-"+stamp+".csv", "text/csv"
	} else {
		subset, err := configdoc.Subset(source, opts.Scope, s.Now())
		if err != nil {
			return nil, &FieldError{Field: "scope", Message: err.Error()}
		}
		if err := configdoc.ApplySecrets(subset, opts.Secrets, opts.Passphrase); err != nil {
			return nil, &FieldError{Field: "passphrase", Message: err.Error()}
		}
		format := opts.Format
		if format == "" {
			format = configdoc.FormatYAML
		}
		if result.Data, err = configdoc.Marshal(subset, format); err != nil {
			return nil, err
		}
		result.Filename = fmt.Sprintf("proxystack-%s-%s.%s", opts.Scope.Scope, stamp, format)
		result.ContentType = "application/yaml"
		if format == configdoc.FormatJSON {
			result.ContentType = "application/json"
		}
	}
	summary := fmt.Sprintf("导出 %s（凭据：%s，格式：%s）", opts.Scope.Scope, opts.Secrets, opts.Format)
	if opts.RevisionID != 0 {
		summary += fmt.Sprintf("，版本 #%d", opts.RevisionID)
	}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionConfigExport, Object: opts.Scope.Scope, Summary: summary,
		})
	})
	return result, err
}

// ImportRequest 是导入请求。
type ImportRequest struct {
	Filename   string
	Content    []byte
	CSV        bool
	Passphrase string
	Options    configdoc.ImportOptions
}

// ImportPreview 是导入预览。
type ImportPreview struct {
	Scope           string
	Secrets         string
	NeedsPassphrase bool
	Entries         []configdoc.ImportEntry
	Issues          []FieldIssue
	Valid           bool
}

// PreviewImport 解析文件并计算合并结果，不写入数据库。
func (s *Service) PreviewImport(ctx context.Context, req ImportRequest) (*ImportPreview, error) {
	preview, _, err := s.planImport(ctx, req)
	return preview, err
}

// ApplyImport 执行导入：替换模式先备份数据库；合并结果在单个事务内写入并生成修订版本。
func (s *Service) ApplyImport(ctx context.Context, actor Actor, req ImportRequest) (*ImportPreview, error) {
	preview, plan, err := s.planImport(ctx, req)
	if err != nil {
		return nil, err
	}
	if !preview.Valid {
		return preview, nil
	}
	if req.Options.Mode == configdoc.ImportReplace {
		if _, err := s.backupDatabase(ctx, "pre-import"); err != nil {
			return nil, err
		}
	}
	summary := fmt.Sprintf("导入：%s，范围 %s", req.Filename, preview.Scope)
	change := &ConfigChange{Action: ActionConfigImport, Object: req.Filename, Summary: summary}
	_, err = s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		*doc = *plan.Result
		return nil
	})
	var validation *ValidationError
	if errors.As(err, &validation) {
		preview.Issues, preview.Valid = validation.Translate(), false
		return preview, nil
	}
	return preview, err
}

func (s *Service) planImport(ctx context.Context, req ImportRequest) (*ImportPreview, *configdoc.ImportPlan, error) {
	var incoming *configdoc.Document
	if req.CSV {
		doc, issues := configdoc.UsersFromCSV(req.Content)
		if len(issues) > 0 {
			return nil, nil, &FieldError{Field: "content", Message: issues.Error()}
		}
		incoming = doc
	} else {
		doc, err := configdoc.Parse(req.Content)
		if err != nil {
			return nil, nil, &FieldError{Field: "content", Message: err.Error()}
		}
		incoming = doc
	}
	preview := &ImportPreview{Scope: incoming.Scope, Secrets: incoming.Secrets, Entries: []configdoc.ImportEntry{}, Issues: []FieldIssue{}}
	if err := configdoc.Decrypt(incoming, req.Passphrase); err != nil {
		if errors.Is(err, configdoc.ErrPassphraseRequired) {
			preview.NeedsPassphrase = true
			return preview, nil, nil
		}
		return nil, nil, &FieldError{Field: "passphrase", Message: err.Error()}
	}
	if req.Options.Mode == configdoc.ImportReplace && incoming.Scope != configdoc.ScopeAll {
		return nil, nil, &FieldError{Field: "mode", Message: "replace mode is only available for full configuration files"}
	}
	var current *configdoc.Document
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		current, err = store.LoadDocument(ctx, q)
		req.Options.ReservedPorts = s.reservedPorts(ctx, q)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	plan, err := configdoc.PlanImport(current, incoming, req.Options)
	if err != nil {
		return nil, nil, &FieldError{Field: "options", Message: err.Error()}
	}
	preview.Entries = plan.Entries
	if issues := configdoc.Validate(plan.Result); len(issues) > 0 {
		preview.Issues = (&ValidationError{Issues: issues, doc: plan.Result}).Translate()
	}
	preview.Valid = len(preview.Issues) == 0
	return preview, plan, nil
}

// SanitizeFilename 去掉文件名中的路径部分，用于修订摘要。
func SanitizeFilename(name string) string {
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	if name == "" {
		return "pasted text"
	}
	return name
}
