package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"proxystack-web/internal/subscription"
	"proxystack-web/internal/subserver"
)

// newSubCommand 是订阅端的命令组：订阅端与管理端分开部署，只通过管理端签发的订阅包获得数据。
func newSubCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "sub",
		Short: "Standalone subscription server (fed by signed bundles from the panel)",
	}
	defaultDir := subserver.DefaultDir
	if env := os.Getenv("PSW_SUB_DIR"); env != "" {
		defaultDir = env
	}
	cmd.PersistentFlags().StringVar(&dir, "dir", defaultDir, "subscription server directory (env PSW_SUB_DIR)")
	layout := func() subserver.Layout { return subserver.Layout{Dir: dir} }
	cmd.AddCommand(
		newSubServeCommand(layout),
		newSubInitCommand(layout),
		newSubConfigCommand(layout),
		newSubImportCommand(layout),
		newSubStatusCommand(layout),
		newSubSourceCommand(layout),
	)
	return cmd
}

func newSubServeCommand(layout func() subserver.Layout) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve subscriptions and import bundles dropped into the inbox",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			server, err := subserver.New(layout())
			if err != nil {
				return err
			}
			return server.Run(ctx)
		},
	}
}

func newSubInitCommand(layout func() subserver.Layout) *cobra.Command {
	var source, publicKey, listen string
	var port int
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the subscription server directory and configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l := layout()
			if _, err := os.Stat(l.ConfigPath()); err == nil {
				return fmt.Errorf("%s already exists", l.ConfigPath())
			}
			config := subserver.DefaultConfig()
			if listen != "" {
				config.Listen = listen
			}
			if port != 0 {
				config.Port = port
			}
			if publicKey != "" {
				config.Sources = append(config.Sources, subserver.SourceConfig{Name: source, PublicKey: publicKey, Primary: true})
			}
			for _, name := range []string{l.Dir, l.DataDir(), l.InboxDir()} {
				if err := os.MkdirAll(name, 0o750); err != nil {
					return err
				}
			}
			if err := subserver.SaveConfig(l, config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Subscription server initialized: %s (listen %s)\n", l.Dir, config.Addr())
			if publicKey == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "No source is trusted yet; add the panel with `proxystack-web sub source add NAME PUBLIC_KEY --primary`.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "main", "name of the primary source (the panel's source name)")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "public key of the primary source (ed25519:...)")
	cmd.Flags().StringVar(&listen, "listen", "", "listen address (default 127.0.0.1)")
	cmd.Flags().IntVar(&port, "port", 0, "listen port (default 9444)")
	return cmd
}

func newSubConfigCommand(layout func() subserver.Layout) *cobra.Command {
	var listen string
	var port int
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or change the listen address and port (changes take effect after restarting the service)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l := layout()
			config, err := subserver.LoadConfig(l)
			if err != nil {
				return err
			}
			if listen == "" && port == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Listen: %s\n", config.Addr())
				return nil
			}
			if listen != "" {
				config.Listen = listen
			}
			if port != 0 {
				config.Port = port
			}
			if err := subserver.SaveConfig(l, config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Listen address set to %s. Run `systemctl restart proxystack-web-sub` to apply it.\n", config.Addr())
			return nil
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "listen address")
	cmd.Flags().IntVar(&port, "port", 0, "listen port")
	return cmd
}

func newSubImportCommand(layout func() subserver.Layout) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "import FILE",
		Short: "Verify and import a subscription bundle (the running service picks it up within seconds)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l := layout()
			config, err := subserver.LoadConfig(l)
			if err != nil {
				return err
			}
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer file.Close()
			data, err := io.ReadAll(io.LimitReader(file, subscription.MaxBundleSize+1))
			if err != nil {
				return err
			}
			result, err := subserver.Import(l, config, data, force)
			if errors.Is(err, subserver.ErrStale) {
				return fmt.Errorf("%w (use --force after reinstalling the panel)", err)
			}
			if err != nil {
				return err
			}
			snapshot := result.Snapshot
			fmt.Fprintf(cmd.OutOrStdout(), "Imported bundle from source %s: sequence %d (previous %d), generated %s, %d users, %d nodes\n",
				snapshot.Source, snapshot.Sequence, result.Previous, snapshot.GeneratedAt.Local().Format(time.DateTime), len(snapshot.Users), len(snapshot.Nodes))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "accept a bundle whose sequence is not newer than the imported one")
	return cmd
}

func newSubStatusCommand(layout func() subserver.Layout) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show trusted sources and the imported bundles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l := layout()
			config, err := subserver.LoadConfig(l)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Directory: %s\nListen:    %s\n\n", l.Dir, config.Addr())
			states := subserver.LoadSources(l, config)
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "SOURCE\tROLE\tKEY\tSEQUENCE\tGENERATED\tUSERS\tNODES\tERROR")
			for _, state := range states {
				role := "nodes"
				if state.Primary {
					role = "primary"
				}
				source, _ := config.Source(state.Name)
				fingerprint := ""
				if key, err := subscription.ParsePublicKey(source.PublicKey); err == nil {
					fingerprint = subscription.KeyFingerprint(key)
				}
				sequence, generated, users, nodes := "-", "not imported", "-", "-"
				if snapshot := state.Snapshot; snapshot != nil {
					sequence = fmt.Sprint(snapshot.Sequence)
					generated = snapshot.GeneratedAt.Local().Format(time.DateTime)
					users, nodes = fmt.Sprint(len(snapshot.Users)), fmt.Sprint(len(snapshot.Nodes))
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", state.Name, role, fingerprint, sequence, generated, users, nodes, state.Error)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			view, err := subserver.BuildView(states)
			if err != nil {
				fmt.Fprintf(out, "\nSubscriptions are not available: %v\n", err)
				return nil
			}
			for _, warning := range view.Warnings {
				fmt.Fprintf(out, "\nWarning: %s", warning)
			}
			if len(view.Warnings) > 0 {
				fmt.Fprintln(out)
			}
			return nil
		},
	}
}

func newSubSourceCommand(layout func() subserver.Layout) *cobra.Command {
	cmd := &cobra.Command{Use: "source", Short: "Manage trusted sources"}
	var primary bool
	add := &cobra.Command{
		Use:   "add NAME PUBLIC_KEY",
		Short: "Trust a source; --primary makes it the source of users, tokens, templates and settings",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			l := layout()
			config, err := subserver.LoadConfig(l)
			if err != nil {
				return err
			}
			if _, ok := config.Source(args[0]); ok {
				return fmt.Errorf("source %s already exists", args[0])
			}
			if primary {
				for index := range config.Sources {
					config.Sources[index].Primary = false
				}
			}
			isPrimary := primary || len(config.Sources) == 0
			config.Sources = append(config.Sources, subserver.SourceConfig{Name: args[0], PublicKey: args[1], Primary: isPrimary})
			if err := subserver.SaveConfig(l, config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Source %s added (primary: %t)\n", args[0], isPrimary)
			return nil
		},
	}
	add.Flags().BoolVar(&primary, "primary", false, "make this the primary source")
	remove := &cobra.Command{
		Use:   "remove NAME",
		Short: "Stop trusting a source and delete its imported bundle",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l := layout()
			config, err := subserver.LoadConfig(l)
			if err != nil {
				return err
			}
			source, ok := config.Source(args[0])
			if !ok {
				return fmt.Errorf("source %s does not exist", args[0])
			}
			if source.Primary && len(config.Sources) > 1 {
				return errors.New("make another source primary before removing the primary source")
			}
			kept := config.Sources[:0]
			for _, item := range config.Sources {
				if item.Name != args[0] {
					kept = append(kept, item)
				}
			}
			config.Sources = kept
			if err := subserver.SaveConfig(l, config); err != nil {
				return err
			}
			if err := os.Remove(l.BundlePath(args[0])); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Source %s removed\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(add, remove)
	return cmd
}
