// proxystack-web 是代理栈管理服务：Web 管理端、订阅端、调度器和流量采集器。
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/eagle/proxystack-web/internal/paths"
	"github.com/eagle/proxystack-web/internal/version"
)

type globalFlags struct {
	baseDir string
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	g := &globalFlags{}
	serve := newServeCommand(g)
	root := &cobra.Command{
		Use:           "proxystack-web",
		Short:         "Web-managed xray/mihomo proxy stacks",
		Version:       fmt.Sprintf("%s (%s, %s)", version.Version, version.Commit, version.Date),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          serve.RunE,
	}
	defaultBase := paths.DefaultBase
	if env := os.Getenv("PSW_BASE_DIR"); env != "" {
		defaultBase = env
	}
	root.PersistentFlags().StringVar(&g.baseDir, "base-dir", defaultBase, "installation directory (env PSW_BASE_DIR)")
	root.Flags().AddFlagSet(serve.Flags())
	root.AddCommand(serve, newInitCommand(g), newAdminCommand(g), newInfoCommand(g), newOpenAPICommand())
	return root
}

func (g *globalFlags) layout() paths.Layout { return paths.New(g.baseDir) }
