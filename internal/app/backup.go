package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/backup"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/version"
)

// 备份相关的审计动作。
const (
	ActionBackupExport  = "backup.export"
	ActionBackupRestore = "backup.restore"
	ActionBackupDelete  = "backup.delete"
)

// 备份文件名前缀：autoPrefix 由「数据库备份」任务生成，受保留份数限制。
const (
	autoPrefix = "auto-"
	dbSuffix   = ".db"
)

var backupNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// snapshotDatabase 把内存中未落库的流量写入数据库，再用 VACUUM INTO 生成一致性快照。
func (s *Service) snapshotDatabase(ctx context.Context, dest string) error {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return err
	}
	s.collectTraffic(ctx)
	if err := s.flushTraffic(ctx, settings); err != nil {
		return err
	}
	_, err = s.Store.DB.ExecContext(ctx, "VACUUM INTO ?", dest)
	return err
}

func runBackupJob(ctx context.Context, s *Service, params JobParams, logf Logf) error {
	if err := os.MkdirAll(s.Layout.BackupsDir(), 0o750); err != nil {
		return err
	}
	name := autoPrefix + s.Now().UTC().Format("20060102-150405") + dbSuffix
	if err := s.snapshotDatabase(ctx, filepath.Join(s.Layout.BackupsDir(), name)); err != nil {
		return err
	}
	logf("已生成 %s", name)
	entries, err := os.ReadDir(s.Layout.BackupsDir())
	if err != nil {
		return err
	}
	auto := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), autoPrefix) && strings.HasSuffix(entry.Name(), dbSuffix) {
			auto = append(auto, entry.Name())
		}
	}
	sort.Strings(auto)
	for len(auto) > params.Keep && params.Keep > 0 {
		if err := os.Remove(filepath.Join(s.Layout.BackupsDir(), auto[0])); err != nil {
			return err
		}
		logf("已删除旧快照 %s", auto[0])
		auto = auto[1:]
	}
	return nil
}

// BackupFile 是备份目录中的一个文件。
type BackupFile struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
	Kind       string    `json:"kind" enum:"snapshot,package"`
}

// ListBackups 列出备份目录中的快照和迁移包，新的在前。
func (s *Service) ListBackups() ([]BackupFile, error) {
	entries, err := os.ReadDir(s.Layout.BackupsDir())
	if errors.Is(err, os.ErrNotExist) {
		return []BackupFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []BackupFile{}
	for _, entry := range entries {
		kind := map[string]string{dbSuffix: "snapshot", backup.Extension: "package"}[filepath.Ext(entry.Name())]
		info, err := entry.Info()
		if kind == "" || err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, BackupFile{Name: entry.Name(), Size: info.Size(), ModifiedAt: info.ModTime(), Kind: kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	return out, nil
}

// backupPath 校验文件名后返回备份文件路径，只允许备份目录下的文件。
func (s *Service) backupPath(name string) (string, error) {
	if !backupNamePattern.MatchString(name) || (filepath.Ext(name) != dbSuffix && filepath.Ext(name) != backup.Extension) {
		return "", ErrNotFound
	}
	path := filepath.Join(s.Layout.BackupsDir(), name)
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return "", ErrNotFound
	}
	return path, nil
}

// OpenBackup 打开备份文件供下载，记入审计日志：其中包含全部凭据。
func (s *Service) OpenBackup(ctx context.Context, actor Actor, name string) (*os.File, int64, error) {
	path, err := s.backupPath(name)
	if err != nil {
		return nil, 0, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionBackupExport, Object: name, Summary: "下载备份文件 " + name,
		})
	})
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

// DeleteBackup 删除备份文件。
func (s *Service) DeleteBackup(ctx context.Context, actor Actor, name string) error {
	path, err := s.backupPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionBackupDelete, Object: name, Summary: "删除备份文件 " + name,
		})
	})
}

// ExportRequest 是导出迁移包的选项。
type ExportRequest struct {
	// Passphrase 是加密口令；Plain 为真时不加密，迁移包包含全部明文凭据。
	Passphrase string `json:"passphrase" required:"false"`
	Plain      bool   `json:"plain" required:"false"`
	// Snapshot 非空时用备份目录中已有的快照打包，否则新生成一份。
	Snapshot string `json:"snapshot" required:"false"`
}

func (r ExportRequest) validate() error {
	if !r.Plain && len(r.Passphrase) < 8 {
		return &FieldError{Field: "passphrase", Message: "must be at least 8 characters"}
	}
	return nil
}

// ExportPackage 在后台作业中生成迁移包到备份目录，完成后在「备份」页下载。
func (s *Service) ExportPackage(ctx context.Context, actor Actor, req ExportRequest) (int64, error) {
	if err := req.validate(); err != nil {
		return 0, err
	}
	return s.startTask("backup", "导出迁移包", actor.Name, func(ctx context.Context, logf Logf) error {
		name, err := s.writeDefaultPackage(ctx, req, logf)
		if err != nil {
			return err
		}
		logf("已生成 %s，可以在备份列表中下载", name)
		return s.Store.InTx(ctx, func(q *dbq.Queries) error {
			return audit.Write(ctx, q, s.Now(), audit.Entry{
				Actor: actor.Name, SourceIP: actor.IP, Action: ActionBackupExport, Object: name,
				Summary: fmt.Sprintf("导出迁移包 %s（加密=%t）", name, !req.Plain),
			})
		})
	})
}

func (s *Service) writeDefaultPackage(ctx context.Context, req ExportRequest, logf Logf) (string, error) {
	hostname, _ := os.Hostname()
	safe := regexp.MustCompile(`[^A-Za-z0-9-]+`).ReplaceAllString(hostname, "-")
	name := strings.Trim(safe, "-")
	if name == "" {
		name = "proxystack"
	}
	name += "-" + s.Now().UTC().Format("20060102-150405") + backup.Extension
	if err := os.MkdirAll(s.Layout.BackupsDir(), 0o750); err != nil {
		return "", err
	}
	return name, s.WritePackage(ctx, filepath.Join(s.Layout.BackupsDir(), name), req, logf)
}

// WritePackage 生成迁移包到 dest（先写临时文件，完成后改名）。
func (s *Service) WritePackage(ctx context.Context, dest string, req ExportRequest, logf Logf) error {
	if err := req.validate(); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(filepath.Dir(dest), ".export-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	snapshot := filepath.Join(temp, backup.DatabaseName)
	if req.Snapshot != "" {
		source, err := s.backupPath(req.Snapshot)
		if err != nil {
			return err
		}
		logf("使用已有快照 %s", req.Snapshot)
		if err := copyFile(source, snapshot, 0o640); err != nil {
			return err
		}
	} else {
		logf("生成数据库快照")
		if err := s.snapshotDatabase(ctx, snapshot); err != nil {
			return err
		}
	}
	manifest, err := s.buildManifest(ctx, snapshot)
	if err != nil {
		return err
	}
	files := []backup.File{{Name: backup.DatabaseName, Path: snapshot}}
	logf("打包%s", map[bool]string{true: "（不加密）", false: "并加密"}[req.Plain])
	partial := filepath.Join(temp, "package")
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	passphrase := req.Passphrase
	if req.Plain {
		passphrase = ""
	}
	err = backup.Write(file, *manifest, files, passphrase)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(partial, dest)
}

func (s *Service) buildManifest(ctx context.Context, snapshot string) (*backup.Manifest, error) {
	st, err := store.Open(snapshot)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	schema, _, err := st.MigrationStatus(ctx)
	if err != nil {
		return nil, err
	}
	stats, err := st.Stats(ctx)
	if err != nil {
		return nil, err
	}
	basic, err := store.GetBasicSettings(ctx, st.Q)
	if err != nil {
		return nil, err
	}
	hostname, _ := os.Hostname()
	manifest := &backup.Manifest{
		AppVersion: version.Version, SchemaVersion: schema, ExportedAt: s.Now().UTC(), Hostname: hostname, ExternalHost: basic.ExternalHost,
		Counts: stats.Counts, TrafficFrom: stats.TrafficFrom, TrafficTo: stats.TrafficTo, Deps: map[string]string{},
	}
	status, err := s.DepsStatus(ctx)
	if err != nil {
		return nil, err
	}
	for _, dep := range status {
		if dep.Installed {
			manifest.Deps[dep.Component] = dep.Version
		}
	}
	return manifest, nil
}

func copyFile(source, dest string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}

// lastBackup 返回最近一个备份文件的时间。
func (s *Service) lastBackup() (time.Time, bool) {
	files, err := s.ListBackups()
	if err != nil || len(files) == 0 {
		return time.Time{}, false
	}
	return slices.MaxFunc(files, func(a, b BackupFile) int { return a.ModifiedAt.Compare(b.ModifiedAt) }).ModifiedAt, true
}
