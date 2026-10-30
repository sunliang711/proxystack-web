package units

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
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
