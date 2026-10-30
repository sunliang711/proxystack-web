package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eagle/proxystack-web/internal/paths"
	"github.com/eagle/proxystack-web/internal/units"
)

// ErrBinaryMissing 表示对应组件的二进制尚未安装，无法做配置检查。
var ErrBinaryMissing = errors.New("binary is not installed")

// Checker 用已安装的二进制检查生成物能否被接受。
type Checker interface {
	Check(ctx context.Context, component, stack string, content []byte) error
}

// BinaryChecker 调用 bin 目录下的 xray（run -test）和 mihomo（-t）。
// Binaries 为空时使用 Layout 的默认路径。
type BinaryChecker struct {
	Layout   paths.Layout
	Binaries map[string]string
}

// BinaryPath 返回组件二进制路径。
func BinaryPath(layout paths.Layout, component string) string {
	return filepath.Join(layout.BinDir(), component)
}

func (c BinaryChecker) Check(ctx context.Context, component, stack string, content []byte) error {
	binary := c.Binaries[component]
	if binary == "" {
		binary = BinaryPath(c.Layout, component)
	}
	if _, err := os.Stat(binary); err != nil {
		return ErrBinaryMissing
	}
	dir := filepath.Join(c.Layout.RuntimeDir(), "check", stack)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	name := "config.yaml"
	if component == units.Xray {
		name = "config.json"
	}
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, content, 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if component == units.Xray {
		cmd = exec.CommandContext(ctx, binary, "run", "-test", "-c", file)
		cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+c.Layout.GeoDir())
	} else {
		cmd = exec.CommandContext(ctx, binary, "-t", "-d", dir, "-f", file)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s rejected the config of %s: %s", component, stack, lastLines(output.String(), 8))
	}
	return nil
}

func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
