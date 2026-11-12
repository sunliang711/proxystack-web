package units

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
)

// Systemd 通过系统总线操作 unit。启停权限由安装脚本写入的 polkit 规则授予。
type Systemd struct {
	conn *dbus.Conn
}

// NewSystemd 连接系统总线。
func NewSystemd(ctx context.Context) (*Systemd, error) {
	conn, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to systemd: %w", err)
	}
	return &Systemd{conn: conn}, nil
}

// Close 关闭总线连接。
func (s *Systemd) Close() { s.conn.Close() }

func (s *Systemd) Start(ctx context.Context, unit string) error {
	return s.wait(ctx, unit, "start", func(ch chan<- string) (int, error) {
		return s.conn.StartUnitContext(ctx, unit, "replace", ch)
	})
}

func (s *Systemd) Stop(ctx context.Context, unit string) error {
	return s.wait(ctx, unit, "stop", func(ch chan<- string) (int, error) {
		return s.conn.StopUnitContext(ctx, unit, "replace", ch)
	})
}

func (s *Systemd) Restart(ctx context.Context, unit string) error {
	return s.wait(ctx, unit, "restart", func(ch chan<- string) (int, error) {
		return s.conn.RestartUnitContext(ctx, unit, "replace", ch)
	})
}

// wait 提交 job 并等待 systemd 报告结果；结果不是 done 时视为失败。
func (s *Systemd) wait(ctx context.Context, unit, verb string, submit func(chan<- string) (int, error)) error {
	ch := make(chan string, 1)
	if _, err := submit(ch); err != nil {
		return fmt.Errorf("%s %s: %w", verb, unit, err)
	}
	select {
	case result := <-ch:
		if result != "done" {
			return fmt.Errorf("%s %s: job %s", verb, unit, result)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%s %s: %w", verb, unit, ctx.Err())
	}
}

// States 查询 unit 的 ActiveState、SubState 和进入当前状态的时间。
func (s *Systemd) States(ctx context.Context, unitNames []string) (map[string]State, error) {
	states := make(map[string]State, len(unitNames))
	for _, unit := range unitNames {
		props, err := s.conn.GetUnitPropertiesContext(ctx, unit)
		if err != nil {
			return nil, fmt.Errorf("query %s: %w", unit, err)
		}
		state := State{Active: stringProp(props, "ActiveState"), Sub: stringProp(props, "SubState")}
		key := "InactiveEnterTimestamp"
		if state.Running() {
			key = "ActiveEnterTimestamp"
		}
		if usec, ok := props[key].(uint64); ok && usec > 0 {
			state.Since = time.UnixMicro(int64(usec))
		}
		states[unit] = state
	}
	return states, nil
}

func stringProp(props map[string]any, key string) string {
	value, _ := props[key].(string)
	return value
}

// Journal 用 journalctl 读取日志，需要服务账号属于 systemd-journal 组。
type Journal struct{}

// Read 读取最近 lines 行日志，follow 时持续推送。
func (Journal) Read(ctx context.Context, unitNames []string, lines int, follow bool, emit func(LogEntry)) error {
	args := []string{"-o", "json", "--no-pager", "-n", strconv.Itoa(lines)}
	if follow {
		args = append(args, "-f")
	}
	for _, unit := range unitNames {
		args = append(args, "-u", unit)
	}
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start journalctl: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if entry, ok := parseJournalLine(scanner.Bytes()); ok {
			emit(entry)
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// Follow 用 journalctl 从书签之后持续读取日志。
func (Journal) Follow(ctx context.Context, unitNames []string, after string, emit func(LogEntry)) error {
	args := []string{"-o", "json", "--no-pager", "-f"}
	if after != "" {
		args = append(args, "--after-cursor="+after)
	} else {
		args = append(args, "-n", "0")
	}
	for _, unit := range unitNames {
		args = append(args, "-u", unit)
	}
	// 读取出错时先结束 journalctl 再等待它退出：-f 不会自行退出，没人读管道时会一直阻塞在写入上。
	run, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(run, "journalctl", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start journalctl: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if entry, ok := parseJournalLine(scanner.Bytes()); ok {
			emit(entry)
		}
	}
	scanErr := scanner.Err()
	stop()
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if message := strings.TrimSpace(stderr.String()); message != "" {
		return fmt.Errorf("journalctl: %s", message)
	}
	if scanErr != nil {
		return fmt.Errorf("read journalctl: %w", scanErr)
	}
	if err != nil {
		return fmt.Errorf("journalctl: %w", err)
	}
	return fmt.Errorf("journalctl exited")
}

// LogRateLimited 读取各 unit 的 LogRateLimitIntervalUSec 和 LogRateLimitBurst；值为 0 的一项沿用 journald 的全局设置。
func (s *Systemd) LogRateLimited(ctx context.Context, unitNames []string) map[string]bool {
	global := readJournaldRateLimit(journaldConfigFiles())
	out := map[string]bool{}
	for _, unit := range unitNames {
		burstProp, err := s.conn.GetUnitTypePropertyContext(ctx, unit, "Service", "LogRateLimitBurst")
		if err != nil || burstProp == nil {
			continue
		}
		intervalProp, err := s.conn.GetUnitTypePropertyContext(ctx, unit, "Service", "LogRateLimitIntervalUSec")
		if err != nil || intervalProp == nil {
			continue
		}
		burst, ok := burstProp.Value.Value().(uint32)
		if !ok {
			continue
		}
		interval, ok := intervalProp.Value.Value().(uint64)
		if !ok {
			continue
		}
		limit := global
		if interval > 0 {
			limit.intervalZero = false
		}
		if burst > 0 {
			limit.burst = uint64(burst)
		}
		out[unit] = limit.limited()
	}
	return out
}

// journaldRateLimit 是生效的限速设置：间隔为 0 或突发上限为 0 时不限速。
type journaldRateLimit struct {
	intervalZero bool
	burst        uint64
}

func (l journaldRateLimit) limited() bool {
	return !l.intervalZero && l.burst > 0 && l.burst < RateLimitBurstEnough
}

// journaldConfigFiles 按生效顺序返回 journald 的配置文件：主配置在前，drop-in 按文件名排序，同名时 /etc 优先。
func journaldConfigFiles() []string {
	files := []string{"/etc/systemd/journald.conf"}
	dropins := map[string]string{}
	for _, dir := range []string{"/usr/lib/systemd/journald.conf.d", "/run/systemd/journald.conf.d", "/etc/systemd/journald.conf.d"} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.conf"))
		for _, path := range matches {
			dropins[filepath.Base(path)] = path
		}
	}
	names := make([]string, 0, len(dropins))
	for name := range dropins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		files = append(files, dropins[name])
	}
	return files
}

// readJournaldRateLimit 读取 journald 的全局限速（缺省 30 秒内 10000 条），读不到的文件跳过。
func readJournaldRateLimit(files []string) journaldRateLimit {
	limit := journaldRateLimit{burst: 10000}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok || strings.HasPrefix(key, "#") || strings.HasPrefix(key, ";") {
				continue
			}
			value = strings.TrimSpace(value)
			switch strings.TrimSpace(key) {
			case "RateLimitIntervalSec", "RateLimitInterval":
				digits := strings.TrimRightFunc(value, func(r rune) bool { return r < '0' || r > '9' })
				limit.intervalZero = strings.Trim(digits, "0") == "" && digits != ""
			case "RateLimitBurst":
				if burst, err := strconv.ParseUint(value, 10, 64); err == nil {
					limit.burst = burst
				}
			}
		}
	}
	return limit
}

func parseJournalLine(line []byte) (LogEntry, bool) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return LogEntry{}, false
	}
	entry := LogEntry{Priority: 6}
	if value, ok := raw["__REALTIME_TIMESTAMP"].(string); ok {
		if usec, err := strconv.ParseInt(value, 10, 64); err == nil {
			entry.Time = time.UnixMicro(usec)
		}
	}
	for _, key := range []string{"_SYSTEMD_UNIT", "UNIT"} {
		if value, ok := raw[key].(string); ok && value != "" {
			entry.Unit = value
			break
		}
	}
	if value, ok := raw["__CURSOR"].(string); ok {
		entry.Cursor = value
	}
	if value, ok := raw["PRIORITY"].(string); ok {
		entry.Priority, _ = strconv.Atoi(value)
	}
	switch message := raw["MESSAGE"].(type) {
	case string:
		entry.Message = message
	case []any:
		// journald 对非 UTF-8 内容输出字节数组。
		bytes := make([]byte, 0, len(message))
		for _, b := range message {
			if f, ok := b.(float64); ok {
				bytes = append(bytes, byte(f))
			}
		}
		entry.Message = string(bytes)
	}
	return entry, true
}
