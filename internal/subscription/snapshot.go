// Package subscription 是订阅数据的公共部分：管理端导出的快照、多来源合并、按 token 出订阅，以及带签名的订阅包。
// 管理端用它预览和发布订阅包，订阅端用它导入订阅包并对外提供订阅。
package subscription

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/eagle/proxystack-web/internal/generator/sub"
)

// 停用或到期用户的订阅响应。
const (
	DisabledNotFound = "not_found"
	DisabledEmpty    = "empty"
)

// Snapshot 是一个来源（一台管理端）在某一时刻的订阅数据，也是订阅包的签名内容。
type Snapshot struct {
	Source      string     `json:"source"`
	Sequence    int64      `json:"sequence"`
	GeneratedAt time.Time  `json:"generated_at"`
	Settings    Settings   `json:"settings"`
	Templates   Templates  `json:"templates"`
	Users       []User     `json:"users"`
	Nodes       []sub.Node `json:"nodes"`
}

// Settings 是影响订阅内容的设置。
type Settings struct {
	PublicBaseURL    string `json:"public_base_url"`
	SurgeManaged     bool   `json:"surge_managed"`
	Interval         int    `json:"interval"`
	Strict           bool   `json:"strict"`
	DisabledResponse string `json:"disabled_response"`
}

// Templates 是自定义模板，空字符串表示使用内置模板。
type Templates struct {
	Clash   string `json:"clash"`
	Premium string `json:"premium"`
	Surge   string `json:"surge"`
}

func (t Templates) forFormat(format string) string {
	return map[string]string{sub.FormatClash: t.Clash, sub.FormatPremium: t.Premium, sub.FormatSurge: t.Surge}[format]
}

// User 是订阅用户。Upload/Download 是导出时的已用流量。
type User struct {
	Name       string `json:"name"`
	Token      string `json:"token"`
	Enabled    bool   `json:"enabled"`
	ExpireAt   string `json:"expire_at,omitempty"`
	QuotaBytes int64  `json:"quota_bytes"`
	Upload     int64  `json:"upload"`
	Download   int64  `json:"download"`
}

// Expired 判断用户在 now 时是否已到期。
func (u User) Expired(now time.Time) bool {
	if u.ExpireAt == "" {
		return false
	}
	expire, err := time.Parse(time.RFC3339, u.ExpireAt)
	return err == nil && !now.Before(expire)
}

var sourcePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidSourceName 判断来源名是否合法：小写字母、数字、下划线和短横线，最长 32 个字符。
func ValidSourceName(name string) bool { return sourcePattern.MatchString(name) }

// Validate 校验快照的完整性：来源名、用户名和 token 唯一、节点合法且属于已有用户。
func (s *Snapshot) Validate() error {
	if !ValidSourceName(s.Source) {
		return fmt.Errorf("invalid source name %q", s.Source)
	}
	if s.Sequence <= 0 {
		return errors.New("sequence must be positive")
	}
	switch s.Settings.DisabledResponse {
	case DisabledNotFound, DisabledEmpty:
	default:
		return fmt.Errorf("invalid disabled_response %q", s.Settings.DisabledResponse)
	}
	names, tokens := map[string]bool{}, map[string]bool{}
	for _, user := range s.Users {
		if user.Name == "" || user.Token == "" {
			return errors.New("every user needs a name and a token")
		}
		if names[user.Name] {
			return fmt.Errorf("duplicate user %s", user.Name)
		}
		if tokens[user.Token] {
			return fmt.Errorf("duplicate token for user %s", user.Name)
		}
		names[user.Name], tokens[user.Token] = true, true
	}
	for _, node := range s.Nodes {
		if !names[node.User] {
			return fmt.Errorf("node %s belongs to unknown user %s", node.ID, node.User)
		}
		if err := node.Validate(); err != nil {
			return fmt.Errorf("node %s: %w", node.ID, err)
		}
	}
	return nil
}

// View 是合并后可直接提供订阅的数据。
type View struct {
	Settings  Settings
	Templates Templates
	Users     []User
	Index     sub.Index
	// Warnings 是合并时跳过的节点。
	Warnings []string
}

// Merge 以主来源的用户、模板和设置为准，把其他来源的节点按用户名并入：
// 主来源没有的用户的节点、与同用户已有节点代理名重复的节点被跳过。
// tag 不参与判重：它不出现在订阅内容中，不同机器使用默认端口时 tag 相同是正常情况。
func Merge(primary *Snapshot, extras []*Snapshot) (*View, error) {
	view := &View{Settings: primary.Settings, Templates: primary.Templates, Users: primary.Users}
	users := map[string]bool{}
	for _, user := range primary.Users {
		users[user.Name] = true
	}
	nodes := append([]sub.Node{}, primary.Nodes...)
	names := map[string]bool{}
	for _, node := range nodes {
		names[node.User+"\x00"+node.Remark] = true
	}
	sources := []string{primary.Source}
	extras = append([]*Snapshot{}, extras...)
	sort.Slice(extras, func(i, j int) bool { return extras[i].Source < extras[j].Source })
	for _, extra := range extras {
		for _, node := range extra.Nodes {
			if !users[node.User] {
				continue
			}
			if names[node.User+"\x00"+node.Remark] {
				view.Warnings = append(view.Warnings, fmt.Sprintf("source %s: skipped node %q of user %s, the name is already used", extra.Source, node.Remark, node.User))
				continue
			}
			names[node.User+"\x00"+node.Remark] = true
			node.ID = extra.Source + ":" + node.ID
			nodes = append(nodes, node)
		}
		sources = append(sources, extra.Source)
	}
	index, err := sub.BuildIndex(nodes, sources, sub.Access{Type: "none"}, primary.GeneratedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	view.Index = index
	return view, nil
}

// UserByToken 按 token 查找用户（逐个常量时间比较）。
func (v *View) UserByToken(token string) *User {
	if token == "" {
		return nil
	}
	var found *User
	for index := range v.Users {
		if subtle.ConstantTimeCompare([]byte(v.Users[index].Token), []byte(token)) == 1 {
			found = &v.Users[index]
		}
	}
	return found
}

// Response 是一次订阅请求的结果。
type Response struct {
	Status  int
	Body    []byte
	Headers map[string]string
	// Code 是错误时的 JSON 错误码（forbidden / not_found / template_error）。
	Code string
}

// BaseURL 返回订阅链接前缀：优先用设置的对外地址，其次用请求地址。
func (v *View) BaseURL(requestURL *url.URL) string {
	if v.Settings.PublicBaseURL != "" {
		return strings.TrimSuffix(v.Settings.PublicBaseURL, "/")
	}
	if requestURL != nil && requestURL.Host != "" {
		return requestURL.Scheme + "://" + requestURL.Host
	}
	return ""
}

// Serve 渲染 token 对应用户的订阅。requestURL 用于未设置对外地址时的 Surge managed 首行。
func (v *View) Serve(token, format string, requestURL *url.URL, now time.Time) *Response {
	user := v.UserByToken(token)
	if user == nil {
		return &Response{Status: http.StatusForbidden, Code: "forbidden"}
	}
	if !user.Enabled || user.Expired(now) {
		if v.Settings.DisabledResponse == DisabledEmpty {
			return &Response{Status: http.StatusOK, Body: []byte{}, Headers: v.headers(user, format)}
		}
		return &Response{Status: http.StatusNotFound, Code: "not_found"}
	}
	if len(sub.NodesForUser(v.Index, user.Name)) == 0 {
		return &Response{Status: http.StatusNotFound, Code: "not_found"}
	}
	body, err := v.Render(user.Name, format, v.Templates.forFormat(format), v.BaseURL(requestURL)+"/s/"+token+"/surge")
	if err != nil {
		slog.Error("render subscription", "user", user.Name, "format", format, "err", err)
		var templateErr sub.TemplateError
		if errors.As(err, &templateErr) {
			return &Response{Status: http.StatusServiceUnavailable, Code: "template_error"}
		}
		return &Response{Status: http.StatusNotFound, Code: "not_found"}
	}
	return &Response{Status: http.StatusOK, Body: []byte(body), Headers: v.headers(user, format)}
}

// Render 用给定模板渲染用户的订阅；managedURL 是 Surge managed 首行的地址（关闭 managed 时忽略）。
func (v *View) Render(user, format, template, managedURL string) (string, error) {
	managed := sub.SurgeManaged{Interval: v.Settings.Interval, Strict: v.Settings.Strict}
	if format == sub.FormatSurge && v.Settings.SurgeManaged {
		managed.URL = managedURL
	}
	return sub.Render(format, v.Index, user, template, managed)
}

// headers 返回客户端识别的响应头：文件名、更新间隔和额度信息。
func (v *View) headers(user *User, format string) map[string]string {
	extension := map[string]string{sub.FormatSurge: "conf"}[format]
	if extension == "" {
		extension = "yaml"
	}
	headers := map[string]string{
		"Content-Type":        "text/plain; charset=utf-8",
		"Content-Disposition": fmt.Sprintf("attachment; filename*=UTF-8''%s.%s", url.PathEscape(user.Name), extension),
		"Cache-Control":       "no-store",
	}
	if v.Settings.Interval > 0 {
		hours := v.Settings.Interval / 3600
		if hours < 1 {
			hours = 1
		}
		headers["Profile-Update-Interval"] = strconv.Itoa(hours)
	}
	info := []string{
		"upload=" + strconv.FormatInt(user.Upload, 10),
		"download=" + strconv.FormatInt(user.Download, 10),
		"total=" + strconv.FormatInt(user.QuotaBytes, 10),
	}
	if user.ExpireAt != "" {
		if expire, err := time.Parse(time.RFC3339, user.ExpireAt); err == nil {
			info = append(info, "expire="+strconv.FormatInt(expire.Unix(), 10))
		}
	}
	headers["Subscription-Userinfo"] = strings.Join(info, "; ")
	return headers
}
