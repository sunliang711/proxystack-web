// Package paths 定义安装目录 /opt/proxystack-web 下的固定布局。
package paths

import "path/filepath"

// DefaultBase 是生产环境的安装目录。
const DefaultBase = "/opt/proxystack-web"

// Layout 按 base 目录计算各子路径。
type Layout struct {
	Base string
}

func New(base string) Layout { return Layout{Base: base} }

func (l Layout) BinDir() string       { return filepath.Join(l.Base, "bin") }
func (l Layout) GeoDir() string       { return filepath.Join(l.Base, "geo") }
func (l Layout) DataDir() string      { return filepath.Join(l.Base, "data") }
func (l Layout) DBPath() string       { return filepath.Join(l.DataDir(), "proxystack.db") }
func (l Layout) TLSDir() string       { return filepath.Join(l.DataDir(), "tls") }
func (l Layout) TLSCert() string      { return filepath.Join(l.TLSDir(), "cert.pem") }
func (l Layout) TLSKey() string       { return filepath.Join(l.TLSDir(), "key.pem") }
func (l Layout) RuntimeDir() string   { return filepath.Join(l.Base, "runtime") }
func (l Layout) GeneratedDir() string { return filepath.Join(l.RuntimeDir(), "generated") }
func (l Layout) BackupsDir() string   { return filepath.Join(l.Base, "backups") }
func (l Layout) DownloadsDir() string { return filepath.Join(l.Base, "downloads") }

// GeneratedXray 返回某个 stack 的 xray 生成物路径。
func (l Layout) GeneratedXray(stack string) string {
	return filepath.Join(l.GeneratedDir(), "xray", stack+".json")
}

// GeneratedMihomo 返回某个 stack 的 mihomo 生成物路径。
func (l Layout) GeneratedMihomo(stack string) string {
	return filepath.Join(l.GeneratedDir(), "mihomo", stack+".yaml")
}

// MihomoWorkDir 返回某个 stack 的 mihomo 工作目录。
func (l Layout) MihomoWorkDir(stack string) string {
	return filepath.Join(l.RuntimeDir(), "mihomo", stack)
}

// Dirs 返回 init 时需要创建的全部目录。
func (l Layout) Dirs() []string {
	return []string{
		l.BinDir(), l.GeoDir(), l.DataDir(), l.TLSDir(),
		filepath.Join(l.GeneratedDir(), "xray"), filepath.Join(l.GeneratedDir(), "mihomo"),
		filepath.Join(l.RuntimeDir(), "mihomo"), l.BackupsDir(), l.DownloadsDir(),
	}
}
