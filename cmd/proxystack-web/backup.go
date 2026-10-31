package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"proxystack-web/internal/app"
)

// readPassphrase 读取口令：stdin 是终端时不回显地提示输入，否则读取一行。
func readPassphrase(cmd *cobra.Command, prompt string) (string, error) {
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		fmt.Fprint(cmd.ErrOrStderr(), prompt)
		data, err := term.ReadPassword(fd)
		fmt.Fprintln(cmd.ErrOrStderr())
		return string(data), err
	}
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func newBackupCommand(g *globalFlags) *cobra.Command {
	var output string
	var req app.ExportRequest
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Write a migration package with the whole database",
		Long: "backup writes a migration package: a consistent database snapshot. " +
			"The package is encrypted with a passphrase unless --plain is given. Traffic the running service has not yet " +
			"written to the database is not included; use the panel to export while the service runs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !req.Plain {
				passphrase, err := readPassphrase(cmd, "Passphrase: ")
				if err != nil {
					return err
				}
				req.Passphrase = passphrase
			}
			svc, closeFn, err := openService(cmd.Context(), g.layout())
			if err != nil {
				return err
			}
			defer closeFn()
			dest, err := filepath.Abs(output)
			if err != nil {
				return err
			}
			return svc.WritePackage(cmd.Context(), dest, req, func(format string, args ...any) {
				fmt.Fprintf(cmd.OutOrStdout(), format+"\n", args...)
			})
		},
	}
	cmd.Flags().StringVar(&output, "output", "", "package file to write")
	_ = cmd.MarkFlagRequired("output")
	cmd.Flags().BoolVar(&req.Plain, "plain", false, "do not encrypt: the package holds every credential in clear text")
	return cmd
}

func newRestoreCommand(g *globalFlags) *cobra.Command {
	var fresh, noStart, encrypted bool
	var opts app.RestoreOptions
	cmd := &cobra.Command{
		Use:   "restore FILE",
		Short: "Restore a migration package",
		Long: "restore replaces the database with the one in a migration package. On an installed instance the current " +
			"database is backed up first and the package takes effect when the service next starts. With --fresh it " +
			"initializes an empty installation directory (install.sh --restore).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.StartProxies = !noStart
			layout := g.layout()
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer file.Close()
			passphrase := ""
			if encrypted {
				if passphrase, err = readPassphrase(cmd, "Passphrase: "); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			if fresh {
				for _, dir := range layout.Dirs() {
					if err := os.MkdirAll(dir, 0o750); err != nil {
						return err
					}
				}
				manifest, err := app.RestoreFresh(cmd.Context(), layout, file, passphrase, opts)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "Restored package from %s, exported %s.\n", manifest.Hostname, manifest.ExportedAt.Format("2006-01-02 15:04:05 MST"))
				svc, closeFn, err := openService(cmd.Context(), layout)
				if err != nil {
					return err
				}
				defer closeFn()
				info, err := svc.Info(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "  Panel URL:  %s\n", info.PanelURL)
				fmt.Fprintln(out, "Log in with the administrator account of the source instance.")
				return nil
			}
			svc, closeFn, err := openService(cmd.Context(), layout)
			if err != nil {
				return err
			}
			defer closeFn()
			preview, err := svc.StageRestore(cmd.Context(), file, passphrase)
			if err != nil {
				var field *app.FieldError
				if errors.As(err, &field) {
					return errors.New(field.Message)
				}
				return err
			}
			for _, conflict := range preview.PortConflicts {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", conflict)
			}
			if err := svc.StageRestoreForNextStart(cmd.Context(), opts); err != nil {
				return err
			}
			fmt.Fprintf(out, "Package from %s staged; the current database was backed up.\n", preview.Hostname)
			fmt.Fprintln(out, "Run `systemctl restart proxystack-web` to finish restoring.")
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&fresh, "fresh", false, "initialize an empty installation directory from the package")
	f.BoolVar(&encrypted, "encrypted", true, "the package is encrypted: read the passphrase")
	f.StringVar(&opts.ExternalHost, "external-host", "", "replace the external host of the source instance")
	f.BoolVar(&opts.KeepPanel, "keep-panel", false, "keep this instance's panel settings")
	f.BoolVar(&opts.KeepAdmins, "keep-admins", false, "keep this instance's administrator accounts")
	f.BoolVar(&noStart, "no-start", false, "do not start stacks after restoring")
	return cmd
}
