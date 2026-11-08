package deps

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// 下载代理类型。
const (
	ProxyDirect = "direct"
	ProxyHTTP   = "http"
	ProxySOCKS5 = "socks5"
)

// ProxyConfig 是下载使用的代理。Address 为 host:port。
type ProxyConfig struct {
	Kind      string
	Address   string
	Username  string
	Password  string
	RemoteDNS bool
}

// Redacted 返回去掉用户名和密码的代理描述，用于日志和审计。
func (p ProxyConfig) Redacted() string {
	if p.Kind == "" || p.Kind == ProxyDirect {
		return "直连"
	}
	return p.Kind + "://" + p.Address
}

// Fetcher 获取发布信息和下载文件。
type Fetcher interface {
	Get(ctx context.Context, url string) ([]byte, error)
	Download(ctx context.Context, url string, progress func(done, total int64)) ([]byte, error)
}

// MaxDownload 是单个下载的大小上限。
const MaxDownload = 256 << 20

// HTTPFetcher 经可选代理访问 GitHub 和自定义下载地址。
type HTTPFetcher struct {
	client *http.Client
	proxy  ProxyConfig
}

// NewHTTPFetcher 按代理设置创建下载器：HTTP 代理用 CONNECT 隧道并发送 Basic 认证，
// SOCKS5 代理用用户名/密码认证；RemoteDNS 为 true 时目标域名交给代理解析。
func NewHTTPFetcher(cfg ProxyConfig) (*HTTPFetcher, error) {
	transport, err := NewTransport(cfg, &net.Dialer{Timeout: 15 * time.Second})
	if err != nil {
		return nil, err
	}
	return &HTTPFetcher{client: &http.Client{Transport: transport}, proxy: cfg}, nil
}

// ForwardDialer 建立到代理服务器（或直连时到目标）的 TCP 连接。
type ForwardDialer interface {
	proxy.Dialer
	proxy.ContextDialer
}

// NewTransport 按代理设置创建 HTTP transport，到代理服务器的连接由 forward 建立。
func NewTransport(cfg ProxyConfig, forward ForwardDialer) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	switch cfg.Kind {
	case "", ProxyDirect:
	case ProxyHTTP:
		transport.DialContext = forward.DialContext
		proxyURL := &url.URL{Scheme: "http", Host: cfg.Address}
		if cfg.Username != "" {
			proxyURL.User = url.UserPassword(cfg.Username, cfg.Password)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	case ProxySOCKS5:
		var auth *proxy.Auth
		if cfg.Username != "" {
			auth = &proxy.Auth{User: cfg.Username, Password: cfg.Password}
		}
		dialer, err := proxy.SOCKS5("tcp", cfg.Address, auth, forward)
		if err != nil {
			return nil, err
		}
		contextDialer := dialer.(proxy.ContextDialer)
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if !cfg.RemoteDNS {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				address = net.JoinHostPort(ips[0].IP.String(), port)
			}
			return contextDialer.DialContext(ctx, network, address)
		}
	default:
		return nil, fmt.Errorf("unknown proxy kind: %s", cfg.Kind)
	}
	return transport, nil
}

// CloseIdleConnections 关闭本轮探测结束后不再使用的空闲连接。
func (f *HTTPFetcher) CloseIdleConnections() { f.client.CloseIdleConnections() }

// Get 读取一个较小的响应（发布信息、摘要文件）。
func (f *HTTPFetcher) Get(ctx context.Context, target string) ([]byte, error) {
	return f.Download(ctx, target, nil)
}

// Download 下载文件，progress 接收已下载字节数和总字节数（未知时为 -1）。
func (f *HTTPFetcher) Download(ctx context.Context, target string, progress func(done, total int64)) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "proxystack-web")
	if strings.HasPrefix(target, GitHubAPI+"/") {
		request.Header.Set("Accept", "application/vnd.github+json")
	}
	response, err := f.client.Do(request)
	if err != nil {
		return nil, ClassifyError(err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusProxyAuthRequired {
		return nil, &FetchError{Kind: ErrProxyAuth, Err: fmt.Errorf("proxy returned %s", response.Status)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &FetchError{Kind: ErrUpstream, Err: fmt.Errorf("GET %s returned %s", redactURL(target), response.Status)}
	}
	reader := io.LimitReader(response.Body, MaxDownload+1)
	var buffer []byte
	chunk := make([]byte, 64*1024)
	last := time.Time{}
	for {
		n, err := reader.Read(chunk)
		buffer = append(buffer, chunk[:n]...)
		if progress != nil && time.Since(last) > time.Second {
			progress(int64(len(buffer)), response.ContentLength)
			last = time.Now()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ClassifyError(err)
		}
	}
	if len(buffer) > MaxDownload {
		return nil, fmt.Errorf("download exceeds %d MiB", MaxDownload>>20)
	}
	if progress != nil {
		progress(int64(len(buffer)), response.ContentLength)
	}
	return buffer, nil
}

// 下载失败的分类，用于「测试连接」提示。
const (
	ErrProxyConnect = "proxy_connect"
	ErrProxyAuth    = "proxy_auth"
	ErrTimeout      = "timeout"
	ErrTLS          = "tls"
	ErrUpstream     = "upstream"
	ErrNetwork      = "network"
)

// FetchError 是分类后的下载错误。
type FetchError struct {
	Kind string
	Err  error
}

func (e *FetchError) Error() string { return e.Err.Error() }
func (e *FetchError) Unwrap() error { return e.Err }

// ClassifyError 判断错误属于连接代理失败、代理认证失败、超时、TLS 错误还是普通网络错误。
func ClassifyError(err error) error {
	var fetchErr *FetchError
	if errors.As(err, &fetchErr) {
		return err
	}
	message := err.Error()
	var netErr net.Error
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var opErr *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return &FetchError{Kind: ErrTimeout, Err: err}
	case strings.Contains(message, "authentication failed") || strings.Contains(message, "Proxy Authentication Required") || strings.Contains(message, "407"):
		return &FetchError{Kind: ErrProxyAuth, Err: err}
	case errors.As(err, &opErr) && opErr.Op == "proxyconnect", strings.Contains(message, "socks connect"):
		return &FetchError{Kind: ErrProxyConnect, Err: err}
	case errors.As(err, &certErr), errors.As(err, &unknownAuthority), errors.As(err, &hostErr), strings.Contains(message, "tls:"):
		return &FetchError{Kind: ErrTLS, Err: err}
	default:
		return &FetchError{Kind: ErrNetwork, Err: err}
	}
}

// TestConnection 经代理请求 GitHub API，返回耗时。
func (f *HTTPFetcher) TestConnection(ctx context.Context) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	start := time.Now()
	_, err := f.Get(ctx, GitHubAPI+"/rate_limit")
	return time.Since(start), err
}

func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.User = nil
	parsed.RawQuery = ""
	return parsed.String()
}
