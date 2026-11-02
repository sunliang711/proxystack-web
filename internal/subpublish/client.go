// Package subpublish 通过 SSH 正向转发访问订阅端的健康检查和在线导入接口。
package subpublish

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	tcpTimeout       = 5 * time.Second
	handshakeTimeout = 10 * time.Second
	probeTimeout     = 20 * time.Second
	maxResponseBytes = 1 << 20
)

var hostKeyAlgorithms = map[string][]string{
	ssh.KeyAlgoED25519:  {ssh.KeyAlgoED25519},
	ssh.KeyAlgoECDSA256: {ssh.KeyAlgoECDSA256},
	ssh.KeyAlgoRSA:      {ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256},
}

// Identity 是管理端用于登录订阅端的 SSH 客户端身份。
type Identity struct {
	Seed []byte
}

// Target 是一次 SSH 连接所需的目标参数；远端主机固定为回环地址，避免把 SSH 当作任意网络跳板。
type Target struct {
	SSHHost            string
	SSHPort            int
	SSHUser            string
	HostKeyAlgorithm   string
	HostKeyFingerprint string
	RemotePort         int
	Source             string
	Secret             string
}

// Stage 是连接测试的一个阶段。
type Stage struct {
	Name       string `json:"name"`
	Status     string `json:"status" enum:"succeeded,failed,skipped"`
	DurationMS int64  `json:"duration_ms"`
	Message    string `json:"message"`
}

// SourceHealth 是订阅端健康检查中的来源状态。
type SourceHealth struct {
	Name     string `json:"name"`
	Sequence int64  `json:"sequence"`
	Error    string `json:"error,omitempty"`
}

// ProbeResult 是 SSH 与订阅端连接测试结果。
type ProbeResult struct {
	Stages  []Stage        `json:"stages"`
	Status  string         `json:"status"`
	Users   int            `json:"users"`
	Nodes   int            `json:"nodes"`
	Sources []SourceHealth `json:"sources"`
}

// DeliveryResult 是订阅端导入接口的脱敏响应。
type DeliveryResult struct {
	Status           string `json:"status"`
	Source           string `json:"source"`
	Sequence         int64  `json:"sequence"`
	PreviousSequence int64  `json:"previous_sequence"`
	Users            int    `json:"users"`
	Nodes            int    `json:"nodes"`
}

// Client 抽象在线发布客户端，测试可以替换为内存实现。
type Client interface {
	Probe(ctx context.Context, identity Identity, target Target) (*ProbeResult, error)
	Deliver(ctx context.Context, identity Identity, target Target, bundle []byte) (*DeliveryResult, error)
}

// SSHClient 使用 golang.org/x/crypto/ssh 建立按需正向转发。
type SSHClient struct{}

type sshConnection struct {
	client *ssh.Client
	tcp    net.Conn
}

// SupportedHostKeyType 报告管理端是否支持该 SSH Host Key 公钥类型。
func SupportedHostKeyType(keyType string) bool {
	_, ok := hostKeyAlgorithms[keyType]
	return ok
}

func (c *sshConnection) Close() {
	if c.client != nil {
		_ = c.client.Close()
	}
	if c.tcp != nil {
		_ = c.tcp.Close()
	}
}

func signer(identity Identity) (ssh.Signer, error) {
	if len(identity.Seed) != ed25519.SeedSize {
		return nil, errors.New("SSH identity is not configured")
	}
	return ssh.NewSignerFromKey(ed25519.NewKeyFromSeed(identity.Seed))
}

func sshAddress(target Target) string {
	return net.JoinHostPort(target.SSHHost, strconv.Itoa(target.SSHPort))
}

func remoteAddress(target Target) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(target.RemotePort))
}

func connect(ctx context.Context, identity Identity, target Target, stages *[]Stage) (*sshConnection, error) {
	sshSigner, err := signer(identity)
	if err != nil {
		return nil, err
	}
	algorithms, ok := hostKeyAlgorithms[target.HostKeyAlgorithm]
	if !ok {
		return nil, errors.New("unsupported SSH host key algorithm")
	}

	started := time.Now()
	tcp, err := (&net.Dialer{Timeout: tcpTimeout}).DialContext(ctx, "tcp", sshAddress(target))
	if err != nil {
		*stages = append(*stages, failedStage("tcp", started, "SSH TCP connection failed"))
		return nil, errors.New("SSH TCP connection failed")
	}
	*stages = append(*stages, succeededStage("tcp", started, "SSH TCP connection established"))

	verifiedHostKey := false
	config := &ssh.ClientConfig{
		User:              target.SSHUser,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(sshSigner)},
		Timeout:           handshakeTimeout,
		HostKeyAlgorithms: append([]string(nil), algorithms...),
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if key.Type() != target.HostKeyAlgorithm || ssh.FingerprintSHA256(key) != target.HostKeyFingerprint {
				return errors.New("SSH host key does not match the configured fingerprint")
			}
			verifiedHostKey = true
			return nil
		},
	}
	_ = tcp.SetDeadline(time.Now().Add(handshakeTimeout))
	handshakeStarted := time.Now()
	conn, chans, reqs, err := ssh.NewClientConn(tcp, sshAddress(target), config)
	_ = tcp.SetDeadline(time.Time{})
	if err != nil {
		_ = tcp.Close()
		if !verifiedHostKey {
			*stages = append(*stages, failedStage("host_key", handshakeStarted, "SSH host key verification failed"))
			*stages = append(*stages, skippedStage("ssh_auth", "SSH authentication was not attempted"))
			return nil, errors.New("SSH host key verification failed")
		}
		*stages = append(*stages, succeededStage("host_key", handshakeStarted, "SSH host key verified"))
		*stages = append(*stages, failedStage("ssh_auth", handshakeStarted, "SSH public key authentication failed"))
		return nil, errors.New("SSH public key authentication failed")
	}
	*stages = append(*stages, succeededStage("host_key", handshakeStarted, "SSH host key verified"))
	*stages = append(*stages, succeededStage("ssh_auth", handshakeStarted, "SSH public key authentication succeeded"))
	return &sshConnection{client: ssh.NewClient(conn, chans, reqs), tcp: tcp}, nil
}

func succeededStage(name string, started time.Time, message string) Stage {
	return Stage{Name: name, Status: "succeeded", DurationMS: time.Since(started).Milliseconds(), Message: message}
}

func failedStage(name string, started time.Time, message string) Stage {
	return Stage{Name: name, Status: "failed", DurationMS: time.Since(started).Milliseconds(), Message: message}
}

func skippedStage(name, message string) Stage {
	return Stage{Name: name, Status: "skipped", Message: message}
}

func targetHTTPClient(client *ssh.Client, target Target, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return client.DialContext(ctx, "tcp", remoteAddress(target))
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("HTTP redirects are not allowed")
		},
	}
}

func endpoint(target Target, path string) string {
	return "http://127.0.0.1:" + strconv.Itoa(target.RemotePort) + path
}

func readJSONResponse(response *http.Response, out any) error {
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return errors.New("read subscription server response failed")
	}
	if len(data) > maxResponseBytes {
		return errors.New("subscription server response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("subscription server returned HTTP %d", response.StatusCode)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("subscription server returned an invalid response")
	}
	return nil
}

func validatePublishProbeResponse(response *http.Response, target Target) error {
	if response.StatusCode != http.StatusNoContent {
		return errors.New("publish Secret was rejected")
	}
	if response.Header.Get("X-Proxystack-Source") != target.Source {
		return errors.New("publish Secret is bound to a different source")
	}
	return nil
}

// Probe 依次测试 SSH、正向转发、健康检查和发布 Secret。
func (SSHClient) Probe(ctx context.Context, identity Identity, target Target) (*ProbeResult, error) {
	result := &ProbeResult{Stages: []Stage{}, Sources: []SourceHealth{}}
	connection, err := connect(ctx, identity, target, &result.Stages)
	if err != nil {
		return result, err
	}
	defer connection.Close()

	started := time.Now()
	forward, err := connection.client.DialContext(ctx, "tcp", remoteAddress(target))
	if err != nil {
		result.Stages = append(result.Stages, failedStage("forward", started, "SSH port forwarding failed"))
		result.Stages = append(result.Stages, skippedStage("health", "Subscription health check was not attempted"))
		result.Stages = append(result.Stages, skippedStage("publish_auth", "Publish authentication was not attempted"))
		return result, errors.New("SSH port forwarding failed")
	}
	_ = forward.Close()
	result.Stages = append(result.Stages, succeededStage("forward", started, "SSH port forwarding succeeded"))

	client := targetHTTPClient(connection.client, target, probeTimeout)
	healthStarted := time.Now()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(target, "/health"), nil)
	response, err := client.Do(request)
	if err != nil {
		result.Stages = append(result.Stages, failedStage("health", healthStarted, "Subscription health check failed"))
		result.Stages = append(result.Stages, skippedStage("publish_auth", "Publish authentication was not attempted"))
		return result, errors.New("subscription health check failed")
	}
	var health struct {
		Status string         `json:"status"`
		Users  int            `json:"users"`
		Nodes  int            `json:"nodes"`
		Source []SourceHealth `json:"sources"`
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusServiceUnavailable {
		_ = response.Body.Close()
		result.Stages = append(result.Stages, failedStage("health", healthStarted, "Subscription health check returned an unexpected status"))
		result.Stages = append(result.Stages, skippedStage("publish_auth", "Publish authentication was not attempted"))
		return result, fmt.Errorf("subscription health check returned HTTP %d", response.StatusCode)
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	_ = response.Body.Close()
	if readErr != nil || len(data) > maxResponseBytes || json.Unmarshal(data, &health) != nil {
		result.Stages = append(result.Stages, failedStage("health", healthStarted, "Subscription health check returned an invalid response"))
		result.Stages = append(result.Stages, skippedStage("publish_auth", "Publish authentication was not attempted"))
		return result, errors.New("subscription health check returned an invalid response")
	}
	result.Status, result.Users, result.Nodes, result.Sources = health.Status, health.Users, health.Nodes, health.Source
	result.Stages = append(result.Stages, succeededStage("health", healthStarted, "Subscription server is online"))

	authStarted := time.Now()
	request, _ = http.NewRequestWithContext(ctx, http.MethodHead, endpoint(target, "/api/v1/subscription-bundles/import"), nil)
	request.Header.Set("Authorization", "Bearer "+target.Secret)
	response, err = client.Do(request)
	if err != nil {
		result.Stages = append(result.Stages, failedStage("publish_auth", authStarted, "Publish authentication check failed"))
		return result, errors.New("publish authentication check failed")
	}
	defer response.Body.Close()
	if err := validatePublishProbeResponse(response, target); err != nil {
		result.Stages = append(result.Stages, failedStage("publish_auth", authStarted, err.Error()))
		return result, err
	}
	result.Stages = append(result.Stages, succeededStage("publish_auth", authStarted, "Publish Secret is valid"))
	return result, nil
}

// Deliver 通过 SSH 正向转发发送原始签名订阅包。
func (SSHClient) Deliver(ctx context.Context, identity Identity, target Target, bundle []byte) (*DeliveryResult, error) {
	stages := []Stage{}
	connection, err := connect(ctx, identity, target, &stages)
	if err != nil {
		return nil, err
	}
	defer connection.Close()

	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(target, "/api/v1/subscription-bundles/import"), bytes.NewReader(bundle))
	request.Header.Set("Authorization", "Bearer "+target.Secret)
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := targetHTTPClient(connection.client, target, 0).Do(request)
	if err != nil {
		return nil, errors.New("subscription bundle delivery failed")
	}
	var result DeliveryResult
	if err := readJSONResponse(response, &result); err != nil {
		return nil, err
	}
	if result.Source != target.Source {
		return nil, errors.New("subscription server returned an unexpected source")
	}
	if !strings.EqualFold(result.Status, "imported") && !strings.EqualFold(result.Status, "already_imported") {
		return nil, errors.New("subscription server returned an unexpected delivery status")
	}
	return &result, nil
}
