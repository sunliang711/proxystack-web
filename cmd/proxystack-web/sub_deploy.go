package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"proxystack-web/internal/subdeploy"
	"proxystack-web/internal/subserver"
)

func newSubDeployCommand(layout func() subserver.Layout) *cobra.Command {
	cmd := &cobra.Command{Use: "deploy", Short: "Install, update, inspect, roll back, or remove the subscription server"}
	cmd.AddCommand(
		newSubDeployInspectCommand(layout),
		newSubDeployApplyCommand(layout),
		newSubDeployRollbackCommand(layout),
		newSubDeployUninstallCommand(layout),
	)
	return cmd
}

func newSubDeployInspectCommand(layout func() subserver.Layout) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect the subscription server installation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := subdeploy.Inspect(cmd.Context(), layout().Dir)
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "State: %s\nVersion: %s\nPrevious: %s\nService active: %t\nService enabled: %t\n",
				status.State, status.Version, status.PreviousVersion, status.ServiceActive, status.ServiceEnabled)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON")
	return cmd
}

func newSubDeployApplyCommand(layout func() subserver.Layout) *cobra.Command {
	var source, publicKey, listen, expectedSHA256 string
	var port int
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Install or update the subscription server using this binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := subdeploy.Apply(cmd.Context(), subdeploy.ApplyOptions{
				Dir: layout().Dir, Source: source, PublicKey: publicKey, Listen: listen, Port: port, ExpectedSHA256: expectedSHA256,
			})
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Subscription server %s: %s\n", result.Action, result.Version)
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "primary subscription source name (required for first installation)")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "primary source public key (required for first installation)")
	cmd.Flags().StringVar(&listen, "listen", "", "listen address for first installation (default 127.0.0.1)")
	cmd.Flags().IntVar(&port, "port", 0, "listen port for first installation (default 9444)")
	cmd.Flags().StringVar(&expectedSHA256, "sha256", "", "expected SHA-256 of this binary")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON")
	return cmd
}

func newSubDeployRollbackCommand(layout func() subserver.Layout) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Switch to the previous subscription server binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := subdeploy.Rollback(cmd.Context(), layout().Dir)
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Subscription server rolled back to %s\n", result.Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON")
	return cmd
}

func newSubDeployUninstallCommand(layout func() subserver.Layout) *cobra.Command {
	var purge, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the subscription service and optionally its data",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := subdeploy.Uninstall(cmd.Context(), layout().Dir, purge)
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Subscription server %s\n", result.Action)
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove configuration, imported bundles, and the service account")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON")
	return cmd
}
