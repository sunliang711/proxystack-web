package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"proxystack-web/internal/api"
	"proxystack-web/internal/version"
)

func newInfoCommand(g *globalFlags) *cobra.Command {
	var subKey bool
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show the panel URL and subscription bundle public key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeFn, err := openService(cmd.Context(), g.layout())
			if err != nil {
				return err
			}
			defer closeFn()
			source, publicKey, err := svc.SubscriptionIdentity(cmd.Context())
			if err != nil {
				return err
			}
			if subKey {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", source, publicKey)
				return nil
			}
			info, err := svc.Info(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Version:    %s\n", version.Current().Version)
			fmt.Fprintf(out, "Base dir:   %s\n", g.baseDir)
			fmt.Fprintf(out, "Panel URL:  %s\n", info.PanelURL)
			fmt.Fprintf(out, "Subscription source:     %s\n", source)
			fmt.Fprintf(out, "Subscription public key: %s\n", publicKey)
			return nil
		},
	}
	cmd.Flags().BoolVar(&subKey, "sub-key", false, "print only \"SOURCE PUBLIC_KEY\" for configuring a subscription server")
	return cmd
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
