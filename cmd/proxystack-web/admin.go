package main

import (
	"bufio"
	"fmt"
	"net/url"

	"strings"

	"github.com/spf13/cobra"

	"proxystack-web/internal/app"
)

func newAdminCommand(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Rescue commands for the admin account (run as root or the service account)",
	}
	cmd.AddCommand(newResetPasswordCommand(g), newResetLinkCommand(g), newSetListenCommand(g))
	return cmd
}

// newSetListenCommand 在本机修改面板监听，用于网页上改错地址、连不上面板时恢复。
func newSetListenCommand(g *globalFlags) *cobra.Command {
	var listen string
	var port int
	cmd := &cobra.Command{
		Use:   "set-listen",
		Short: "Change the panel listen address or port (takes effect after restarting the service)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if listen == "" && port == 0 {
				return fmt.Errorf("give --listen, --port or both")
			}
			svc, closeFn, err := openService(cmd.Context(), g.layout())
			if err != nil {
				return err
			}
			defer closeFn()
			info, err := svc.Info(cmd.Context())
			if err != nil {
				return err
			}
			target := app.PanelListen{Listen: info.Panel.Listen, Port: info.Panel.Port}
			if listen != "" {
				target.Listen = listen
			}
			if port != 0 {
				target.Port = port
			}
			if err := svc.UpdatePanelListen(cmd.Context(), app.Actor{Name: "cli"}, target); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Panel listen address set to %s:%d. Run `systemctl restart proxystack-web` to apply it.\n", target.Listen, target.Port)
			return nil
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "listen address")
	cmd.Flags().IntVar(&port, "port", 0, "listen port")
	return cmd
}

func newResetPasswordCommand(g *globalFlags) *cobra.Command {
	var opts app.ResetPasswordOptions
	var fromStdin bool
	cmd := &cobra.Command{
		Use:   "reset-password",
		Short: "Set a new admin password and revoke all sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if fromStdin {
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && line == "" {
					return fmt.Errorf("read password from stdin: %w", err)
				}
				opts.Password = strings.TrimRight(line, "\r\n")
			}
			svc, closeFn, err := openService(cmd.Context(), g.layout())
			if err != nil {
				return err
			}
			defer closeFn()
			password, err := svc.ResetPassword(cmd.Context(), opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if fromStdin {
				fmt.Fprintln(out, "Password updated. All sessions have been revoked.")
			} else {
				fmt.Fprintf(out, "New password: %s\n", password)
				fmt.Fprintln(out, "All sessions have been revoked. You will be asked to change the password after logging in.")
			}
			if opts.DisableTOTP {
				fmt.Fprintln(out, "Two-factor authentication has been disabled.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Username, "username", "", "admin username (default: the first admin)")
	cmd.Flags().BoolVar(&fromStdin, "password-stdin", false, "read the new password from stdin instead of generating one")
	cmd.Flags().BoolVar(&opts.DisableTOTP, "disable-totp", false, "also disable two-factor authentication")
	return cmd
}

func newResetLinkCommand(g *globalFlags) *cobra.Command {
	var username string
	var disableTOTP bool
	cmd := &cobra.Command{
		Use:   "reset-link",
		Short: "Print a one-time password reset link valid for 15 minutes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeFn, err := openService(cmd.Context(), g.layout())
			if err != nil {
				return err
			}
			defer closeFn()
			token, err := svc.CreateResetLink(cmd.Context(), username, disableTOTP)
			if err != nil {
				return err
			}
			info, err := svc.Info(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Reset link (valid for %s, single use):\n  %sreset?token=%s\n",
				app.ResetLinkTTL, info.PanelURL, url.QueryEscape(token))
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "admin username (default: the first admin)")
	cmd.Flags().BoolVar(&disableTOTP, "disable-totp", false, "also disable two-factor authentication")
	return cmd
}
