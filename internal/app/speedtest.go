package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/deps"
	"proxystack-web/internal/mihomoapi"
)

// 下载测速的模式与限制。
const (
	SpeedModeDuration = "duration"
	SpeedModeSize     = "size"

	// DefaultSpeedURL 是默认测速地址，{bytes} 替换为本次请求的字节数。
	DefaultSpeedURL = "https://speed.cloudflare.com/__down?bytes={bytes}"
	// speedChunk 是单次请求的字节数，Cloudflare 拒绝 100 MB 及以上的请求。
	speedChunk = 50_000_000
	// speedDurationByteCap 是时长模式的流量上限，避免高带宽线路在限定时间内消耗过多流量。
	speedDurationByteCap = 1_000_000_000
	// speedSizeTimeout 是大小模式的时长上限，避免慢速出口长时间占用。
	speedSizeTimeout = 60 * time.Second
	// speedFirstByteTimeout 是等待首字节的时长上限。
	speedFirstByteTimeout = 15 * time.Second
	// speedWarmup 是计算稳定速度时排除的起始阶段（TCP 慢启动）。
	speedWarmup           = time.Second
	speedProgressInterval = 500 * time.Millisecond
	speedCheckInterval    = 100 * time.Millisecond
)

// 测速结束的原因。
const (
	SpeedStopDuration  = "duration"
	SpeedStopSize      = "size"
	SpeedStopByteCap   = "byte_cap"
	SpeedStopTimeout   = "timeout"
	SpeedStopCancelled = "cancelled"
	SpeedStopError     = "error"
)

// SpeedTestOptions 是一次下载测速的参数。
type SpeedTestOptions struct {
	Listener string `json:"listener" enum:"socks,http"`
	Mode     string `json:"mode" enum:"duration,size"`
	Seconds  int    `json:"seconds"`
	MB       int    `json:"mb"`
	URL      string `json:"url"`
}

// SpeedTestProgress 是测速进行中的一次采样。
type SpeedTestProgress struct {
	ElapsedMS int64   `json:"elapsed_ms" doc:"自收到首字节起的毫秒数"`
	Bytes     int64   `json:"bytes"`
	Mbps      float64 `json:"mbps" doc:"最近一个采样间隔的速度"`
}

// SpeedTestResult 是一次下载测速的结果。
type SpeedTestResult struct {
	Listener    string   `json:"listener"`
	Mode        string   `json:"mode"`
	URL         string   `json:"url"`
	Bytes       int64    `json:"bytes"`
	ElapsedMS   int64    `json:"elapsed_ms" doc:"自收到首字节起的下载用时"`
	FirstByteMS int64    `json:"first_byte_ms" doc:"从发起请求到收到首字节的毫秒数"`
	AvgMbps     float64  `json:"avg_mbps"`
	StableMbps  float64  `json:"stable_mbps" doc:"排除首字节后第一秒的平均速度"`
	PeakMbps    float64  `json:"peak_mbps" doc:"一秒窗口内的最高速度"`
	Chain       []string `json:"chain" doc:"实际走的代理链，从外层代理组到出口节点；无法查询时为空"`
	Rule        string   `json:"rule"`
	RulePayload string   `json:"rule_payload"`
	StopReason  string   `json:"stop_reason" enum:"duration,size,byte_cap,timeout,cancelled,error,"`
	Error       string   `json:"error"`
}

// speedPlan 是校验后的测速计划。
type speedPlan struct {
	url       string
	template  bool
	duration  time.Duration
	target    int64
	timeout   time.Duration
	byteCap   int64
	firstByte time.Duration
}

func newSpeedPlan(options SpeedTestOptions) (speedPlan, error) {
	plan := speedPlan{url: strings.TrimSpace(options.URL), firstByte: speedFirstByteTimeout}
	if plan.url == "" {
		plan.url = DefaultSpeedURL
	}
	parsed, err := url.Parse(plan.url)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return plan, &FieldError{Field: "url", Message: "测速地址必须是 http 或 https 地址"}
	}
	plan.template = strings.Contains(plan.url, "{bytes}")
	switch options.Mode {
	case SpeedModeDuration:
		if options.Seconds < 5 || options.Seconds > 60 {
			return plan, &FieldError{Field: "seconds", Message: "测速时长须在 5 到 60 秒之间"}
		}
		plan.duration = time.Duration(options.Seconds) * time.Second
		plan.byteCap = speedDurationByteCap
		plan.timeout = plan.firstByte + plan.duration
	case SpeedModeSize:
		if options.MB < 10 || options.MB > 1000 {
			return plan, &FieldError{Field: "mb", Message: "测速大小须在 10 到 1000 MB 之间"}
		}
		plan.target = int64(options.MB) * 1_000_000
		plan.timeout = speedSizeTimeout
	default:
		return plan, &FieldError{Field: "mode", Message: "unknown speed test mode: " + options.Mode}
	}
	return plan, nil
}

// limit 返回本次测速的字节上限。
func (p speedPlan) limit() int64 {
	if p.target > 0 {
		return p.target
	}
	return p.byteCap
}

// requestURL 返回下一次请求的地址，remaining 是距字节上限还差的字节数。
func (p speedPlan) requestURL(remaining int64) string {
	if !p.template {
		return p.url
	}
	return strings.ReplaceAll(p.url, "{bytes}", strconv.FormatInt(min(remaining, speedChunk), 10))
}

// speedSample 是某一时刻累计下载的字节数，时刻相对首字节。
type speedSample struct {
	at    time.Duration
	bytes int64
}

func mbps(bytes int64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(bytes) * 8 / elapsed.Seconds() / 1e6
}

// summarize 由采样计算平均、稳定和峰值速度。samples 按时间递增，最后一个是结束时刻。
func summarize(samples []speedSample) (avg, stable, peak float64) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	end := samples[len(samples)-1]
	avg = mbps(end.bytes, end.at)
	stable = avg
	if end.at >= 2*speedWarmup {
		for _, sample := range samples {
			if sample.at >= speedWarmup {
				stable = mbps(end.bytes-sample.bytes, end.at-sample.at)
				break
			}
		}
	}
	start := 0
	for index, sample := range samples {
		for start < index && sample.at-samples[start+1].at >= time.Second {
			start++
		}
		if window := sample.at - samples[start].at; window >= time.Second {
			peak = max(peak, mbps(sample.bytes-samples[start].bytes, window))
		}
	}
	if peak == 0 {
		peak = avg
	}
	return avg, stable, peak
}

// runSpeedTest 经 client 反复下载直到满足计划的停止条件。firstByte 在收到首字节时异步调用一次，返回前等待其结束；
// progress 在每个采样间隔调用一次。
func runSpeedTest(ctx context.Context, client *http.Client, plan speedPlan, firstByte func(), progress func(SpeedTestProgress)) SpeedTestResult {
	result := SpeedTestResult{URL: plan.url}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	started := time.Now()
	var (
		total     atomic.Int64
		firstAt   atomic.Int64 // 首字节时刻（Unix 纳秒），0 表示尚未收到
		stopMu    sync.Mutex
		stop      string
		samplesMu sync.Mutex
		samples   []speedSample
		callbacks sync.WaitGroup
	)
	defer callbacks.Wait()
	setStop := func(reason string) {
		stopMu.Lock()
		if stop == "" {
			stop = reason
		}
		stopMu.Unlock()
		cancel()
	}
	since := func(now time.Time) time.Duration { return now.Sub(time.Unix(0, firstAt.Load())) }

	done := make(chan struct{})
	go func() {
		defer close(done)
		check := time.NewTicker(speedCheckInterval)
		defer check.Stop()
		lastReport := time.Duration(0)
		var lastBytes int64
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-check.C:
				if firstAt.Load() == 0 {
					if now.Sub(started) >= plan.firstByte {
						setStop(SpeedStopTimeout)
					}
					continue
				}
				elapsed, bytes := since(now), total.Load()
				if elapsed-lastReport >= speedProgressInterval {
					samplesMu.Lock()
					samples = append(samples, speedSample{elapsed, bytes})
					samplesMu.Unlock()
					if progress != nil {
						progress(SpeedTestProgress{ElapsedMS: elapsed.Milliseconds(), Bytes: bytes, Mbps: mbps(bytes-lastBytes, elapsed-lastReport)})
					}
					lastReport, lastBytes = elapsed, bytes
				}
				switch {
				case plan.duration > 0 && elapsed >= plan.duration:
					setStop(SpeedStopDuration)
				case now.Sub(started) >= plan.timeout:
					setStop(SpeedStopTimeout)
				}
			}
		}
	}()

	buffer := make([]byte, 64<<10)
	limit := plan.limit()
	var downloadErr error
	for downloadErr == nil && total.Load() < limit {
		downloadErr = func() error {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, plan.requestURL(limit-total.Load()), nil)
			if err != nil {
				return err
			}
			request.Header.Set("Accept-Encoding", "identity")
			response, err := client.Do(request)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("测速地址返回 %s", response.Status)
			}
			received := false
			for {
				want := min(int64(len(buffer)), limit-total.Load())
				if want <= 0 {
					return nil
				}
				n, err := response.Body.Read(buffer[:want])
				if n > 0 {
					if firstAt.Load() == 0 {
						now := time.Now()
						firstAt.Store(now.UnixNano())
						result.FirstByteMS = now.Sub(started).Milliseconds()
						if firstByte != nil {
							callbacks.Go(firstByte)
						}
					}
					total.Add(int64(n))
					received = true
				}
				if err == io.EOF {
					if !received {
						return errors.New("测速地址返回了空响应")
					}
					return nil
				}
				if err != nil {
					return err
				}
			}
		}()
	}
	end := time.Now()
	if total.Load() >= limit {
		if plan.target > 0 {
			setStop(SpeedStopSize)
		} else {
			setStop(SpeedStopByteCap)
		}
	}
	cancel()
	<-done
	stopMu.Lock()
	result.StopReason = stop
	stopMu.Unlock()
	if result.StopReason == "" {
		result.StopReason = SpeedStopError
		if errors.Is(downloadErr, context.Canceled) {
			result.StopReason = SpeedStopCancelled
		}
		if downloadErr != nil {
			result.Error = deps.ClassifyError(downloadErr).Error()
		}
	}
	if result.StopReason == SpeedStopTimeout && firstAt.Load() == 0 {
		result.Error = fmt.Sprintf("%d 秒内没有收到数据", int(plan.firstByte.Seconds()))
	}
	result.Bytes = total.Load()
	if firstAt.Load() != 0 {
		elapsed := since(end)
		if plan.duration > 0 {
			elapsed = min(elapsed, plan.duration)
		}
		result.ElapsedMS = elapsed.Milliseconds()
		samplesMu.Lock()
		all := append([]speedSample{{}}, samples...)
		all = append(all, speedSample{elapsed, result.Bytes})
		samplesMu.Unlock()
		result.AvgMbps, result.StableMbps, result.PeakMbps = summarize(all)
	}
	return result
}

// portRecorder 建立到监听的 TCP 连接，并记录本端端口，用于在 mihomo 连接列表中找到测速连接。
type portRecorder struct {
	dialer net.Dialer
	mu     sync.Mutex
	ports  []string
}

func (r *portRecorder) Dial(network, address string) (net.Conn, error) {
	return r.DialContext(context.Background(), network, address)
}

func (r *portRecorder) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := r.dialer.DialContext(ctx, network, address)
	if err == nil {
		if local, ok := conn.LocalAddr().(*net.TCPAddr); ok {
			r.mu.Lock()
			r.ports = append(r.ports, strconv.Itoa(local.Port))
			r.mu.Unlock()
		}
	}
	return conn, err
}

func (r *portRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ports)
}

// matchConnection 在 mihomo 连接列表中找到测速连接：优先按本端端口匹配，其次按目标域名唯一匹配。
func matchConnection(connections []mihomoapi.Connection, ports []string, host string) *mihomoapi.Connection {
	for index := range connections {
		if slices.Contains(ports, connections[index].Metadata.SourcePort) {
			return &connections[index]
		}
	}
	var found *mihomoapi.Connection
	for index := range connections {
		if connections[index].Metadata.Host == host {
			if found != nil {
				return nil
			}
			found = &connections[index]
		}
	}
	return found
}

// listenerProxy 返回经 stack 的 mihomo 本机监听访问外部的代理设置。kind 为 socks 或 http。
func listenerProxy(stack configdoc.Stack, kind string) deps.ProxyConfig {
	listener, proxyKind := stack.Mihomo.Socks, deps.ProxySOCKS5
	if kind == "http" {
		listener, proxyKind = stack.Mihomo.HTTP, deps.ProxyHTTP
	}
	host := listener.Listen
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	proxy := deps.ProxyConfig{Kind: proxyKind, Address: net.JoinHostPort(host, strconv.Itoa(listener.Port)), RemoteDNS: true}
	if listener.Auth == configdoc.ListenerAuthAccounts && len(listener.Accounts) > 0 {
		proxy.Username, proxy.Password = listener.Accounts[0].Username, listener.Accounts[0].Password
	}
	return proxy
}

// appliedStack 返回 stack 当前已应用版本中的配置。
func (s *Service) appliedStack(ctx context.Context, name string) (*configdoc.Stack, error) {
	applied, err := s.Store.Q.ListAppliedState(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range applied {
		if row.StackName != name {
			continue
		}
		snapshot, err := s.RevisionSnapshot(ctx, row.RevisionID)
		if err != nil {
			return nil, err
		}
		if index := snapshot.StackByName(name); index >= 0 {
			return &snapshot.Stacks[index], nil
		}
	}
	return nil, &FieldError{Field: "stack", Message: "stack " + name + " 还没有应用过配置"}
}

// StackSpeedTest 经 stack 已应用配置中的 mihomo socks 或 http 监听下载测速。
// 同一时间只允许一个测速，所有 stack 共用本机带宽，并发测速会互相影响结果。
func (s *Service) StackSpeedTest(ctx context.Context, id int64, options SpeedTestOptions, progress func(SpeedTestProgress)) (*SpeedTestResult, error) {
	if options.Listener == "" {
		options.Listener = "socks"
	}
	if options.Listener != "socks" && options.Listener != "http" {
		return nil, &FieldError{Field: "listener", Message: "unknown listener: " + options.Listener}
	}
	plan, err := newSpeedPlan(options)
	if err != nil {
		return nil, err
	}
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	saved, err := stackByID(doc, id)
	if err != nil {
		return nil, err
	}
	stack, err := s.appliedStack(ctx, saved.Name)
	if err != nil {
		return nil, err
	}
	if !stack.Mihomo.Enabled {
		return nil, &FieldError{Field: "stack", Message: "stack " + stack.Name + " 没有启用 mihomo"}
	}
	if options.Listener == "http" && !stack.Mihomo.HTTP.Enabled {
		return nil, &FieldError{Field: "listener", Message: "stack " + stack.Name + " 已应用的配置没有启用 HTTP 监听"}
	}
	if err := s.requireRunning(ctx, stack.Name); err != nil {
		return nil, err
	}
	if !s.speedTestMu.TryLock() {
		return nil, &FieldError{Field: "stack", Message: "另一个测速正在进行，请等它结束后再试"}
	}
	defer s.speedTestMu.Unlock()

	recorder := &portRecorder{dialer: net.Dialer{Timeout: 10 * time.Second}}
	transport, err := deps.NewTransport(listenerProxy(*stack, options.Listener), recorder)
	if err != nil {
		return nil, err
	}
	transport.DisableCompression = true
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}

	controller := mihomoapi.Client{
		Base:   "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(stack.Mihomo.ControllerPort)),
		Secret: stack.Mihomo.ControllerSecret,
		HTTP:   &http.Client{Timeout: 5 * time.Second},
	}
	host := ""
	if parsed, err := url.Parse(plan.url); err == nil {
		host = parsed.Hostname()
	}
	var (
		connMu sync.Mutex
		conn   *mihomoapi.Connection
	)
	lookup := func() {
		connections, err := controller.Connections(ctx)
		if err != nil {
			return
		}
		if found := matchConnection(connections, recorder.recorded(), host); found != nil {
			connMu.Lock()
			conn = found
			connMu.Unlock()
		}
	}
	result := runSpeedTest(ctx, client, plan, lookup, progress)
	result.Listener, result.Mode = options.Listener, options.Mode
	connMu.Lock()
	defer connMu.Unlock()
	result.Chain = []string{}
	if conn != nil {
		result.Chain = slices.Clone(conn.Chains)
		slices.Reverse(result.Chain)
		result.Rule, result.RulePayload = conn.Rule, conn.RulePayload
	}
	return &result, nil
}
