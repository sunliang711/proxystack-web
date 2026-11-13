// Package favicon 经下载代理获取网站图标。
//
// 要取图标的域名来自代理用户的访问日志，不可信：用户可以访问解析到内网地址的域名，让面板替他访问内网服务。
// 所以只访问带公共后缀的域名，只连 80 和 443 端口，并且每次建立连接（包括跟随跳转）之前都在本机解析域名，
// 解析结果只要有一个是本机、内网或保留地址就拒绝，然后连接检查过的 IP。即使下载代理设置了由代理解析域名，
// 也不把域名交给代理：代理再解析一次可能得到不同的地址（DNS 重绑定），检查就失去了意义。
package favicon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/proxy"
	"golang.org/x/net/publicsuffix"

	"proxystack-web/internal/deps"
)

const (
	// MaxIcon 是图标文件的大小上限。
	MaxIcon = 100 << 10
	// maxPage 是为查找图标声明而读取的首页长度：图标声明在 <head> 里，只读开头这一段，更长的页面截断而不是放弃。
	maxPage = 512 << 10
	// dialTimeout 是连接单个地址的时长上限，比单个请求的上限短，第一个地址不通时还来得及试下一个。
	dialTimeout = 2500 * time.Millisecond
	// maxRedirects 是跟随跳转的次数上限。
	maxRedirects = 3
	// maxLinks 是首页声明的图标地址最多尝试的个数。
	maxLinks = 4
	// maxAddrs 是一个域名最多尝试连接的地址个数。
	maxAddrs = 3
)

// attemptTimeout 是单个请求（含跟随跳转）的时长上限，一个地址卡住不会耗光整个获取的时间。测试时缩短。
var attemptTimeout = 5 * time.Second

// ErrProxy 表示连不上下载代理或代理认证失败。这类失败与网站无关，调用方不应把它当作「该网站没有图标」缓存下来。
var ErrProxy = errors.New("download proxy unavailable")

// ErrForbidden 表示目标不是公网域名或端口不允许。
var ErrForbidden = errors.New("target is not a public web address")

// ErrNoIcon 表示网站有应答但没有可用的图标（返回非 200、内容不是支持的图片或超出大小）。
// 其余失败（连不上、超时）可能是暂时的。
var ErrNoIcon = errors.New("site has no usable icon")

// Icon 是取回的图标。
type Icon struct {
	ContentType string
	Data        []byte
}

// Fetcher 获取网站图标。
type Fetcher struct {
	client *http.Client
	cfg    deps.ProxyConfig
	// Resolve 解析域名，测试时替换。
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	// dial 建立到目标的 TCP 连接，测试时替换。
	dial func(ctx context.Context, address string) (net.Conn, error)
}

// New 按下载代理设置创建图标获取器。
func New(cfg deps.ProxyConfig) *Fetcher {
	f := &Fetcher{cfg: cfg, Resolve: resolve}
	f.dial = f.dialTarget
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = f.guardedDial
	transport.MaxIdleConns = 4
	transport.IdleConnTimeout = 30 * time.Second
	transport.ResponseHeaderTimeout = attemptTimeout
	f.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return checkURL(request.URL)
		},
	}
	return f
}

// Close 关闭空闲连接。
func (f *Fetcher) Close() { f.client.CloseIdleConnections() }

// Fetch 获取主域名的图标：先取主域名和 www. 主机的 /favicon.ico，没有时读取首页（主域名打不开时读 www. 主机的）
// 中声明的图标，首页跳转到其他主机时再试那台主机的 /favicon.ico。每个请求单独限时。有连接类失败（连不上、超时）时返回其中最后一个；没有时，目标不允许访问返回 ErrForbidden，
// 网站有应答但没有可用图标返回 ErrNoIcon。
func (f *Fetcher) Fetch(ctx context.Context, domain string) (*Icon, error) {
	if !PublicDomain(domain) {
		return nil, ErrForbidden
	}
	var transient, forbidden error
	// note 记录一次失败。fallback 表示这是对 www. 主机的补充尝试：很多域名没有 www. 主机，它连不上不算暂时失败。
	note := func(err error, fallback bool) {
		switch {
		case errors.Is(err, ErrForbidden):
			forbidden = err
		case err != nil && !errors.Is(err, ErrNoIcon) && !fallback:
			transient = err
		}
	}
	attempt := func(target string, fallback bool) (*Icon, bool, error) {
		icon, err := f.icon(ctx, target)
		switch {
		case err == nil:
			return icon, true, nil
		case errors.Is(err, ErrProxy):
			return nil, true, err
		}
		note(err, fallback)
		return nil, false, nil
	}
	hosts := []string{domain}
	if !strings.HasPrefix(domain, "www.") {
		hosts = append(hosts, "www."+domain)
	}
	for index, host := range hosts {
		if icon, done, err := attempt("https://"+host+"/favicon.ico", index > 0); done {
			return icon, err
		}
	}
	var links []string
	var base *url.URL
	for index, host := range hosts {
		pageLinks, pageBase, err := f.pageIcons(ctx, "https://"+host+"/")
		if errors.Is(err, ErrProxy) {
			return nil, err
		}
		note(err, index > 0)
		// 保留第一个得到的最终页面地址：主域名首页跳转到其他主机时，最后还要试那台主机的 /favicon.ico。
		if base == nil {
			base = pageBase
		}
		if err == nil {
			links, base = pageLinks, pageBase
			break
		}
		// 主域名首页已经跳转到 www. 主机，不必再取一次。
		if pageBase != nil && pageBase.Host == "www."+domain {
			break
		}
	}
	tried := 0
	for _, link := range links {
		if tried == maxLinks {
			break
		}
		target, err := base.Parse(link)
		if err != nil || checkURL(target) != nil {
			continue
		}
		tried++
		if icon, done, err := attempt(target.String(), false); done {
			return icon, err
		}
	}
	if base != nil && base.Host != domain && base.Host != "www."+domain {
		if icon, done, err := attempt((&url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/favicon.ico"}).String(), false); done {
			return icon, err
		}
	}
	switch {
	case transient != nil:
		return nil, transient
	case forbidden != nil:
		return nil, forbidden
	}
	return nil, fmt.Errorf("%w: %s", ErrNoIcon, domain)
}

// get 读取 target 的响应。truncate 为 false 时超过 limit 视为没有可用内容；为 true 时只取前 limit 字节。
func (f *Fetcher) get(ctx context.Context, target string, limit int64, truncate bool) (*http.Response, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, nil, err
	}
	if err := checkURL(request.URL); err != nil {
		return nil, nil, err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; proxystack-web favicon)")
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/*;q=0.8,text/html;q=0.5,*/*;q=0.1")
	response, err := f.client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response, nil, fmt.Errorf("%w: GET %s returned %s", ErrNoIcon, request.URL.Redacted(), response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return response, nil, err
	}
	if int64(len(data)) > limit {
		if truncate {
			return response, data[:limit], nil
		}
		return response, nil, fmt.Errorf("%w: response larger than %d bytes", ErrNoIcon, limit)
	}
	return response, data, nil
}

// icon 下载并校验一个图标文件：只接受按内容识别为 PNG、GIF、JPEG、WebP 或 ICO 的文件。SVG 可以内嵌脚本，不接受。
func (f *Fetcher) icon(ctx context.Context, target string) (*Icon, error) {
	_, data, err := f.get(ctx, target, MaxIcon, false)
	if err != nil {
		return nil, err
	}
	contentType := imageType(data)
	if contentType == "" {
		return nil, fmt.Errorf("%w: %s is not a supported image", ErrNoIcon, target)
	}
	return &Icon{ContentType: contentType, Data: data}, nil
}

func imageType(data []byte) string { return ImageType(data, false) }

// ImageType 按内容识别图标格式，返回 Content-Type；不是支持的格式时返回空串。自动获取的图标不接受 SVG
// （来自不可信的网站）；管理员手动设置的图标 allowSVG 为真时接受 SVG，返回时由接口加上禁止执行脚本的响应头。
func ImageType(data []byte, allowSVG bool) string {
	if len(data) == 0 {
		return ""
	}
	if allowSVG && isSVG(data) {
		return "image/svg+xml"
	}
	switch detected := http.DetectContentType(data); detected {
	case "image/png", "image/gif", "image/jpeg", "image/webp", "image/x-icon", "image/vnd.microsoft.icon":
		return detected
	}
	// 部分 ICO 文件的头部不被识别（例如资源类型为光标），按文件头补充判断。
	if bytes.HasPrefix(data, []byte{0, 0, 1, 0}) {
		return "image/x-icon"
	}
	return ""
}

// isSVG 判断内容是否是 SVG：开头（跳过 BOM、空白、XML 声明、注释和 DOCTYPE，DOCTYPE 可以带 [...] 内部子集，
// 例如 Illustrator 导出的文件）是 <svg 元素。
func isSVG(data []byte) bool {
	text := strings.TrimPrefix(string(data), "\ufeff")
	for {
		text = strings.TrimLeft(text, " \t\r\n")
		lower := strings.ToLower(text[:min(len(text), 16)])
		switch {
		case strings.HasPrefix(lower, "<svg"):
			return true
		case strings.HasPrefix(lower, "<?xml"):
			end := strings.Index(text, "?>")
			if end < 0 {
				return false
			}
			text = text[end+2:]
		case strings.HasPrefix(lower, "<!--"):
			end := strings.Index(text, "-->")
			if end < 0 {
				return false
			}
			text = text[end+3:]
		case strings.HasPrefix(lower, "<!doctype"):
			head := text
			if subset := strings.IndexByte(head, '['); subset >= 0 && subset < strings.IndexByte(head, '>') {
				closing := strings.IndexByte(head[subset:], ']')
				if closing < 0 {
					return false
				}
				head = head[subset+closing:]
			}
			end := strings.IndexByte(head, '>')
			if end < 0 {
				return false
			}
			text = head[end+1:]
		default:
			return false
		}
	}
}

// FetchImage 下载管理员指定的图片地址作为手动图标：同样只访问公网域名的 80、443 端口并做内网检查，
// 接受 PNG、GIF、JPEG、WebP、ICO 和 SVG，不超过 MaxIcon。
func (f *Fetcher) FetchImage(ctx context.Context, target string) (*Icon, error) {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%w: invalid URL", ErrForbidden)
	}
	if err := checkURL(parsed); err != nil {
		return nil, err
	}
	_, data, err := f.get(ctx, parsed.String(), MaxIcon, false)
	if err != nil {
		return nil, err
	}
	contentType := ImageType(data, true)
	if contentType == "" {
		return nil, fmt.Errorf("%w: %s is not a supported image", ErrNoIcon, parsed.Redacted())
	}
	return &Icon{ContentType: contentType, Data: data}, nil
}

// pageIcons 读取首页，按偏好顺序返回声明的图标地址和用于解析相对地址的最终页面地址。
func (f *Fetcher) pageIcons(ctx context.Context, target string) ([]string, *url.URL, error) {
	response, data, err := f.get(ctx, target, maxPage, true)
	var base *url.URL
	if response != nil && response.Request != nil {
		base = response.Request.URL
	}
	if err != nil {
		return nil, base, err
	}
	return parseIconLinks(data), base, nil
}

// parseIconLinks 从 HTML 中取出 <link rel="icon">、<link rel="shortcut icon"> 和 <link rel="apple-touch-icon"> 的地址，
// icon 在前；跳过 SVG 和 data: 地址。
func parseIconLinks(page []byte) []string {
	var icons, touch []string
	tokenizer := html.NewTokenizer(bytes.NewReader(page))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return append(icons, touch...)
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tokenizer.TagName()
			if string(name) == "body" {
				return append(icons, touch...)
			}
			if string(name) != "link" || !hasAttr {
				continue
			}
			var rel, href, kind string
			for {
				key, value, more := tokenizer.TagAttr()
				switch string(key) {
				case "rel":
					rel = strings.ToLower(string(value))
				case "href":
					href = strings.TrimSpace(string(value))
				case "type":
					kind = strings.ToLower(string(value))
				}
				if !more {
					break
				}
			}
			if href == "" || strings.HasPrefix(strings.ToLower(href), "data:") || strings.Contains(kind, "svg") || strings.HasSuffix(strings.ToLower(strings.SplitN(href, "?", 2)[0]), ".svg") {
				continue
			}
			fields := strings.Fields(rel)
			switch {
			case contains(fields, "icon"):
				icons = append(icons, href)
			case contains(fields, "apple-touch-icon"), contains(fields, "apple-touch-icon-precomposed"):
				touch = append(touch, href)
			}
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// checkURL 只允许 http/https、默认端口和公网域名。
func checkURL(target *url.URL) error {
	if target.Scheme != "https" && target.Scheme != "http" {
		return ErrForbidden
	}
	if port := target.Port(); port != "" && port != "80" && port != "443" {
		return ErrForbidden
	}
	if !PublicDomain(target.Hostname()) {
		return ErrForbidden
	}
	return nil
}

// PublicDomain 判断主机名是否是公网域名：不是 IP 地址，并且以公共后缀列表中的后缀结尾（排除 .lan、.local 等内部名称）。
func PublicDomain(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /\\:@[]") {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	suffix, icann := publicsuffix.PublicSuffix(host)
	if !icann && !strings.Contains(suffix, ".") {
		return false
	}
	return suffix != host
}

// publicAddr 判断地址是否是公网单播地址。
func publicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() || addr.IsInterfaceLocalMulticast() {
		return false
	}
	for _, prefix := range reserved {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// reserved 是公网之外的保留地址段。198.18.0.0/15 也在其中：透明代理的 fake-ip DNS 用它作为假地址，
// 连接它等于交给本机的透明代理重新解析，检查就失去了意义；系统 DNS 返回这类地址的主机取不到图标。
var reserved = []netip.Prefix{
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// guardedDial 在连接前检查目标：端口只能是 80 或 443，域名必须是公网域名，本机解析结果必须全部是公网地址；
// 然后依次连接检查过的地址（直连或经下载代理），不再解析第二次。
func (f *Fetcher) guardedDial(ctx context.Context, _, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if (port != "80" && port != "443") || !PublicDomain(host) {
		return nil, ErrForbidden
	}
	addrs, err := f.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%s has no address", host)
	}
	for _, addr := range addrs {
		if !publicAddr(addr) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrForbidden, host, addr)
		}
	}
	var lastErr error
	for index, addr := range addrs {
		if index == maxAddrs {
			break
		}
		conn, err := f.dial(ctx, net.JoinHostPort(addr.Unmap().String(), port))
		if err == nil || errors.Is(err, ErrProxy) || ctx.Err() != nil {
			return conn, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// dialTarget 直连或经下载代理连接目标。连接代理失败时返回 ErrProxy。
func (f *Fetcher) dialTarget(ctx context.Context, target string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: dialTimeout}
	switch f.cfg.Kind {
	case "", deps.ProxyDirect:
		return dialer.DialContext(ctx, "tcp", target)
	case deps.ProxySOCKS5:
		var auth *proxy.Auth
		if f.cfg.Username != "" {
			auth = &proxy.Auth{User: f.cfg.Username, Password: f.cfg.Password}
		}
		socks, err := proxy.SOCKS5("tcp", f.cfg.Address, auth, dialer)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrProxy, err)
		}
		conn, err := socks.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
		if err != nil {
			return nil, proxyError(err)
		}
		return conn, nil
	case deps.ProxyHTTP:
		return f.connect(ctx, dialer, target)
	default:
		return nil, fmt.Errorf("%w: unknown proxy kind %s", ErrProxy, f.cfg.Kind)
	}
}

// proxyError 把连不上代理和代理认证失败归为 ErrProxy；代理已连上、但它连不上目标的情况算网站的失败。
func proxyError(err error) error {
	message := err.Error()
	if strings.Contains(message, "username/password authentication failed") || strings.Contains(message, "no acceptable authentication methods") {
		return fmt.Errorf("%w: %v", ErrProxy, err)
	}
	// SOCKS 客户端把连接代理的错误包在一层 Op 为 connect 的 OpError 里，要沿错误链找 Op 为 dial 的那一层。
	for current := err; current != nil; current = errors.Unwrap(current) {
		if opErr, ok := current.(*net.OpError); ok && opErr.Op == "dial" {
			return fmt.Errorf("%w: %v", ErrProxy, err)
		}
	}
	return err
}

// connect 经 HTTP 代理用 CONNECT 建立隧道。
func (f *Fetcher) connect(ctx context.Context, dialer *net.Dialer, target string) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, "tcp", f.cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProxy, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(dialTimeout))
	}
	request := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if f.cfg.Username != "" {
		request += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(f.cfg.Username+":"+f.cfg.Password)) + "\r\n"
	}
	if _, err := io.WriteString(conn, request+"\r\n"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrProxy, err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrProxy, err)
	}
	response.Body.Close()
	switch {
	case response.StatusCode == http.StatusProxyAuthRequired:
		conn.Close()
		return nil, fmt.Errorf("%w: proxy returned %s", ErrProxy, response.Status)
	case response.StatusCode != http.StatusOK:
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s returned %s", target, response.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
