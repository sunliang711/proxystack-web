package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eagle/proxystack-web/internal/configdoc"
	"github.com/eagle/proxystack-web/internal/deps"
	"github.com/eagle/proxystack-web/internal/mihomoapi"
	"github.com/eagle/proxystack-web/internal/units"
)

// 出口 IP 的数据源和回退顺序沿用参考项目。
var egressSources = map[string][]string{
	"ipv4": {"https://ipinfo.io/json", "https://myip.ipip.net"},
	"ipv6": {"https://ifconfig.me/all.json", "https://ifconfig.co/json", "https://api64.ipify.org?format=json"},
}

const (
	defaultEgressTimeout = 8 * time.Second
	defaultDelayTimeout  = 5 * time.Second
	delayConcurrency     = 8
)

var ipCandidate = regexp.MustCompile(`[0-9A-Fa-f:.]+`)

// EgressSource 是一个数据源的查询结果。
type EgressSource struct {
	URL    string `json:"url"`
	IP     string `json:"ip"`
	Region string `json:"region"`
	Error  string `json:"error"`
}

// EgressResult 是一个地址族的出口 IP。
type EgressResult struct {
	Family  string         `json:"family" enum:"ipv4,ipv6"`
	IP      string         `json:"ip"`
	Region  string         `json:"region"`
	Sources []EgressSource `json:"sources"`
}

// parseEgress 从数据源的响应中取出 IP 和地区：JSON 响应按字段读取，纯文本响应取第一个合法地址。
func parseEgress(body []byte) (ip, region string) {
	var object map[string]any
	if json.Unmarshal(body, &object) == nil {
		for _, key := range []string{"ip", "ip_addr", "ip_address"} {
			if value, ok := object[key].(string); ok && value != "" {
				ip = value
				break
			}
		}
		parts := []string{}
		for _, key := range []string{"country", "region", "city"} {
			if value, ok := object[key].(string); ok && value != "" {
				parts = append(parts, value)
			}
		}
		return ip, strings.Join(parts, " ")
	}
	text := string(body)
	for _, candidate := range ipCandidate.FindAllString(text, -1) {
		if _, err := netip.ParseAddr(candidate); err == nil {
			ip = candidate
			break
		}
	}
	if _, after, ok := strings.Cut(text, "来自于："); ok {
		region = strings.TrimSpace(after)
	}
	return ip, region
}

func matchesFamily(ip, family string) bool {
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	return (family == "ipv4") == address.Unmap().Is4()
}

// StackEgress 经 stack 的 mihomo socks 监听查询出口 IP。family 为 ipv4、ipv6 或 all。
func (s *Service) StackEgress(ctx context.Context, id int64, family string, timeout time.Duration) ([]EgressResult, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	stack, err := stackByID(doc, id)
	if err != nil {
		return nil, err
	}
	if err := s.requireRunning(ctx, stack.Name); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = defaultEgressTimeout
	}
	listener := stack.Mihomo.Socks
	host := listener.Listen
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	proxy := deps.ProxyConfig{Kind: deps.ProxySOCKS5, Address: net.JoinHostPort(host, fmt.Sprint(listener.Port)), RemoteDNS: true}
	if listener.Auth == configdoc.ListenerAuthAccounts && len(listener.Accounts) > 0 {
		proxy.Username, proxy.Password = listener.Accounts[0].Username, listener.Accounts[0].Password
	}
	fetcher, err := deps.NewHTTPFetcher(proxy)
	if err != nil {
		return nil, err
	}
	families := []string{family}
	if family == "all" {
		families = []string{"ipv4", "ipv6"}
	}
	out := make([]EgressResult, len(families))
	var wg sync.WaitGroup
	for index, name := range families {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[index] = queryEgress(ctx, fetcher, name, timeout)
		}()
	}
	wg.Wait()
	return out, nil
}

func queryEgress(ctx context.Context, fetcher deps.Fetcher, family string, timeout time.Duration) EgressResult {
	result := EgressResult{Family: family, Sources: []EgressSource{}}
	for _, source := range egressSources[family] {
		item := EgressSource{URL: source}
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		body, err := fetcher.Get(requestCtx, source)
		cancel()
		switch {
		case err != nil:
			item.Error = err.Error()
		default:
			item.IP, item.Region = parseEgress(body)
			if !matchesFamily(item.IP, family) {
				item.IP, item.Region, item.Error = "", "", "响应中没有该地址族的地址"
			}
		}
		result.Sources = append(result.Sources, item)
		if item.IP != "" {
			result.IP, result.Region = item.IP, item.Region
			break
		}
	}
	return result
}

func stackByID(doc *configdoc.Document, id int64) (*configdoc.Stack, error) {
	index := slices.IndexFunc(doc.Stacks, func(stack configdoc.Stack) bool { return stack.ID == id })
	if index < 0 {
		return nil, ErrNotFound
	}
	return &doc.Stacks[index], nil
}

func (s *Service) requireRunning(ctx context.Context, stack string) error {
	unit := units.Name(units.Mihomo, stack)
	states, err := s.Units.States(ctx, []string{unit})
	if err != nil {
		return err
	}
	if !states[unit].Running() {
		return &FieldError{Field: "stack", Message: "mihomo of stack " + stack + " is not running"}
	}
	return nil
}

func (s *Service) mihomoClient(ctx context.Context, id int64) (mihomoapi.Client, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return mihomoapi.Client{}, err
	}
	stack, err := stackByID(doc, id)
	if err != nil {
		return mihomoapi.Client{}, err
	}
	if err := s.requireRunning(ctx, stack.Name); err != nil {
		return mihomoapi.Client{}, err
	}
	base := "http://" + net.JoinHostPort("127.0.0.1", fmt.Sprint(stack.Mihomo.ControllerPort))
	return mihomoapi.Client{Base: base, Secret: stack.Mihomo.ControllerSecret, HTTP: &http.Client{Timeout: 30 * time.Second}}, nil
}

// StackProxies 返回 stack 可以测速的代理和代理组。
func (s *Service) StackProxies(ctx context.Context, id int64) ([]mihomoapi.Proxy, error) {
	client, err := s.mihomoClient(ctx, id)
	if err != nil {
		return nil, err
	}
	proxies, err := client.Proxies(ctx)
	if err != nil {
		return nil, &FieldError{Field: "stack", Message: "query mihomo controller: " + err.Error()}
	}
	return proxies, nil
}

// DelayResult 是一个代理的延迟测试结果。
type DelayResult struct {
	Name  string `json:"name"`
	Delay int    `json:"delay" doc:"毫秒，失败时为 0"`
	Error string `json:"error"`
}

// StackDelay 测试 stack 中选定代理的延迟，names 为空时测试全部。
func (s *Service) StackDelay(ctx context.Context, id int64, names []string, testURL string, timeout time.Duration) ([]DelayResult, error) {
	client, err := s.mihomoClient(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		proxies, err := client.Proxies(ctx)
		if err != nil {
			return nil, &FieldError{Field: "stack", Message: "query mihomo controller: " + err.Error()}
		}
		for _, proxy := range proxies {
			names = append(names, proxy.Name)
		}
	}
	if testURL == "" {
		testURL = configdoc.DefaultTestURL
	}
	if timeout <= 0 {
		timeout = defaultDelayTimeout
	}
	results := make([]DelayResult, len(names))
	limit := make(chan struct{}, delayConcurrency)
	var wg sync.WaitGroup
	for index, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			delay, err := client.Delay(ctx, name, testURL, timeout)
			results[index] = DelayResult{Name: name, Delay: delay}
			if err != nil {
				results[index].Error = err.Error()
			}
		}()
	}
	wg.Wait()
	return results, nil
}
