// Package xrayapi 通过已安装 xray 二进制的 api 子命令修改运行中实例的用户。
//
// 不直接引入 xray-core 的 gRPC stub：xray-core 的依赖树对管理服务来说太重，
// 编译期固定的 protobuf 版本也会和实际安装的 xray 版本脱节。调用同一个二进制
// 自带的 api 子命令天然与运行中的实例同版本。
package xrayapi

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Timeout 是单次 api 调用的等待上限。
const Timeout = 3 * time.Second

// API 热增删运行中 xray 实例的入站用户。server 为 API 监听地址（127.0.0.1:端口）。
type API interface {
	RemoveUser(ctx context.Context, server, inboundTag, email string) error
	AddUser(ctx context.Context, server, inboundPatch string) error
}

// successPattern 匹配 adu/rmu 的结尾统计行。这两个子命令对单个用户失败只打印一行就继续，
// 退出码不能当成功信号，必须读这一行的计数。
var successPattern = regexp.MustCompile(`(?m)^(?:Removed|Added)\s+(\d+)\s+user\(s\) in total\.`)

// CLI 调用 xray 二进制。TempDir 存放带凭据的临时 patch 文件，应位于服务私有目录。
type CLI struct {
	Binary  string
	TempDir string
}

func (c CLI) RemoveUser(ctx context.Context, server, inboundTag, email string) error {
	for label, value := range map[string]string{"inbound tag": inboundTag, "user email": email} {
		if value == "" || strings.HasPrefix(value, "-") {
			return fmt.Errorf("%s must not be empty or start with '-': %q", label, value)
		}
	}
	return c.runCounted(ctx, "api", "rmu", "--server="+server, "--timeout=3", "-tag="+inboundTag, email)
}

func (c CLI) AddUser(ctx context.Context, server, inboundPatch string) error {
	if err := os.MkdirAll(c.TempDir, 0o750); err != nil {
		return err
	}
	file, err := os.CreateTemp(c.TempDir, ".adu-*.json")
	if err != nil {
		return err
	}
	path := file.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := file.WriteString(inboundPatch); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return c.runCounted(ctx, "api", "adu", "--server="+server, "--timeout=3", path)
}

func (c CLI) runCounted(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout+2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, c.Binary, args...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("xray %s %s failed: %w: %s", args[0], args[1], err, strings.TrimSpace(output.String()))
	}
	match := successPattern.FindStringSubmatch(output.String())
	if match == nil {
		return fmt.Errorf("xray %s %s did not report a user count: %s", args[0], args[1], strings.TrimSpace(output.String()))
	}
	if count, _ := strconv.Atoi(match[1]); count < 1 {
		return fmt.Errorf("xray %s %s processed 0 users: %s", args[0], args[1], strings.TrimSpace(output.String()))
	}
	return nil
}

// Fake 记录调用，供开发环境和测试使用。Fail 非空时所有调用返回该错误。
type Fake struct {
	mu    sync.Mutex
	Calls []string
	Fail  error
}

func (f *Fake) RemoveUser(_ context.Context, server, inboundTag, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, fmt.Sprintf("rmu %s %s %s", server, inboundTag, email))
	return f.Fail
}

func (f *Fake) AddUser(_ context.Context, server, inboundPatch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, fmt.Sprintf("adu %s %d bytes", server, len(inboundPatch)))
	return f.Fail
}
