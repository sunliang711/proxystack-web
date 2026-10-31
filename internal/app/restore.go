package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/backup"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/paths"
	"proxystack-web/internal/runtime"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/units"
)

// settingRestoreState 记录恢复后尚未完成的收尾工作（运行状态，不属于配置）。
const settingRestoreState = "restore_state"

// restoreState 在恢复时写入新数据库：依赖就绪后重新生成配置，按 Start 拉起 stack。
type restoreState struct {
	Pending bool              `json:"pending"`
	Start   bool              `json:"start"`
	Deps    map[string]string `json:"deps"`
}

// RestorePreview 是恢复前的预览。
type RestorePreview struct {
	Hostname     string            `json:"hostname"`
	ExportedAt   time.Time         `json:"exported_at"`
	AppVersion   string            `json:"app_version"`
	ExternalHost string            `json:"external_host"`
	Counts       map[string]int64  `json:"counts"`
	TrafficFrom  time.Time         `json:"traffic_from"`
	TrafficTo    time.Time         `json:"traffic_to"`
	Deps         map[string]string `json:"deps"`
	// PortConflicts 是迁移包中的 stack 与本机已被占用端口的冲突。
	PortConflicts []string `json:"port_conflicts"`
}

// StageRestore 解开迁移包到暂存目录并校验：格式、sha256、数据库版本。数据库版本高于当前程序时拒绝；
// 低于当前程序时在暂存副本上执行迁移。返回预览，不改动当前实例。
func (s *Service) StageRestore(ctx context.Context, source io.Reader, passphrase string) (*RestorePreview, error) {
	staging := s.Layout.RestoreStagingDir()
	if err := os.RemoveAll(staging); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return nil, err
	}
	manifest, err := stagePackage(ctx, source, passphrase, staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	if err := writeStagedManifest(staging, manifest); err != nil {
		return nil, err
	}
	preview := &RestorePreview{
		Hostname: manifest.Hostname, ExportedAt: manifest.ExportedAt, AppVersion: manifest.AppVersion, ExternalHost: manifest.ExternalHost,
		Counts: manifest.Counts, TrafficFrom: manifest.TrafficFrom, TrafficTo: manifest.TrafficTo, Deps: manifest.Deps,
		PortConflicts: []string{},
	}
	if preview.PortConflicts, err = s.portConflicts(ctx, staging); err != nil {
		return nil, err
	}
	return preview, nil
}

func stagedManifestPath(staging string) string { return filepath.Join(staging, "manifest.json") }

func writeStagedManifest(staging string, manifest *backup.Manifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return os.WriteFile(stagedManifestPath(staging), data, 0o640)
}

// stagePackage 解开迁移包，并让暂存的数据库升级到当前程序的结构。
func stagePackage(ctx context.Context, source io.Reader, passphrase, staging string) (*backup.Manifest, error) {
	manifest, err := backup.Read(source, passphrase, staging)
	if errors.Is(err, backup.ErrPassphraseRequired) || errors.Is(err, backup.ErrWrongPassphrase) {
		return nil, &FieldError{Field: "passphrase", Message: err.Error()}
	}
	if err != nil {
		return nil, &FieldError{Field: "file", Message: err.Error()}
	}
	st, err := store.Open(filepath.Join(staging, backup.DatabaseName))
	if err != nil {
		return nil, err
	}
	defer st.Close()
	current, latest, err := st.MigrationStatus(ctx)
	if err != nil {
		return nil, err
	}
	if current > latest {
		return nil, &FieldError{Field: "file", Message: fmt.Sprintf("the package has database version %d, newer than this program (%d); upgrade proxystack-web first", current, latest)}
	}
	if err := st.Migrate(ctx); err != nil {
		return nil, err
	}
	return manifest, nil
}

// portConflicts 检查迁移包中各 stack 的端口是否被本机占用。当前实例自己的 stack 占用的端口不算：恢复时会先停掉它们。
func (s *Service) portConflicts(ctx context.Context, staging string) ([]string, error) {
	st, err := store.Open(filepath.Join(staging, backup.DatabaseName))
	if err != nil {
		return nil, err
	}
	defer st.Close()
	var doc *configdoc.Document
	err = st.InTx(ctx, func(q *dbq.Queries) (err error) {
		doc, err = store.LoadDocument(ctx, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	ours := map[int]bool{}
	if current, err := s.LoadConfig(ctx); err == nil {
		for index := range current.Stacks {
			for port := range configdoc.StackPorts(&current.Stacks[index]) {
				ours[port] = true
			}
		}
	}
	out := []string{}
	if s.PortChecker == nil {
		return out, nil
	}
	for index := range doc.Stacks {
		stack := &doc.Stacks[index]
		ports := []int{}
		for port := range configdoc.StackPorts(stack) {
			ports = append(ports, port)
		}
		sort.Ints(ports)
		for _, port := range ports {
			if port != 0 && !ours[port] && !s.PortChecker.IsAvailable("0.0.0.0", port) {
				out = append(out, fmt.Sprintf("stack %s：端口 %d 已被占用", stack.Name, port))
			}
		}
	}
	return out, nil
}

// RestoreOptions 是恢复选项。
type RestoreOptions struct {
	// ExternalHost 非空时覆盖迁移包中的对外地址。
	ExternalHost string `json:"external_host" required:"false"`
	// KeepPanel 为真时保留本机的面板设置（监听地址和端口）。
	KeepPanel bool `json:"keep_panel" required:"false"`
	// KeepAdmins 为真时保留本机的管理员账号。
	KeepAdmins bool `json:"keep_admins" required:"false"`
	// StartProxies 为真时恢复后拉起期望运行的 stack。
	StartProxies bool `json:"start_proxies" required:"false"`
}

// prepareRestore 在暂存的数据库上应用恢复选项。current 为 nil 表示全新安装，没有可保留的内容。
func prepareRestore(ctx context.Context, staging string, actor Actor, opts RestoreOptions, manifest *backup.Manifest, current *Service) error {
	st, err := store.Open(filepath.Join(staging, backup.DatabaseName))
	if err != nil {
		return err
	}
	defer st.Close()
	var admins []dbq.Admin
	var panel store.PanelSettings
	if current != nil {
		if opts.KeepPanel {
			if panel, err = store.GetPanelSettings(ctx, current.Store.Q); err != nil {
				return err
			}
		}
		if opts.KeepAdmins {
			if admins, err = current.Store.Q.ListAdmins(ctx); err != nil {
				return err
			}
		}
	}
	return st.InTx(ctx, func(q *dbq.Queries) error {
		// 会话不恢复，恢复后需要重新登录。
		if err := q.DeleteAllSessions(ctx); err != nil {
			return err
		}
		if opts.ExternalHost != "" {
			basic, err := store.GetBasicSettings(ctx, q)
			if err != nil {
				return err
			}
			basic.ExternalHost = opts.ExternalHost
			if err := store.PutJSON(ctx, q, store.SettingBasic, basic); err != nil {
				return err
			}
		}
		if current != nil && opts.KeepPanel {
			if err := store.PutJSON(ctx, q, store.SettingPanel, panel); err != nil {
				return err
			}
		}
		if len(admins) > 0 {
			if err := q.DeleteAllAdmins(ctx); err != nil {
				return err
			}
			for _, admin := range admins {
				if err := q.RestoreAdmin(ctx, dbq.RestoreAdminParams(admin)); err != nil {
					return err
				}
			}
		}
		if !opts.StartProxies {
			if err := q.StopAllStacks(ctx); err != nil {
				return err
			}
		}
		state := restoreState{Pending: true, Start: opts.StartProxies, Deps: manifest.Deps}
		if err := store.PutJSON(ctx, q, settingRestoreState, state); err != nil {
			return err
		}
		// 写进恢复后的数据库：旧数据库即将被替换，这条记录要留在新实例上。
		return audit.Write(ctx, q, time.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionBackupRestore, Object: manifest.Hostname,
			Summary: fmt.Sprintf("恢复迁移包（来源 %s，%s 导出）", manifest.Hostname, manifest.ExportedAt.Format(time.RFC3339)),
		})
	})
}

// ExecuteRestore 执行已暂存的恢复：备份当前数据库，把暂存内容放到待替换位置，然后退出由 systemd 重启，
// 启动时在打开数据库之前替换到位。运行中不动数据库文件。
func (s *Service) ExecuteRestore(ctx context.Context, actor Actor, opts RestoreOptions) error {
	if s.RequestRestart == nil {
		return &FieldError{Field: "restore", Message: "restoring needs the service to restart, which is not available in this mode"}
	}
	if err := s.stageForRestart(ctx, actor, opts); err != nil {
		return err
	}
	s.RequestRestart()
	return nil
}

func (s *Service) stageForRestart(ctx context.Context, actor Actor, opts RestoreOptions) error {
	staging := s.Layout.RestoreStagingDir()
	data, err := os.ReadFile(stagedManifestPath(staging))
	if errors.Is(err, os.ErrNotExist) {
		return &FieldError{Field: "restore", Message: "upload a migration package first"}
	}
	if err != nil {
		return err
	}
	var manifest backup.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if err := prepareRestore(ctx, staging, actor, opts, &manifest, s); err != nil {
		return err
	}
	name := "pre-restore-" + s.Now().UTC().Format("20060102-150405") + dbSuffix
	if err := os.MkdirAll(s.Layout.BackupsDir(), 0o750); err != nil {
		return err
	}
	if err := s.snapshotDatabase(ctx, filepath.Join(s.Layout.BackupsDir(), name)); err != nil {
		return fmt.Errorf("back up the current database: %w", err)
	}
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionBackupRestore, Object: manifest.Hostname,
			Summary: fmt.Sprintf("恢复迁移包（来源 %s，%s 导出），当前数据库已备份为 %s", manifest.Hostname, manifest.ExportedAt.Format(time.RFC3339), name),
		})
	}); err != nil {
		return err
	}
	pending := s.Layout.RestorePendingDir()
	if err := os.RemoveAll(pending); err != nil {
		return err
	}
	return os.Rename(staging, pending)
}

// StageRestoreForNextStart 供命令行使用：把已暂存的迁移包放到待替换位置，下次服务启动时生效。
func (s *Service) StageRestoreForNextStart(ctx context.Context, opts RestoreOptions) error {
	return s.stageForRestart(ctx, Actor{Name: "cli"}, opts)
}

// ApplyPendingRestore 把待替换的数据库放到位。在打开数据库之前调用。返回是否执行了替换。
func ApplyPendingRestore(layout paths.Layout) (bool, error) {
	pending := layout.RestorePendingDir()
	database := filepath.Join(pending, backup.DatabaseName)
	if _, err := os.Stat(database); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(layout.DBPath() + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	if err := os.Rename(database, layout.DBPath()); err != nil {
		return false, err
	}
	return true, os.RemoveAll(pending)
}

// RestoreFresh 在全新安装的目录上恢复迁移包（install.sh --restore），数据库直接就位。
func RestoreFresh(ctx context.Context, layout paths.Layout, source io.Reader, passphrase string, opts RestoreOptions) (*backup.Manifest, error) {
	if _, err := os.Stat(layout.DBPath()); err == nil {
		return nil, ErrAlreadyInitialized
	}
	staging := layout.RestoreStagingDir()
	if err := os.RemoveAll(staging); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	manifest, err := stagePackage(ctx, source, passphrase, staging)
	if err != nil {
		return nil, err
	}
	if err := prepareRestore(ctx, staging, Actor{Name: "cli"}, opts, manifest, nil); err != nil {
		return nil, err
	}
	pending := layout.RestorePendingDir()
	if err := os.RemoveAll(pending); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, pending); err != nil {
		return nil, err
	}
	if _, err := ApplyPendingRestore(layout); err != nil {
		return nil, err
	}
	return manifest, nil
}

// RestoreStatus 是恢复后的收尾状态。
type RestoreStatus struct {
	Pending bool `json:"pending"`
	// Deps 是源实例的依赖版本，Missing 是本机缺失、阻塞收尾的组件。
	Deps    map[string]string `json:"deps"`
	Missing []string          `json:"missing"`
}

func (s *Service) loadRestoreState(ctx context.Context) (restoreState, error) {
	var state restoreState
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		err := store.GetJSON(ctx, q, settingRestoreState, &state)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	})
	return state, err
}

// missingDeps 返回源实例上有、本机还没装的组件。源实例上就没有的组件不阻塞收尾。
func (s *Service) missingDeps(ctx context.Context, state restoreState) ([]string, error) {
	status, err := s.DepsStatus(ctx)
	if err != nil {
		return nil, err
	}
	missing := []string{}
	for _, dep := range status {
		if _, used := state.Deps[dep.Component]; used && !dep.Installed {
			missing = append(missing, dep.Component)
		}
	}
	return missing, nil
}

// RestoreStatusData 返回恢复后的收尾状态。
func (s *Service) RestoreStatusData(ctx context.Context) (*RestoreStatus, error) {
	state, err := s.loadRestoreState(ctx)
	if err != nil {
		return nil, err
	}
	out := &RestoreStatus{Pending: state.Pending, Deps: state.Deps, Missing: []string{}}
	if out.Deps == nil {
		out.Deps = map[string]string{}
	}
	if state.Pending {
		if out.Missing, err = s.missingDeps(ctx, state); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// resumeAfterRestore 完成恢复：依赖就绪后停掉旧 stack 留下的 unit，按已应用版本重新生成配置，
// 再按选项拉起期望运行的 stack。依赖缺失时保持待处理，等依赖安装后再执行。
func (s *Service) resumeAfterRestore(ctx context.Context) {
	state, err := s.loadRestoreState(ctx)
	if err != nil || !state.Pending {
		if err != nil {
			slog.Warn("resume after restore", "err", err)
		}
		return
	}
	missing, err := s.missingDeps(ctx, state)
	if err != nil {
		slog.Warn("resume after restore", "err", err)
		return
	}
	if len(missing) > 0 {
		slog.Info("restore waits for dependencies", "missing", missing)
		return
	}
	if err := s.finishRestore(ctx); err != nil {
		slog.Error("resume after restore", "err", err)
		return
	}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if err := store.PutJSON(ctx, q, settingRestoreState, restoreState{}); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: SystemActor.Name, Action: ActionBackupRestore, Object: "runtime", Summary: "恢复完成：已重新生成配置"})
	})
	if err != nil {
		slog.Warn("resume after restore", "err", err)
	}
	if _, err := s.Reconcile(ctx, true); err != nil {
		slog.Warn("resume after restore", "err", err)
	}
}

func (s *Service) finishRestore(ctx context.Context) error {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	stale, err := s.disk().Stacks()
	if err != nil {
		return err
	}
	for _, name := range stale {
		for _, component := range []string{units.Mihomo, units.Xray} {
			if err := s.Units.Stop(ctx, units.Name(component, name)); err != nil {
				slog.Warn("stop unit after restore", "stack", name, "component", component, "err", err)
			}
		}
		if err := s.disk().Write(name, runtime.Files{}); err != nil {
			return err
		}
	}
	current, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	var applied []dbq.AppliedState
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		applied, err = q.ListAppliedState(ctx)
		return err
	}); err != nil {
		return err
	}
	auto := s.autoPaused(ctx)
	snapshots := map[int64]*configdoc.Document{}
	for _, row := range applied {
		if _, _, err := s.rewriteApplied(ctx, row, current, auto, snapshots); err != nil {
			return fmt.Errorf("stack %s: %w", row.StackName, err)
		}
	}
	return nil
}
