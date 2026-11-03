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

// DeployRequest contains the verified release artifact and first-install settings.
type DeployRequest struct {
	Binary    []byte
	SHA256    string
	Source    string
	PublicKey string
	Port      int
	Secret    string
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
	RollbackDeploy(ctx context.Context, identity Identity, target Target) (*DeployResult, error)
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
	stages := []Stage{}
	connection, err := connect(ctx, identity, target, &stages)
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
	upload := "umask 077 && cat > " + temporary + " && chmod 0700 " + temporary
	if _, err := runSSHCommand(ctx, connection.client, upload, bytes.NewReader(request.Binary)); err != nil {
		return nil, errors.New("upload subscription deployment binary failed")
	}
	command := fmt.Sprintf("%s sub --dir %s deploy apply --json --sha256 %s --source %s --public-key %s --listen 127.0.0.1 --port %d",
		temporary, remoteSubDir, request.SHA256, shellQuote(request.Source), shellQuote(request.PublicKey), request.Port)
	output, err := runSSHCommand(ctx, connection.client, privilegedCommand(target, command), nil)
	if err != nil {
		return nil, fmt.Errorf("remote subscription deployment failed: %w", err)
	}
	var result DeployResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, errors.New("remote subscription deployment returned an invalid response")
	}
	installed := remoteSubDir + "/bin/proxystack-web"
	secretCommand := installed + " sub --dir " + remoteSubDir + " source set-publish-token " + shellQuote(request.Source)
	if _, err := runSSHCommand(ctx, connection.client, privilegedCommand(target, secretCommand), strings.NewReader(request.Secret+"\n")); err != nil {
		return nil, errors.New("configure subscription publish token failed")
	}
	return &result, nil
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
