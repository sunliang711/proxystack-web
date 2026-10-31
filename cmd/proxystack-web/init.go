package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"proxystack-web/internal/app"
	"proxystack-web/internal/store"
)

func newInitCommand(g *globalFlags) *cobra.Command {
	var opts app.InitOptions
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the database, the initial admin password and the panel certificate",
		Long: "init creates the directory tree and database, generates a random admin password, a random panel path " +
			"and a self-signed certificate. The password is printed once to this terminal and only its hash is stored.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout := g.layout()
			for _, dir := range layout.Dirs() {
				if err := os.MkdirAll(dir, 0o750); err != nil {
					return err
				}
			}
			st, err := store.Open(layout.DBPath())
			if err != nil {
				return err
			}
			defer st.Close()
			res, err := app.New(st, layout).Initialize(cmd.Context(), opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "proxystack-web initialized.")
			fmt.Fprintf(out, "  Panel URL:  %s\n", res.PanelURL)
			fmt.Fprintf(out, "  Username:   %s\n", res.Username)
			fmt.Fprintf(out, "  Password:   %s\n", res.Password)
			if res.Fingerprint != "" {
				fmt.Fprintf(out, "  TLS SHA-256 fingerprint:\n    %s\n", res.Fingerprint)
			}
			fmt.Fprintln(out, "The password is shown only once. You will be asked to change it after the first login.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Listen, "listen", "", "panel listen address (default 0.0.0.0, or 127.0.0.1 with --tls proxy)")
	f.IntVar(&opts.Port, "port", 9443, "panel port")
	f.StringVar(&opts.TLSMode, "tls", store.TLSSelfSigned, "TLS mode: self-signed | acme | proxy")
	f.StringVar(&opts.Domain, "domain", "", "panel domain (certificate SAN, ACME domain and external host)")
	f.StringVar(&opts.Email, "email", "", "ACME account email")
	return cmd
}
