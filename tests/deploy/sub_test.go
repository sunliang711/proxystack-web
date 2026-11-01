//go:build e2e

// Package deploy 在 tests/deploy/run.sh 创建的隔离 systemd 容器中验证安装脚本。
package deploy

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/generator/sub"
	"proxystack-web/internal/subscription"
)

const (
	newDir    = "/opt/proxystack-web-sub"
	oldDir    = "/opt/proxystack-sub"
	newUnit   = "proxystack-web-sub.service"
	oldUnit   = "proxystack-sub.service"
	deployDir = "/root/sub-isolation-deploy"
	binary    = "/root/sub-isolation-binary"
)

type machine struct {
	t    *testing.T
	name string
}

func (m machine) run(args ...string) (string, error) {
	m.t.Helper()
	out, err := exec.Command("docker", append([]string{"exec", m.name}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (m machine) must(args ...string) string {
	m.t.Helper()
	out, err := m.run(args...)
	require.NoError(m.t, err, "%v\n%s", args, out)
	return out
}

func (m machine) put(path string, data []byte, mode os.FileMode) {
	m.t.Helper()
	local := filepath.Join(m.t.TempDir(), "file")
	require.NoError(m.t, os.WriteFile(local, data, mode))
	out, err := exec.Command("docker", "cp", local, m.name+":"+path).CombinedOutput()
	require.NoError(m.t, err, "%s", out)
}

func (m machine) install(options ...string) (string, error) {
	m.t.Helper()
	return m.run(append([]string{"bash", deployDir + "/install.sh", "--role", "sub", "--binary", binary}, options...)...)
}

func (m machine) expectInstallFailure(message string, options ...string) {
	m.t.Helper()
	out, err := m.install(options...)
	require.Error(m.t, err, out)
	require.Contains(m.t, out, message)
}

func (m machine) active(unit string) {
	m.t.Helper()
	require.Equal(m.t, "active", m.must("systemctl", "is-active", unit))
}

func (m machine) subscriptionHash() string {
	m.t.Helper()
	m.must("curl", "--retry", "10", "--retry-connrefused", "--retry-delay", "1", "-fs", "-o", "/root/sub-test-response", "http://127.0.0.1:19444/s/migration-test-token/clash")
	return m.must("sha256sum", "/root/sub-test-response")
}

// fixture 签发真实订阅包，用迁移前后的订阅 HTTP 内容验证 token、公钥和包数据没有变化。
func fixture(t *testing.T) (string, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	snapshot := &subscription.Snapshot{
		Source: "main", Sequence: 1, GeneratedAt: time.Now().UTC(),
		Settings: subscription.Settings{DisabledResponse: subscription.DisabledNotFound},
		Users:    []subscription.User{{Name: "alice", Token: "migration-test-token", Enabled: true}},
		Nodes: []sub.Node{{ID: "edge:alice", User: "alice", Protocol: "socks5", Server: "198.51.100.1", Port: 4301,
			Tag: "edge", Remark: "migration test", Auth: &sub.Auth{Type: "noauth"}}},
	}
	bundle, err := subscription.Seal(snapshot, priv)
	require.NoError(t, err)
	return subscription.FormatPublicKey(pub), bundle
}

func TestSubscriptionInstall(t *testing.T) {
	name := os.Getenv("PSW_DEPLOY_CONTAINER")
	if name == "" {
		t.Skip("run tests/deploy/run.sh to create an isolated systemd container")
	}
	m := machine{t: t, name: name}
	key, bundle := fixture(t)
	var pssubConfig, pssubService string
	t.Run("与 pssub 共存且不能将其迁移", func(t *testing.T) {
		m.t = t
		m.must("mkdir", "-p", oldDir)
		m.put(oldDir+"/config.yaml", []byte("listen: 127.0.0.1:3003\naccess:\n  type: none\n"), 0o640)
		m.put("/usr/local/bin/pssub", []byte("#!/bin/sh\nexec sleep infinity\n"), 0o755)
		m.put("/etc/systemd/system/"+oldUnit, []byte("[Service]\nExecStart=/usr/local/bin/pssub --base-dir /opt/proxystack-sub serve\n[Install]\nWantedBy=multi-user.target\n"), 0o644)
		m.must("systemctl", "daemon-reload")
		m.must("systemctl", "enable", "--now", oldUnit)
		pssubConfig = m.must("sha256sum", oldDir+"/config.yaml")
		pssubService = m.must("sha256sum", "/etc/systemd/system/"+oldUnit)
		m.expectInstallFailure("pssub cannot be migrated", "--migrate-sub")
		out, err := m.install("--public-key", key)
		require.NoError(t, err, out)
		m.active(newUnit)
		m.active(oldUnit)
		require.Contains(t, m.must(binary, "sub", "config"), "127.0.0.1:9444")
		require.Equal(t, pssubConfig, m.must("sha256sum", oldDir+"/config.yaml"))
		require.Equal(t, pssubService, m.must("sha256sum", "/etc/systemd/system/"+oldUnit))
	})
	t.Run("账号目录异常时拒绝升级或卸载", func(t *testing.T) {
		m.t = t
		m.must("systemctl", "stop", newUnit)
		m.must("usermod", "--home", "/opt/unrelated-test-home", "proxystack-web-sub")
		m.expectInstallFailure("unexpected home directory")
		out, err := m.run("bash", deployDir+"/uninstall.sh", "--role", "sub", "--purge")
		require.Error(t, err, out)
		require.Contains(t, out, "unexpected home directory")
		state, err := m.run("systemctl", "is-active", newUnit)
		require.Error(t, err)
		require.Equal(t, "inactive", state)
		m.must("test", "-f", newDir+"/config.yaml")
		m.must("usermod", "--home", newDir, "proxystack-web-sub")
		m.must("systemctl", "start", newUnit)
	})
	t.Run("升级及卸载重装保持订阅包和配置", func(t *testing.T) {
		m.t = t
		m.put("/root/main.bundle", bundle, 0o640)
		m.must(binary, "sub", "--dir", newDir, "import", "/root/main.bundle")
		config := m.must("sha256sum", newDir+"/config.yaml")
		data := m.must("sha256sum", newDir+"/data/main.bundle")
		out, err := m.install()
		require.NoError(t, err, out)
		require.Equal(t, config, m.must("sha256sum", newDir+"/config.yaml"))
		require.Equal(t, data, m.must("sha256sum", newDir+"/data/main.bundle"))
		m.must("bash", deployDir+"/uninstall.sh", "--role", "sub")
		m.must("test", "-f", newDir+"/.proxystack-web-sub")
		out, err = m.install()
		require.NoError(t, err, out)
		m.active(newUnit)
		require.Equal(t, data, m.must("sha256sum", newDir+"/data/main.bundle"))
		m.must("bash", deployDir+"/uninstall.sh", "--role", "sub")
		m.must("bash", deployDir+"/uninstall.sh", "--role", "sub", "--purge")
		m.must("test", "!", "-e", newDir)
		m.active(oldUnit)
		require.Equal(t, pssubConfig, m.must("sha256sum", oldDir+"/config.yaml"))
		require.Equal(t, pssubService, m.must("sha256sum", "/etc/systemd/system/"+oldUnit))
	})
	t.Run("未知目录和符号链接不能被安装或删除", func(t *testing.T) {
		m.t = t
		m.must("mkdir", newDir)
		m.put(newDir+"/config.yaml", []byte("listen: 127.0.0.1:3003\n"), 0o640)
		m.expectInstallFailure("cannot confirm ownership", "--public-key", key)
		out, err := m.run("bash", deployDir+"/uninstall.sh", "--role", "sub", "--purge")
		require.Error(t, err, out)
		require.Contains(t, out, "cannot confirm ownership")
		m.must("test", "-f", newDir+"/config.yaml")
		m.must("rm", "-r", newDir)
		m.must("ln", "-s", oldDir, newDir)
		m.expectInstallFailure("refusing symbolic link", "--public-key", key)
		out, err = m.run("bash", deployDir+"/uninstall.sh", "--role", "sub", "--purge")
		require.Error(t, err, out)
		require.Contains(t, out, "refusing symbolic link")
		m.must("rm", newDir)
		m.active(oldUnit)
	})
	t.Run("同名服务执行其他程序时拒绝覆盖或卸载", func(t *testing.T) {
		m.t = t
		m.put("/etc/systemd/system/"+newUnit, []byte("[Service]\nExecStart=/usr/local/bin/pssub serve\n"), 0o644)
		m.must("systemctl", "daemon-reload")
		m.expectInstallFailure("belongs to another installation", "--public-key", key)
		out, err := m.run("bash", deployDir+"/uninstall.sh", "--role", "sub", "--purge")
		require.Error(t, err, out)
		require.Contains(t, out, "belongs to another installation")
		m.must("rm", "/etc/systemd/system/"+newUnit)
		m.must("systemctl", "daemon-reload")
	})
	var before, oldConfig string
	t.Run("旧版安装必须显式迁移", func(t *testing.T) {
		m.t = t
		// 前面已验证 pssub 未被触碰，此处仅清理容器内的测试替身，换成本项目旧版布局。
		m.must("systemctl", "disable", "--now", oldUnit)
		m.must("rm", "-r", oldDir)
		m.must("mkdir", "-p", oldDir+"/bin", oldDir+"/inbox/rejected")
		m.must("cp", binary, oldDir+"/bin/proxystack-web")
		m.must(binary, "sub", "--dir", oldDir, "init", "--public-key", key, "--port", "19444")
		m.must(binary, "sub", "--dir", oldDir, "import", "/root/main.bundle")
		m.put(oldDir+"/inbox/next.bundle.part", []byte("pending upload"), 0o640)
		m.put(oldDir+"/inbox/rejected/invalid.bundle", []byte("rejected upload"), 0o640)
		m.put("/etc/systemd/system/"+oldUnit, []byte("[Service]\nExecStart="+oldDir+"/bin/proxystack-web sub --dir "+oldDir+" serve\n[Install]\nWantedBy=multi-user.target\n"), 0o644)
		m.must("ln", "-sfn", oldDir+"/bin/proxystack-web", "/usr/local/bin/proxystack-web")
		m.must("systemctl", "daemon-reload")
		m.must("systemctl", "enable", "--now", oldUnit)
		before = m.subscriptionHash()
		oldConfig = m.must("cat", oldDir+"/config.yaml")
		m.expectInstallFailure("--migrate-sub first", "--public-key", key)
		m.expectInstallFailure("preserves the existing configuration", "--migrate-sub", "--sub-port", "19445")
	})
	t.Run("命令相同但配置属于 pssub 时拒绝迁移", func(t *testing.T) {
		m.t = t
		m.put(oldDir+"/config.yaml", []byte("listen: 127.0.0.1:3003\naccess:\n  type: none\n"), 0o640)
		m.expectInstallFailure("not a valid proxystack-web subscription configuration", "--migrate-sub")
		m.active(oldUnit)
		m.must("test", "!", "-e", newDir)
		m.put(oldDir+"/config.yaml", []byte(oldConfig+"\n"), 0o640)
	})
	for _, failure := range []struct {
		name    string
		command string
		active  bool
	}{
		{"新进程失败恢复运行中的旧服务", "/bin/false", true},
		{"HTTP 检查失败保留旧服务的停用状态", "/bin/sleep infinity", false},
	} {
		t.Run(failure.name, func(t *testing.T) {
			m.t = t
			if !failure.active {
				m.must("systemctl", "disable", "--now", oldUnit)
			}
			unitFile := deployDir + "/systemd/" + newUnit
			m.must("cp", unitFile, unitFile+".saved")
			m.put(unitFile, []byte("[Service]\nExecStart="+failure.command+"\n[Install]\nWantedBy=multi-user.target\n"), 0o644)
			m.expectInstallFailure("restoring the old subscription service", "--migrate-sub")
			m.must("mv", unitFile+".saved", unitFile)
			if failure.active {
				m.active(oldUnit)
				require.Equal(t, "enabled", m.must("systemctl", "is-enabled", oldUnit))
			} else {
				state, err := m.run("systemctl", "is-active", oldUnit)
				require.Error(t, err)
				require.Equal(t, "inactive", state)
				state, err = m.run("systemctl", "is-enabled", oldUnit)
				require.Error(t, err)
				require.Equal(t, "disabled", state)
				m.must("systemctl", "enable", "--now", oldUnit)
			}
			require.Equal(t, oldConfig, m.must("cat", oldDir+"/config.yaml"))
			m.must("test", "!", "-e", newDir)
			require.Equal(t, oldDir+"/bin/proxystack-web", m.must("readlink", "/usr/local/bin/proxystack-web"))
			require.Equal(t, before, m.subscriptionHash())
		})
	}
	t.Run("迁移保留订阅链接配置和收件目录并备份旧安装", func(t *testing.T) {
		m.t = t
		out, err := m.install("--migrate-sub")
		require.NoError(t, err, out)
		require.Contains(t, out, "migration complete")
		m.active(newUnit)
		require.Equal(t, oldConfig, m.must("cat", newDir+"/config.yaml"))
		require.Equal(t, before, m.subscriptionHash())
		require.Equal(t, "pending upload", m.must("cat", newDir+"/inbox/next.bundle.part"))
		require.Equal(t, "rejected upload", m.must("cat", newDir+"/inbox/rejected/invalid.bundle"))
		require.Equal(t, "proxystack-web-sub:proxystack-web-sub", m.must("stat", "-c", "%U:%G", newDir+"/data/main.bundle"))
		require.Equal(t, newDir+"/bin/proxystack-web", m.must("readlink", "/usr/local/bin/proxystack-web"))
		m.must("test", "!", "-e", oldDir)
		m.must("test", "!", "-e", "/etc/systemd/system/"+oldUnit)
		_, err = m.run("systemctl", "is-enabled", oldUnit)
		require.Error(t, err)
		backup := strings.Split(strings.Split(out, "migration backup: ")[1], "\n")[0]
		m.must("test", "-f", backup+"/original/config.yaml")
		m.must("test", "-f", backup+"/retired/data/main.bundle")
		m.must("test", "-f", backup+"/"+oldUnit)
		m.must("bash", deployDir+"/uninstall.sh", "--role", "sub", "--purge")
		m.must("test", "-f", backup+"/original/config.yaml")
	})
}
