//go:build e2e

// Package deploy 在 tests/deploy/run.sh 创建的隔离 systemd 容器中验证订阅端部署。
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
	subDir    = "/opt/proxystack-web-sub"
	subUnit   = "proxystack-web-sub.service"
	deployDir = "/root/sub-isolation-deploy"
	binaryV1  = "/root/sub-isolation-binary-v1"
	binaryV2  = "/root/sub-isolation-binary-v2"
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

func (m machine) install(binary string, options ...string) (string, error) {
	m.t.Helper()
	args := []string{"bash", deployDir + "/install.sh", "--role", "sub", "--binary", binary}
	return m.run(append(args, options...)...)
}

func (m machine) active() {
	m.t.Helper()
	require.Equal(m.t, "active", m.must("systemctl", "is-active", subUnit))
}

// fixture 签发真实订阅包，验证更新、回滚和普通卸载不会改变配置与数据。
func fixture(t *testing.T) (string, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	snapshot := &subscription.Snapshot{
		Source: "main", Sequence: 1, GeneratedAt: time.Now().UTC(),
		Settings: subscription.Settings{DisabledResponse: subscription.DisabledNotFound},
		Users:    []subscription.User{{Name: "alice", Token: "deploy-test-token", Enabled: true}},
		Nodes: []sub.Node{{ID: "edge:alice", User: "alice", Protocol: "socks5", Server: "198.51.100.1", Port: 4301,
			Tag: "edge", Remark: "deploy test", Auth: &sub.Auth{Type: "noauth"}}},
	}
	bundle, err := subscription.Seal(snapshot, priv)
	require.NoError(t, err)
	return subscription.FormatPublicKey(pub), bundle
}

func TestSubscriptionDeployment(t *testing.T) {
	name := os.Getenv("PSW_DEPLOY_CONTAINER")
	if name == "" {
		t.Skip("run tests/deploy/run.sh to create an isolated systemd container")
	}
	m := machine{t: t, name: name}
	key, bundle := fixture(t)

	t.Run("未知目录拒绝安装和清理", func(t *testing.T) {
		m.t = t
		m.must("mkdir", subDir)
		m.put(subDir+"/config.yaml", []byte("listen: 127.0.0.1:3003\n"), 0o640)
		out, err := m.install(binaryV1, "--public-key", key)
		require.Error(t, err, out)
		require.Contains(t, out, "cannot confirm ownership")
		out, err = m.run("bash", deployDir+"/uninstall.sh", "--role", "sub", "--purge")
		require.Error(t, err, out)
		require.Contains(t, out, "cannot confirm ownership")
		m.must("rm", "-r", subDir)
	})

	t.Run("离线安装更新回滚和卸载", func(t *testing.T) {
		m.t = t
		out, err := m.install(binaryV1, "--public-key", key, "--sub-port", "19444")
		require.NoError(t, err, out)
		m.active()
		require.Contains(t, m.must(subDir+"/bin/proxystack-web", "--version"), "v0.1.0-test")
		require.Equal(t, "root:proxystack-web-sub", m.must("stat", "-c", "%U:%G", subDir+"/bin/proxystack-web"))
		require.Equal(t, "proxystack-web-sub:proxystack-web-sub", m.must("stat", "-c", "%U:%G", subDir+"/data"))

		m.put("/root/main.bundle", bundle, 0o640)
		m.must(subDir+"/bin/proxystack-web", "sub", "--dir", subDir, "import", "/root/main.bundle")
		configHash := m.must("sha256sum", subDir+"/config.yaml")
		dataHash := m.must("sha256sum", subDir+"/data/main.bundle")

		out, err = m.install(binaryV2)
		require.NoError(t, err, out)
		m.active()
		require.Contains(t, m.must(subDir+"/bin/proxystack-web", "--version"), "v0.2.0-test")
		require.Contains(t, m.must(subDir+"/bin/proxystack-web.prev", "--version"), "v0.1.0-test")
		require.Equal(t, configHash, m.must("sha256sum", subDir+"/config.yaml"))
		require.Equal(t, dataHash, m.must("sha256sum", subDir+"/data/main.bundle"))

		m.must(subDir+"/bin/proxystack-web", "sub", "--dir", subDir, "deploy", "rollback")
		m.active()
		require.Contains(t, m.must(subDir+"/bin/proxystack-web", "--version"), "v0.1.0-test")
		require.Contains(t, m.must(subDir+"/bin/proxystack-web.prev", "--version"), "v0.2.0-test")

		m.must("bash", deployDir+"/uninstall.sh", "--role", "sub")
		m.must("test", "!", "-e", subDir+"/bin")
		m.must("test", "-f", subDir+"/config.yaml")
		m.must("test", "-f", subDir+"/data/main.bundle")

		out, err = m.install(binaryV2)
		require.NoError(t, err, out)
		m.active()
		require.Equal(t, dataHash, m.must("sha256sum", subDir+"/data/main.bundle"))
		m.must("bash", deployDir+"/uninstall.sh", "--role", "sub", "--purge")
		m.must("test", "!", "-e", subDir)
		_, err = m.run("getent", "passwd", "proxystack-web-sub")
		require.Error(t, err)
	})
}
