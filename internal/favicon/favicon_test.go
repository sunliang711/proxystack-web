package favicon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"

	"proxystack-web/internal/deps"
)

var (
	pngIcon = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x10\x00\x00\x00\x10\x08\x06\x00\x00\x00")
	icoIcon = []byte{0, 0, 1, 0, 1, 0, 16, 16}
)

// testFetcher 让全部公网域名解析到一个公网地址，实际连接都打到本地的 TLS 测试服务器，并记录连接的目标。
func testFetcher(t *testing.T, handler http.Handler, resolved map[string]string) (*Fetcher, *[]string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	f := New(deps.ProxyConfig{Kind: deps.ProxyDirect})
	t.Cleanup(f.Close)
	f.client.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig
	f.Resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		if value, ok := resolved[host]; ok {
			return []netip.Addr{netip.MustParseAddr(value)}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
	}
	var mu sync.Mutex
	targets := []string{}
	f.dial = func(ctx context.Context, address string) (net.Conn, error) {
		mu.Lock()
		targets = append(targets, address)
		mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	return f, &targets
}

func TestFetchFaviconICO(t *testing.T) {
	f, targets := testFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			_, _ = w.Write(icoIcon)
			return
		}
		http.NotFound(w, r)
	}), nil)
	icon, err := f.Fetch(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if icon.ContentType != "image/x-icon" || string(icon.Data) != string(icoIcon) {
		t.Fatalf("icon = %+v", icon)
	}
	if (*targets)[0] != "93.184.215.14:443" {
		t.Fatalf("direct mode must dial the checked IP, got %v", *targets)
	}
}

func TestFetchFaviconFromPageAfterRedirect(t *testing.T) {
	f, _ := testFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Host == "example.com" && r.URL.Path == "/":
			http.Redirect(w, r, "https://www.example.com/home", http.StatusFound)
		case r.Host == "www.example.com" && r.URL.Path == "/home":
			_, _ = w.Write([]byte(`<html><head>
				<link rel="icon" type="image/svg+xml" href="/icon.svg">
				<link rel="apple-touch-icon" href="/touch.png">
				<link rel="shortcut icon" href="static/icon.png?v=2">
				</head><body><link rel="icon" href="/late.png"></body></html>`))
		case r.Host == "www.example.com" && r.URL.Path == "/static/icon.png":
			_, _ = w.Write(pngIcon)
		default:
			http.NotFound(w, r)
		}
	}), nil)
	icon, err := f.Fetch(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if icon.ContentType != "image/png" {
		t.Fatalf("content type = %s", icon.ContentType)
	}
}

func TestFetchRejectsNonImages(t *testing.T) {
	f, _ := testFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/favicon.ico":
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
		case "/":
			_, _ = w.Write([]byte(`<link rel="icon" href="/big.png">`))
		case "/big.png":
			_, _ = w.Write(append(append([]byte{}, pngIcon...), make([]byte, MaxIcon)...))
		default:
			http.NotFound(w, r)
		}
	}), nil)
	if _, err := f.Fetch(context.Background(), "example.com"); err == nil {
		t.Fatal("SVG and oversized icons must be rejected")
	}
}

func TestFetchBlocksPrivateTargets(t *testing.T) {
	f, targets := testFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "example.com" {
			http.Redirect(w, r, "https://evil.example.net/favicon.ico", http.StatusFound)
			return
		}
		_, _ = w.Write(icoIcon)
	}), map[string]string{"internal.example.org": "192.168.1.10", "evil.example.net": "127.0.0.1", "cgnat.example.org": "100.64.1.1"})

	for _, domain := range []string{"printer.lan", "localhost", "10.0.0.1", "co.uk", "internal.example.org", "cgnat.example.org"} {
		if _, err := f.Fetch(context.Background(), domain); !errors.Is(err, ErrForbidden) {
			t.Errorf("Fetch(%q) error = %v, want ErrForbidden", domain, err)
		}
	}
	// 跳转到解析为本机地址的域名：每一跳都重新检查。
	if _, err := f.Fetch(context.Background(), "example.com"); err == nil {
		t.Fatal("redirect to a loopback address must fail")
	}
	for _, target := range *targets {
		if strings.HasPrefix(target, "127.") || strings.HasPrefix(target, "192.168.") {
			t.Fatalf("dialed a private address: %v", *targets)
		}
	}
	if err := checkURL(mustParse(t, "https://example.com:6379/favicon.ico")); !errors.Is(err, ErrForbidden) {
		t.Fatal("non-web ports must be rejected")
	}
}

// 下载代理设置了由代理解析域名时也连接本机检查过的 IP，代理再解析一次可能得到内网地址。
func TestRemoteDNSStillDialsCheckedIP(t *testing.T) {
	f, targets := testFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(icoIcon) }), nil)
	f.cfg = deps.ProxyConfig{Kind: deps.ProxySOCKS5, Address: "127.0.0.1:1080", RemoteDNS: true}
	if _, err := f.Fetch(context.Background(), "example.com"); err != nil {
		t.Fatal(err)
	}
	if (*targets)[0] != "93.184.215.14:443" {
		t.Fatalf("must dial the checked IP, got %v", *targets)
	}
}

func TestFetchRedirectLimitAndErrorKinds(t *testing.T) {
	f, _ := testFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// a.example.com 跳转 3 次后给出图标，b.example.com 跳转 4 次。
		hops := map[string]int{"a.example.com": 3, "b.example.com": 4}[strings.Split(r.Host, ":")[0]]
		step := len(r.URL.Query().Get("n"))
		if step < hops {
			http.Redirect(w, r, r.URL.Path+"?n="+strings.Repeat("x", step+1), http.StatusFound)
			return
		}
		if r.URL.Path == "/favicon.ico" && hops > 0 {
			_, _ = w.Write(icoIcon)
			return
		}
		http.NotFound(w, r)
	}), nil)
	if _, err := f.Fetch(context.Background(), "a.example.com"); err != nil {
		t.Fatalf("3 redirects must be followed: %v", err)
	}
	if _, err := f.Fetch(context.Background(), "b.example.com"); err == nil || errors.Is(err, ErrNoIcon) {
		t.Fatalf("4 redirects must fail as a non-cacheable error, got %v", err)
	}
	if _, err := f.Fetch(context.Background(), "c.example.com"); !errors.Is(err, ErrNoIcon) {
		t.Fatalf("a site that answers 404 everywhere has no icon, got %v", err)
	}
	f.dial = func(context.Context, string) (net.Conn, error) { return nil, errors.New("connection refused") }
	if _, err := f.Fetch(context.Background(), "d.example.com"); err == nil || errors.Is(err, ErrNoIcon) {
		t.Fatalf("unreachable sites are transient failures, got %v", err)
	}
}

func TestProxyUnavailable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	for _, kind := range []string{deps.ProxySOCKS5, deps.ProxyHTTP} {
		f := New(deps.ProxyConfig{Kind: kind, Address: address})
		f.Resolve = func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
		}
		if _, err := f.Fetch(context.Background(), "example.com"); !errors.Is(err, ErrProxy) {
			t.Errorf("%s: error = %v, want ErrProxy", kind, err)
		}
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"93.184.215.14": true, "2606:4700::1111": true, "198.18.1.208": false, "2002:c0a8:101::1": false, "::7f00:1": false,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "::1": false, "fd00::1": false, "fe80::1": false, "::ffff:127.0.0.1": false,
		"240.0.0.1": false, "224.0.0.1": false,
	} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("publicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestPublicDomain(t *testing.T) {
	for host, want := range map[string]bool{
		"google.com": true, "www.google.com": true, "bbc.co.uk": true, "alice.github.io": true,
		"co.uk": false, "com": false, "github.io": false, "printer.lan": false, "nas.local": false,
		"localhost": false, "1.2.3.4": false, "::1": false, "": false, "a b.com": false,
	} {
		if got := PublicDomain(host); got != want {
			t.Errorf("PublicDomain(%q) = %v, want %v", host, got, want)
		}
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
