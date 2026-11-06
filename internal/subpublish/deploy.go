package subpublish

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const remoteSubDir = "/opt/proxystack-web-sub"

// DeployStatus is the SSH deployment preflight result.
type DeployStatus struct {
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	Sudo            bool   `json:"sudo"`
	State           string `json:"state" enum:"uninstalled,retained,installed"`
	Version         string `json:"version,omitempty"`
	PreviousVersion string `json:"previous_version,omitempty"`
	ServiceActive   bool   `json:"service_active"`
	ServiceEnabled  bool   `json:"service_enabled"`
}

// Logf receives progress lines of long remote operations. A nil Logf discards them.
type Logf func(format string, args ...any)

func (l Logf) log(format string, args ...any) {
	if l != nil {
		l(format, args...)
	}
}

// DeployRequest contains the verified release artifact and first-install settings.
type DeployRequest struct {
	Binary    []byte
	SHA256    string
	Source    string
	PublicKey string
	Port      int
	Secret    string
	Logf      Logf
}

// DeployResult is returned by the remote sub deploy command.
type DeployResult struct {
	Action          string `json:"action"`
	Version         string `json:"version,omitempty"`
	PreviousVersion string `json:"previous_version,omitempty"`
	RolledBack      bool   `json:"rolled_back,omitempty"`
}

// Deployer uses the same SSH identity and target as online publishing.
type Deployer interface {
	InspectDeploy(ctx context.Context, identity Identity, target Target) (*DeployStatus, error)
	Deploy(ctx context.Context, identity Identity, target Target, request DeployRequest) (*DeployResult, error)
	ConfigurePublishToken(ctx context.Context, identity Identity, target Target, source, secret string) error
	RollbackDeploy(ctx context.Context, identity Identity, target Target) (*DeployResult, error)
	UninstallDeploy(ctx context.Context, identity Identity, target Target, binary []byte, logf Logf) (*DeployResult, error)
}

// InspectDeploy verifies platform and non-interactive privilege escalation. An uninstalled node is a valid result.
func (SSHClient) InspectDeploy(ctx context.Context, identity Identity, target Target) (*DeployStatus, error) {
	stages := []Stage{}
	connection, err := connect(ctx, identity, target, &stages)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	osName, err := runSSHCommand(ctx, connection.client, "uname -s", nil)
	if err != nil {
		return nil, errors.New("remote operating system detection failed")
	}
	if strings.TrimSpace(string(osName)) != "Linux" {
		return nil, errors.New("subscription deployment requires Linux")
	}
	archOutput, err := runSSHCommand(ctx, connection.client, "uname -m", nil)
	if err != nil {
		return nil, errors.New("remote architecture detection failed")
	}
	arch, err := normalizeRemoteArch(strings.TrimSpace(string(archOutput)))
	if err != nil {
		return nil, err
	}
	if _, err := runSSHCommand(ctx, connection.client, privilegedCommand(target, "/bin/true"), nil); err != nil {
		return nil, errors.New("SSH user does not support non-interactive sudo")
	}
	status := &DeployStatus{OS: "linux", Arch: arch, Sudo: true, State: "uninstalled"}
	binary := remoteSubDir + "/bin/proxystack-web"
	installed, err := runSSHTest(ctx, connection.client, privilegedCommand(target, "/usr/bin/test -x "+binary))
	if err != nil {
		return nil, fmt.Errorf("inspect installed subscription server: %w", err)
	}
	if !installed {
		marker := privilegedCommand(target, "/usr/bin/test -f "+remoteSubDir+"/.proxystack-web-sub")
		config := privilegedCommand(target, "/usr/bin/test -f "+remoteSubDir+"/config.yaml")
		markerExists, markerErr := runSSHTest(ctx, connection.client, marker)
		if markerErr != nil {
			return nil, fmt.Errorf("inspect retained subscription marker: %w", markerErr)
		}
		configExists, configErr := runSSHTest(ctx, connection.client, config)
		if configErr != nil {
			return nil, fmt.Errorf("inspect retained subscription configuration: %w", configErr)
		}
		if markerExists && configExists {
			status.State = "retained"
		}
		return status, nil
	}
	output, err := runSSHCommand(ctx, connection.client, privilegedCommand(target, binary+" sub --dir "+remoteSubDir+" deploy inspect --json"), nil)
	if err != nil {
		return nil, errors.New("inspect installed subscription server failed")
	}
	var remote struct {
		State           string `json:"state"`
		Version         string `json:"version"`
		PreviousVersion string `json:"previous_version"`
		ServiceActive   bool   `json:"service_active"`
		ServiceEnabled  bool   `json:"service_enabled"`
	}
	if err := json.Unmarshal(output, &remote); err != nil {
		return nil, errors.New("subscription deployment inspection returned an invalid response")
	}
	status.State, status.Version, status.PreviousVersion = remote.State, remote.Version, remote.PreviousVersion
	status.ServiceActive, status.ServiceEnabled = remote.ServiceActive, remote.ServiceEnabled
	return status, nil
}

// Deploy uploads a verified binary and invokes the same sub deploy apply command used for offline deployment.
func (SSHClient) Deploy(ctx context.Context, identity Identity, target Target, request DeployRequest) (*DeployResult, error) {
	logf := request.Logf
	connection, err := connectLogged(ctx, identity, target, logf)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	temporary, err := remoteTemporaryPath()
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = runSSHCommand(cleanupCtx, connection.client, "rm -f "+temporary, nil)
	}()
	if err := uploadBinary(ctx, connection.client, temporary, request.Binary, logf); err != nil {
		return nil, errors.New("upload subscription deployment binary failed")
	}
	command := fmt.Sprintf("%s sub --dir %s deploy apply --json --sha256 %s --source %s --public-key %s --listen 127.0.0.1 --port %d",
		temporary, remoteSubDir, request.SHA256, shellQuote(request.Source), shellQuote(request.PublicKey), request.Port)
	logf.log("Running the remote installer: verify, install, start the service and wait for its health check")
	started := time.Now()
	output, err := runSSHCommand(ctx, connection.client, privilegedCommand(target, command), nil)
	if err != nil {
		return nil, fmt.Errorf("remote subscription deployment failed after %s: %w", roundDuration(time.Since(started)), err)
	}
	var result DeployResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, errors.New("remote subscription deployment returned an invalid response")
	}
	logf.log("Remote installer finished in %s: %s %s", roundDuration(time.Since(started)), result.Action, result.Version)
	started = time.Now()
	if err := setPublishToken(ctx, connection.client, target, request.Source, request.Secret); err != nil {
		return nil, err
	}
	logf.log("Publish token configured in %s", roundDuration(time.Since(started)))
	return &result, nil
}

// connectLogged opens the SSH connection and reports how long the TCP connection and handshake took.
func connectLogged(ctx context.Context, identity Identity, target Target, logf Logf) (*sshConnection, error) {
	logf.log("Connecting to %s@%s over SSH", target.SSHUser, sshAddress(target))
	started := time.Now()
	stages := []Stage{}
	connection, err := connect(ctx, identity, target, &stages)
	if err != nil {
		return nil, err
	}
	logf.log("SSH connected in %s", roundDuration(time.Since(started)))
	return connection, nil
}

// uploadBinary streams binary to path on the remote host and reports progress about every five seconds.
func uploadBinary(ctx context.Context, client *ssh.Client, path string, binary []byte, logf Logf) error {
	total := int64(len(binary))
	logf.log("Uploading %s to the subscription host", formatSize(total))
	started := time.Now()
	reader := &progressReader{reader: bytes.NewReader(binary), total: total, started: started, last: started, logf: logf}
	upload := "umask 077 && cat > " + path + " && chmod 0700 " + path
	if _, err := runSSHCommand(ctx, client, upload, reader); err != nil {
		return err
	}
	elapsed := time.Since(started)
	logf.log("Upload finished: %s in %s (%s/s)", formatSize(total), roundDuration(elapsed), formatSize(rate(total, elapsed)))
	return nil
}

// progressInterval is how often the upload reports its progress.
const progressInterval = 5 * time.Second

type progressReader struct {
	reader        io.Reader
	total, read   int64
	started, last time.Time
	logf          Logf
}

func (p *progressReader) Read(buffer []byte) (int, error) {
	n, err := p.reader.Read(buffer)
	p.read += int64(n)
	if now := time.Now(); p.read < p.total && now.Sub(p.last) >= progressInterval {
		p.last = now
		elapsed := now.Sub(p.started)
		speed := rate(p.read, elapsed)
		remaining := "unknown"
		if speed > 0 {
			remaining = roundDuration(time.Duration(float64(p.total-p.read) / float64(speed) * float64(time.Second))).String()
		}
		p.logf.log("Uploaded %s / %s (%d%%) at %s/s, about %s remaining",
			formatSize(p.read), formatSize(p.total), p.read*100/p.total, formatSize(speed), remaining)
	}
	return n, err
}

// rate returns bytes per second.
func rate(bytes int64, elapsed time.Duration) int64 {
	if elapsed <= 0 {
		return 0
	}
	return int64(float64(bytes) / elapsed.Seconds())
}

func roundDuration(value time.Duration) time.Duration {
	if value < 10*time.Second {
		return value.Round(100 * time.Millisecond)
	}
	return value.Round(time.Second)
}

// formatSize prints a byte count in KB or MB with one decimal.
func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// ConfigurePublishToken writes the publish Secret to an installed subscription server without replacing its binary.
func (SSHClient) ConfigurePublishToken(ctx context.Context, identity Identity, target Target, source, secret string) error {
	stages := []Stage{}
	connection, err := connect(ctx, identity, target, &stages)
	if err != nil {
		return err
	}
	defer connection.Close()
	return setPublishToken(ctx, connection.client, target, source, secret)
}

func setPublishToken(ctx context.Context, client *ssh.Client, target Target, source, secret string) error {
	installed := remoteSubDir + "/bin/proxystack-web"
	command := installed + " sub --dir " + remoteSubDir + " source set-publish-token " + shellQuote(source)
	if _, err := runSSHCommand(ctx, client, privilegedCommand(target, command), strings.NewReader(secret+"\n")); err != nil {
		return errors.New("configure subscription publish token failed")
	}
	return nil
}

// RollbackDeploy invokes the local rollback transaction through SSH.
func (SSHClient) RollbackDeploy(ctx context.Context, identity Identity, target Target) (*DeployResult, error) {
	stages := []Stage{}
	connection, err := connect(ctx, identity, target, &stages)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	binary := remoteSubDir + "/bin/proxystack-web"
	command := binary + " sub --dir " + remoteSubDir + " deploy rollback --json"
	output, err := runSSHCommand(ctx, connection.client, privilegedCommand(target, command), nil)
	if err != nil {
		return nil, errors.New("remote subscription rollback failed")
	}
	var result DeployResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, errors.New("remote subscription rollback returned an invalid response")
	}
	return &result, nil
}

// UninstallDeploy removes the subscription service together with its configuration, imported bundles and service account.
// With a nil binary the installed program runs the removal; otherwise binary is uploaded to a temporary path first, which
// covers installations whose program was already removed.
func (SSHClient) UninstallDeploy(ctx context.Context, identity Identity, target Target, binary []byte, logf Logf) (*DeployResult, error) {
	connection, err := connectLogged(ctx, identity, target, logf)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	program := remoteSubDir + "/bin/proxystack-web"
	if binary != nil {
		temporary, err := remoteTemporaryPath()
		if err != nil {
			return nil, err
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = runSSHCommand(cleanupCtx, connection.client, "rm -f "+temporary, nil)
		}()
		if err := uploadBinary(ctx, connection.client, temporary, binary, logf); err != nil {
			return nil, errors.New("upload subscription uninstall binary failed")
		}
		program = temporary
	}
	command := program + " sub --dir " + remoteSubDir + " deploy uninstall --purge --json"
	logf.log("Running the remote uninstaller")
	started := time.Now()
	output, err := runSSHCommand(ctx, connection.client, privilegedCommand(target, command), nil)
	if err != nil {
		return nil, fmt.Errorf("remote subscription uninstall failed: %w", err)
	}
	var result DeployResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, errors.New("remote subscription uninstall returned an invalid response")
	}
	logf.log("Remote uninstaller finished in %s", roundDuration(time.Since(started)))
	return &result, nil
}

func runSSHCommand(ctx context.Context, client interface {
	NewSession() (*ssh.Session, error)
}, command string, stdin io.Reader) ([]byte, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	if stdin != nil {
		session.Stdin = stdin
	}
	type commandResult struct {
		output []byte
		err    error
	}
	done := make(chan commandResult, 1)
	go func() {
		output, runErr := session.CombinedOutput(command)
		done <- commandResult{output: output, err: runErr}
	}()
	select {
	case result := <-done:
		if result.err != nil {
			return nil, &sshCommandError{output: result.output, err: result.err}
		}
		return result.output, nil
	case <-ctx.Done():
		_ = session.Close()
		return nil, ctx.Err()
	}
}

type sshCommandError struct {
	output []byte
	err    error
}

func (e *sshCommandError) Error() string {
	if output := strings.TrimSpace(string(e.output)); output != "" {
		return output
	}
	return e.err.Error()
}

func (e *sshCommandError) Unwrap() error { return e.err }

func runSSHTest(ctx context.Context, client interface {
	NewSession() (*ssh.Session, error)
}, command string) (bool, error) {
	_, err := runSSHCommand(ctx, client, command, nil)
	if err == nil {
		return true, nil
	}
	if isSSHTestMiss(err) {
		return false, nil
	}
	return false, err
}

func isSSHTestMiss(err error) bool {
	var commandErr *sshCommandError
	if !errors.As(err, &commandErr) || strings.TrimSpace(string(commandErr.output)) != "" {
		return false
	}
	var exitErr interface{ ExitStatus() int }
	return errors.As(commandErr, &exitErr) && exitErr.ExitStatus() == 1
}

func normalizeRemoteArch(value string) (string, error) {
	switch value {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported remote architecture: %s", value)
	}
}

func privilegedCommand(target Target, command string) string {
	if target.SSHUser == "root" {
		return command
	}
	return "sudo -n " + command
}

func remoteTemporaryPath() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return "/tmp/proxystack-web-deploy-" + hex.EncodeToString(data), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
