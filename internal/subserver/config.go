// Package subserver 是独立运行的订阅端：导入管理端签发的订阅包，对外提供 /s/<token>/<格式> 和健康检查。
// 订阅端不连接管理端，只读取自己目录下的配置和已导入的订阅包。
package subserver

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"proxystack-web/internal/subscription"
)

// DefaultDir 是订阅端的默认目录。
const DefaultDir = "/opt/proxystack-sub"

// Layout 是订阅端目录布局。
type Layout struct {
	Dir string
}

func (l Layout) ConfigPath() string { return filepath.Join(l.Dir, "config.yaml") }
func (l Layout) DataDir() string    { return filepath.Join(l.Dir, "data") }
func (l Layout) InboxDir() string   { return filepath.Join(l.Dir, "inbox") }
func (l Layout) RejectedDir() string {
	return filepath.Join(l.InboxDir(), "rejected")
}

// BundlePath 返回某个来源当前订阅包的存放路径。
func (l Layout) BundlePath(source string) string {
	return filepath.Join(l.DataDir(), source+".bundle")
}

// Config 是订阅端的本机配置：监听地址和受信任的来源。
type Config struct {
	Listen  string         `yaml:"listen"`
	Port    int            `yaml:"port"`
	Sources []SourceConfig `yaml:"sources"`
}

// SourceConfig 是一个受信任的来源（一台管理端）。主来源提供用户、token、模板和设置，其他来源只提供节点。
type SourceConfig struct {
	Name      string `yaml:"name"`
	PublicKey string `yaml:"public_key"`
	Primary   bool   `yaml:"primary,omitempty"`
}

// DefaultConfig 返回默认配置（没有来源）。订阅端由 nginx 反向代理，默认只监听回环地址。
func DefaultConfig() Config {
	return Config{Listen: "127.0.0.1", Port: 9444, Sources: []SourceConfig{}}
}

// Addr 返回监听地址。
func (c Config) Addr() string { return net.JoinHostPort(c.Listen, fmt.Sprint(c.Port)) }

// Validate 校验配置：监听地址、来源名唯一、公钥合法、恰好一个主来源。
func (c Config) Validate() error {
	if net.ParseIP(c.Listen) == nil {
		return fmt.Errorf("listen must be an IP address")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	names := map[string]bool{}
	primaries := 0
	for _, source := range c.Sources {
		if !subscription.ValidSourceName(source.Name) {
			return fmt.Errorf("invalid source name %q", source.Name)
		}
		if names[source.Name] {
			return fmt.Errorf("duplicate source %s", source.Name)
		}
		names[source.Name] = true
		if _, err := subscription.ParsePublicKey(source.PublicKey); err != nil {
			return fmt.Errorf("source %s: %w", source.Name, err)
		}
		if source.Primary {
			primaries++
		}
	}
	if len(c.Sources) > 0 && primaries != 1 {
		return errors.New("exactly one source must be primary")
	}
	return nil
}

// Source 返回来源配置。
func (c Config) Source(name string) (SourceConfig, bool) {
	for _, source := range c.Sources {
		if source.Name == name {
			return source, true
		}
	}
	return SourceConfig{}, false
}

// TrustedKey 返回来源的公钥；未配置的来源返回错误。
func (c Config) TrustedKey(name string) (ed25519.PublicKey, error) {
	source, ok := c.Source(name)
	if !ok {
		return nil, fmt.Errorf("source %q is not trusted by this subscription server; add it with `proxystack-web sub source add`", name)
	}
	return subscription.ParsePublicKey(source.PublicKey)
}

// LoadConfig 读取并校验配置，未知字段报错。
func LoadConfig(layout Layout) (Config, error) {
	data, err := os.ReadFile(layout.ConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("%s does not exist; run `proxystack-web sub init` first", layout.ConfigPath())
		}
		return Config{}, err
	}
	config := DefaultConfig()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("%s: %w", layout.ConfigPath(), err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", layout.ConfigPath(), err)
	}
	return config, nil
}

// SaveConfig 校验后原子写入配置。
func SaveConfig(layout Layout, config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	header := []byte("# proxystack-web 订阅端配置。修改监听地址或端口后需要重启 proxystack-sub 服务；来源变化自动生效。\n")
	return writeFileAtomic(layout.ConfigPath(), append(header, data...), 0o640)
}

// writeFileAtomic 写临时文件后改名；以 root 执行时把属主改为所在目录的属主。
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	cleanup := func(err error) error {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return cleanup(err)
	}
	if _, err := file.Write(data); err != nil {
		return cleanup(err)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		return cleanup(err)
	}
	matchOwner(tmp, dir)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
