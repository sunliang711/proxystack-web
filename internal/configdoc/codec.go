package configdoc

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// 文件格式。
const (
	FormatYAML = "yaml"
	FormatJSON = "json"
)

// Parse 解析 YAML 或 JSON 文档并检查文件头。未知字段视为错误。
func Parse(data []byte) (*Document, error) {
	doc := &Document{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(doc); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
	} else {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(doc); err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}
	}
	if doc.Schema != Schema {
		return nil, fmt.Errorf("schema must be %s", Schema)
	}
	if doc.Version != Version {
		return nil, fmt.Errorf("unsupported version %d", doc.Version)
	}
	doc.Normalize()
	return doc, nil
}

// Marshal 按格式输出文档。
func Marshal(doc *Document, format string) ([]byte, error) {
	switch format {
	case FormatJSON:
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(data, '\n'), nil
	case FormatYAML, "":
		var buf bytes.Buffer
		encoder := yaml.NewEncoder(&buf)
		encoder.SetIndent(2)
		if err := encoder.Encode(doc); err != nil {
			return nil, err
		}
		if err := encoder.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

// Normalize 补齐空列表和缺省枚举值，使 JSON 输出稳定（空列表输出为 [] 而不是 null）。
func (d *Document) Normalize() {
	if d.Settings != nil {
		d.Settings.Normalize()
	}
	for index := range d.Users {
		d.Users[index].Normalize()
	}
	for index := range d.Stacks {
		d.Stacks[index].Normalize()
	}
	if d.Templates == nil {
		d.Templates = &Templates{}
	}
	for index := range d.ManualNodes {
		node := &d.ManualNodes[index]
		if node.Mode == "" {
			node.Mode = NodeStandard
		}
		if node.WSHeaders == nil {
			node.WSHeaders = map[string]string{}
		}
	}
}

// Normalize 用默认值补齐设置中缺失的分组和字段（例如较早版本导出的文件）。
func (s *Settings) Normalize() {
	defaults := DefaultSettings()
	// 旧配置未包含访问日志开关时，保留原有的开启行为。
	if s.Defaults.Xray.AccessLog == nil {
		s.Defaults.Xray.AccessLog = defaults.Defaults.Xray.AccessLog
	}
	if s.Defaults.Xray.APIServices == nil {
		s.Defaults.Xray.APIServices = []string{}
	}
	for _, pair := range []struct{ value, fallback *DepSource }{
		{&s.Deps.Xray, &defaults.Deps.Xray}, {&s.Deps.Mihomo, &defaults.Deps.Mihomo}, {&s.Deps.Geo, &defaults.Deps.Geo},
	} {
		if pair.value.Version == "" {
			pair.value.Version = pair.fallback.Version
		}
		if pair.value.Source == "" {
			pair.value.Source = pair.fallback.Source
		}
	}
	fill := func(target *string, value string) {
		if *target == "" {
			*target = value
		}
	}
	fill(&s.Deps.MihomoVariant, defaults.Deps.MihomoVariant)
	fill(&s.Deps.WindowStart, defaults.Deps.WindowStart)
	fill(&s.Deps.WindowEnd, defaults.Deps.WindowEnd)
	if s.Subscription.SourceName == "" && s.Subscription.Interval == 0 && s.Subscription.DisabledResponse == "" {
		s.Subscription = defaults.Subscription
	}
	fill(&s.Subscription.DisabledResponse, defaults.Subscription.DisabledResponse)
	if s.Traffic.Interval == 0 {
		s.Traffic = defaults.Traffic
	}
	s.Traffic.PauseWindows = normalizePauseWindows(s.Traffic.PauseWindows)
	if s.Notify.WarnPercent == 0 {
		s.Notify = defaults.Notify
	}
	if s.Notify.To == nil {
		s.Notify.To = []string{}
	}
	if s.Notify.Events == nil {
		s.Notify.Events = []string{}
	}
	if s.DownloadProxy.Mode == "" {
		s.DownloadProxy = defaults.DownloadProxy
	}
	fill(&s.DownloadProxy.Listener, defaults.DownloadProxy.Listener)
	fill(&s.DownloadProxy.Protocol, defaults.DownloadProxy.Protocol)
}

// Normalize 补齐用户的空列表和缺省值。
func (u *User) Normalize() {
	if u.Profiles == nil {
		u.Profiles = []Profile{}
	}
	if u.PausedStacks == nil {
		u.PausedStacks = []string{}
	}
	if u.ResetMode == "" {
		u.ResetMode = "none"
	}
	if u.DailyLimitAction == "" {
		u.DailyLimitAction = DailyActionPause
	}
	if u.PauseWindowMode == "" {
		u.PauseWindowMode = PauseWindowInherit
	}
	// 只有自定义模式使用自己的时段，其他模式不保留，避免隐藏的旧值参与校验。
	if u.PauseWindowMode == PauseWindowCustom {
		u.PauseWindows = normalizePauseWindows(u.PauseWindows)
	} else {
		u.PauseWindows = []string{}
	}
}

// Normalize 补齐 stack 的空列表和缺省枚举值。
func (s *Stack) Normalize() {
	if s.DirectSuffix == "" {
		s.DirectSuffix = DefaultDirectSuffix
	}
	if s.DirectUsers == nil {
		s.DirectUsers = []string{}
	}
	if s.Labels == nil {
		s.Labels = []string{}
	}
	if s.Role == "" {
		s.Role = "edge"
	}
	for _, field := range []*string{&s.Xray.LogLevel, &s.Xray.AccessLog, &s.Xray.API, &s.Xray.Stats, &s.Xray.Policy, &s.Mihomo.Mode, &s.Mihomo.LogLevel} {
		if *field == "" {
			*field = Inherit
		}
	}
	if s.Xray.Inbounds == nil {
		s.Xray.Inbounds = []Inbound{}
	}
	for index := range s.Xray.Inbounds {
		inbound := &s.Xray.Inbounds[index]
		if inbound.Users == nil {
			inbound.Users = []UserRef{}
		}
		if inbound.WSHeaders == nil {
			inbound.WSHeaders = map[string]string{}
		}
		if (inbound.Protocol == "socks5" || inbound.Protocol == "http") && inbound.AuthType == "" {
			inbound.AuthType = "noauth"
		}
	}
	s.Mihomo.Socks.Enabled = true
	for _, listener := range []*Listener{&s.Mihomo.Socks, &s.Mihomo.HTTP} {
		if listener.Auth == "" {
			listener.Auth = ListenerAuthInherit
		}
		if listener.Accounts == nil {
			listener.Accounts = []ListenerAccount{}
		}
	}
	if s.Mihomo.Upstreams == nil {
		s.Mihomo.Upstreams = []Upstream{}
	}
	if s.Mihomo.Groups == nil {
		s.Mihomo.Groups = []Group{}
	}
	for index := range s.Mihomo.Groups {
		if s.Mihomo.Groups[index].Proxies == nil {
			s.Mihomo.Groups[index].Proxies = []string{}
		}
	}
	if s.Mihomo.Rules.Profile == "" {
		s.Mihomo.Rules.Profile = "default"
	}
	if s.Mihomo.Rules.Final == "" {
		s.Mihomo.Rules.Final = SelectGroup
	}
	if s.Mihomo.Rules.Extra == nil {
		s.Mihomo.Rules.Extra = []Rule{}
	}
}
