package subpublish

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type exitStatusError int

func (e exitStatusError) Error() string   { return "command failed" }
func (e exitStatusError) ExitStatus() int { return int(e) }

func TestNormalizeRemoteArch(t *testing.T) {
	for input, expected := range map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"} {
		actual, err := normalizeRemoteArch(input)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
	_, err := normalizeRemoteArch("riscv64")
	require.ErrorContains(t, err, "unsupported remote architecture")
}

func TestPrivilegedCommand(t *testing.T) {
	require.Equal(t, "/bin/true", privilegedCommand(Target{SSHUser: "root"}, "/bin/true"))
	require.Equal(t, "sudo -n /bin/true", privilegedCommand(Target{SSHUser: "deployer"}, "/bin/true"))
}

func TestRemoteTemporaryPath(t *testing.T) {
	path, err := remoteTemporaryPath()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(path, "/tmp/proxystack-web-deploy-"))
	require.Len(t, strings.TrimPrefix(path, "/tmp/proxystack-web-deploy-"), 32)
}

func TestSSHTestMissRequiresExitOneWithoutOutput(t *testing.T) {
	require.True(t, isSSHTestMiss(&sshCommandError{err: exitStatusError(1)}))
	require.False(t, isSSHTestMiss(&sshCommandError{output: []byte("sudo denied"), err: exitStatusError(1)}))
	require.False(t, isSSHTestMiss(&sshCommandError{err: exitStatusError(255)}))
	require.False(t, isSSHTestMiss(errors.New("connection lost")))
}
