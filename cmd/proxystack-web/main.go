// proxystack-web 是代理栈管理服务：Web 管理端、调度器和流量采集器；sub 子命令是独立部署的订阅端。
package main

import (
	"errors"
	"fmt"
	"os"
	_ "time/tzdata" // 流量统计周期按设置时区计算，最小化系统可能没有时区数据。

	"github.com/spf13/cobra"

	"github.com/eagle/proxystack-web/internal/paths"
	"github.com/eagle/proxystack-web/internal/version"
)

type globalFlags struct {
	baseDir string
}

func main() {
	// 与 unit 的 UMask=0027 一致：init 和救援命令在 unit 之外运行，创建的数据库、证书和生成物也不应对其他用户可读。
	setUmask()
	if err := newRootCommand().Execute(); err != nil {
		if errors.Is(err, errRestart) {
			os.Exit(RestartExitCode)
		}
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
	root.AddCommand(serve, newInitCommand(g), newAdminCommand(g), newInfoCommand(g), newOpenAPICommand(), newSubCommand(), newBackupCommand(g), newRestoreCommand(g))
	return root
}

func (g *globalFlags) layout() paths.Layout { return paths.New(g.baseDir) }
