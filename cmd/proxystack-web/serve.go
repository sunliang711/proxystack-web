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
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/eagle/proxystack-web/internal/api"
	"github.com/eagle/proxystack-web/internal/app"
	"github.com/eagle/proxystack-web/internal/store"
	"github.com/eagle/proxystack-web/internal/version"
	"github.com/eagle/proxystack-web/web"
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
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
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
