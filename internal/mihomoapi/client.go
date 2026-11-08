// Package mihomoapi 调用运行中 mihomo 的 external-controller。
package mihomoapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Client 访问一个 mihomo 实例。Base 形如 http://127.0.0.1:9090，Secret 可为空。
type Client struct {
	Base   string
	Secret string
	HTTP   *http.Client
}

func (c Client) get(ctx context.Context, path string, query url.Values, out any) error {
	target := strings.TrimSuffix(c.Base, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	if c.Secret != "" {
		request.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &failure)
		if failure.Message != "" {
			return fmt.Errorf("%s", failure.Message)
		}
		return fmt.Errorf("controller returned %s", response.Status)
	}
	return json.Unmarshal(body, out)
}

// Proxy 是一个代理或代理组。
type Proxy struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// builtin 是不参与测速的内置类型。
var builtin = []string{"Direct", "Reject", "RejectDrop", "Compatible", "Pass", "PassRule", "Dns"}

// globalGroup 是 mihomo 自带的代理组，不是配置中的内容。
const globalGroup = "GLOBAL"

// Proxies 返回可以测速的代理和代理组，按名字排序。
func (c Client) Proxies(ctx context.Context) ([]Proxy, error) {
	var response struct {
		Proxies map[string]Proxy `json:"proxies"`
	}
	if err := c.get(ctx, "/proxies", nil, &response); err != nil {
		return nil, err
	}
	out := []Proxy{}
	for name, proxy := range response.Proxies {
		if name != globalGroup && !slices.Contains(builtin, proxy.Type) {
			proxy.Name = name
			out = append(out, proxy)
		}
	}
	slices.SortFunc(out, func(a, b Proxy) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Delay 测试代理访问 testURL 的延迟（毫秒）。
func (c Client) Delay(ctx context.Context, name, testURL string, timeout time.Duration) (int, error) {
	var response struct {
		Delay int `json:"delay"`
	}
	query := url.Values{"url": {testURL}, "timeout": {strconv.FormatInt(timeout.Milliseconds(), 10)}}
	err := c.get(ctx, "/proxies/"+url.PathEscape(name)+"/delay", query, &response)
	return response.Delay, err
}

// Connection 是 mihomo 当前活动的一条连接。
type Connection struct {
	ID       string `json:"id"`
	Metadata struct {
		Type       string `json:"type"`
		SourceIP   string `json:"sourceIP"`
		SourcePort string `json:"sourcePort"`
		Host       string `json:"host"`
	} `json:"metadata"`
	// Chains 从实际出口节点开始，依次到外层代理组。
	Chains      []string `json:"chains"`
	Rule        string   `json:"rule"`
	RulePayload string   `json:"rulePayload"`
}

// Connections 返回当前活动的连接。
func (c Client) Connections(ctx context.Context) ([]Connection, error) {
	var response struct {
		Connections []Connection `json:"connections"`
	}
	if err := c.get(ctx, "/connections", nil, &response); err != nil {
		return nil, err
	}
	return response.Connections, nil
}
