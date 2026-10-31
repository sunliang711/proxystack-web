// Package xrayapi 通过已安装 xray 二进制的 api 子命令修改运行中实例的用户。
//
// 不直接引入 xray-core 的 gRPC stub：xray-core 的依赖树对管理服务来说太重，
// 编译期固定的 protobuf 版本也会和实际安装的 xray 版本脱节。调用同一个二进制
// 自带的 api 子命令天然与运行中的实例同版本。
package xrayapi

import (
	"bytes"
	"context"
	"encoding/json"
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

// API 操作运行中的 xray 实例：热增删入站用户、读取流量计数。server 为 API 监听地址（127.0.0.1:端口）。
type API interface {
	RemoveUser(ctx context.Context, server, inboundTag, email string) error
	AddUser(ctx context.Context, server, inboundPatch string) error
	// QueryStats 读取全部计数并清零，返回自上次读取以来的增量。
	QueryStats(ctx context.Context, server string) ([]Stat, error)
}

// Stat 是一条 xray 流量计数。
type Stat struct {
	Name  string
	Value int64
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

func (c CLI) QueryStats(ctx context.Context, server string) ([]Stat, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout+2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, c.Binary, "api", "statsquery", "--server="+server, "--timeout=3", "-reset=true")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("xray api statsquery failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return ParseStats(stdout.Bytes())
}

// ParseStats 解析 statsquery 的 JSON 输出。value 可能是数字，也可能是字符串（protobuf 的 int64 编码），计数为 0 时可能省略。
func ParseStats(output []byte) ([]Stat, error) {
	var response struct {
		Stat []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
		} `json:"stat"`
	}
	if len(bytes.TrimSpace(output)) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("invalid statsquery output: %w", err)
	}
	stats := make([]Stat, 0, len(response.Stat))
	for _, item := range response.Stat {
		text := strings.Trim(string(item.Value), `"`)
		if text == "" {
			stats = append(stats, Stat{Name: item.Name})
			continue
		}
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value for counter %s: %s", item.Name, text)
		}
		stats = append(stats, Stat{Name: item.Name, Value: value})
	}
	return stats, nil
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
// Stats 是各 server 上待读取的计数，读取后清空，与 xray 的 reset 行为一致。
type Fake struct {
	mu    sync.Mutex
	Calls []string
	Fail  error
	Stats map[string][]Stat
}

// AddStats 给 server 追加待读取的计数。
func (f *Fake) AddStats(server string, stats ...Stat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Stats == nil {
		f.Stats = map[string][]Stat{}
	}
	f.Stats[server] = append(f.Stats[server], stats...)
}

func (f *Fake) QueryStats(_ context.Context, server string) ([]Stat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return nil, f.Fail
	}
	stats := f.Stats[server]
	delete(f.Stats, server)
	return stats, nil
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
