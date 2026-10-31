package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"proxystack-web/internal/api"
	"proxystack-web/internal/app"
	"proxystack-web/internal/runtime"
	"proxystack-web/internal/store"
	"proxystack-web/internal/units"
	"proxystack-web/internal/version"
	"proxystack-web/internal/xrayapi"
	"proxystack-web/web"
)

func newServeCommand(g *globalFlags) *cobra.Command {
	var dev bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the management service (default command)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context(), g, dev)
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "development mode: non-Secure cookies, fake systemd and xray APIs")
	return cmd
}

func runServe(ctx context.Context, g *globalFlags, dev bool) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	layout := g.layout()
	// 恢复迁移包时数据库在这里替换到位：此时还没有任何连接打开它。
	if restored, err := app.ApplyPendingRestore(layout); err != nil {
		return fmt.Errorf("apply pending restore: %w", err)
	} else if restored {
		slog.Info("database replaced from the migration package")
	}
	if _, err := os.Stat(layout.DBPath()); err != nil {
		return app.ErrNotInitialized
	}
	st, err := store.Open(layout.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	if err := migrateWithBackup(ctx, st, layout.BackupsDir()); err != nil {
		return err
	}
	svc := app.New(st, layout)
	if ok, err := svc.IsInitialized(ctx); err != nil {
		return err
	} else if !ok {
		return app.ErrNotInitialized
	}
	panel, err := store.GetPanelSettings(ctx, st.Q)
	if err != nil {
		return fmt.Errorf("read panel settings: %w", err)
	}
	if err := setupRuntime(ctx, svc, dev); err != nil {
		return err
	}
	defer svc.Units.Close()
	// 后台循环在退出时最后落库一次，必须等它结束再关闭数据库（自更新重启时 ctx 不会被信号取消，这里单独取消）。
	backgroundCtx, stopBackground := context.WithCancel(ctx)
	defer func() {
		stopBackground()
		svc.WaitBackground()
	}()
	if err := svc.StartBackground(backgroundCtx); err != nil {
		return err
	}
	restartCh := make(chan struct{}, 1)
	if !dev {
		svc.RequestRestart = func() {
			select {
			case restartCh <- struct{}{}:
			default:
			}
		}
	}
	handler := api.New(svc, api.Options{
		PanelPath:  panel.Path,
		Dev:        dev,
		TrustProxy: panel.TLSMode == store.TLSProxy,
		Static:     web.Dist(),
	})
	srv := &http.Server{
		Addr:              net.JoinHostPort(panel.Listen, strconv.Itoa(panel.Port)),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("proxystack-web started", "version", version.Version, "addr", srv.Addr, "tls", panel.TLSMode, "dev", dev)
		switch panel.TLSMode {
		case store.TLSSelfSigned:
			errCh <- srv.ListenAndServeTLS(layout.TLSCert(), layout.TLSKey())
		case store.TLSProxy:
			errCh <- srv.ListenAndServe()
		default:
			errCh <- fmt.Errorf("TLS mode %q is not supported yet", panel.TLSMode)
		}
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	case <-restartCh:
		slog.Info("restarting after self-update")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return errRestart
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// errRestart 让进程以 RestartExitCode 退出：unit 的 RestartForceExitStatus 据此重启管理服务。
var errRestart = errors.New("restart requested")

// RestartExitCode 与 proxystack-web.service 的 RestartForceExitStatus 一致。
const RestartExitCode = 75

// setupRuntime 选择运行时实现：Linux 上使用 systemd、journald 和已安装的 xray；
// 开发模式和其他平台使用内存实现，只模拟状态，不启动进程。
func setupRuntime(ctx context.Context, svc *app.Service, dev bool) error {
	if dev || goruntime.GOOS != "linux" {
		if !dev {
			slog.Warn("systemd is only available on Linux; using the in-memory runtime")
		}
		return nil
	}
	// 连接不随 ctx 断开：退出时后台循环还要最后读一次 unit 状态，由 Units.Close 负责关闭。
	manager, err := units.NewSystemd(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	svc.Units = manager
	svc.Logs = units.Journal{}
	svc.XrayAPI = xrayapi.CLI{Binary: runtime.BinaryPath(svc.Layout, units.Xray), TempDir: svc.Layout.RuntimeDir()}
	svc.ProbePorts = true
	return nil
}

// migrateWithBackup 在存在待执行迁移时先用 VACUUM INTO 备份数据库。
func migrateWithBackup(ctx context.Context, st *store.Store, backupsDir string) error {
	current, latest, err := st.MigrationStatus(ctx)
	if err != nil {
		return err
	}
	if current > latest {
		return fmt.Errorf("database schema version %d is newer than this program (%d); upgrade proxystack-web", current, latest)
	}
	if current == latest {
		return nil
	}
	if current > 0 {
		if err := os.MkdirAll(backupsDir, 0o750); err != nil {
			return err
		}
		name := filepath.Join(backupsDir, fmt.Sprintf("pre-migrate-v%d-%s.db", current, time.Now().UTC().Format("20060102T150405Z")))
		if _, err := st.DB.ExecContext(ctx, "VACUUM INTO ?", name); err != nil {
			return fmt.Errorf("backup before migration: %w", err)
		}
		slog.Info("database backed up before migration", "path", name, "from", current, "to", latest)
	}
	return st.Migrate(ctx)
}
