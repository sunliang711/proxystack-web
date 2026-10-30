package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/eagle/proxystack-web/internal/api"
	"github.com/eagle/proxystack-web/internal/version"
)

func newInfoCommand(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show the panel URL and certificate fingerprint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeFn, err := openService(cmd.Context(), g.layout())
			if err != nil {
				return err
			}
			defer closeFn()
			info, err := svc.Info(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Version:    %s\n", version.Version)
			fmt.Fprintf(out, "Base dir:   %s\n", g.baseDir)
			fmt.Fprintf(out, "Panel URL:  %s\n", info.PanelURL)
			fmt.Fprintf(out, "TLS mode:   %s\n", info.Panel.TLSMode)
			if info.Fingerprint != "" {
				fmt.Fprintf(out, "TLS SHA-256 fingerprint:\n  %s\n", info.Fingerprint)
			}
			return nil
		},
	}
}

func newOpenAPICommand() *cobra.Command {
	return &cobra.Command{
		Use:    "openapi",
		Short:  "Print the OpenAPI document (used to generate frontend types)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(api.OpenAPI())
		},
	}
}
