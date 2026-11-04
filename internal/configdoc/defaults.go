package configdoc

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"

	"proxystack-web/internal/domain"
)

// DefaultDirectSuffix 包含前导空格，使直连标记与原节点名分隔。
const DefaultDirectSuffix = " [DRT]"

// DefaultSettings 返回新实例的全局设置。
func DefaultSettings() Settings {
	return Settings{
		Basic: BasicSettings{Timezone: "Asia/Shanghai"},
		PortPools: PortPools{
			XrayInbound:      PortRange{Start: 4300, End: 4399},
			MihomoSocks:      PortRange{Start: 7001, End: 7101},
			MihomoHTTP:       PortRange{Start: 7201, End: 7301},
			XrayAPI:          PortRange{Start: 10001, End: 10999},
			MihomoController: PortRange{Start: 19000, End: 19999},
		},
		Defaults: DefaultsSettings{
			Mihomo: MihomoDefaults{Mode: "Rule", LogLevel: "info"},
			Xray: XrayDefaults{
				AccessLog:   triState(On),
				LogLevel:    "warning",
				APIEnabled:  true,
				APIServices: []string{"HandlerService", "StatsService"},
				Stats:       true,
				Policy: Policy{
					Enabled:               true,
					StatsUserUplink:       true,
					StatsUserDownlink:     true,
					StatsInboundUplink:    true,
					StatsInboundDownlink:  true,
					StatsOutboundUplink:   true,
					StatsOutboundDownlink: true,
				},
			},
		},
		Security: SecuritySettings{RequireAuthForPublicSocksHTTP: true},
		Deps: DepsSettings{
			Xray:          DepSource{Version: "latest", Source: "auto"},
			Mihomo:        DepSource{Version: "latest", Source: "auto"},
			Geo:           DepSource{Version: "latest", Source: "auto"},
			MihomoVariant: "auto",
			WindowStart:   "04:00",
			WindowEnd:     "06:00",
		},
		DownloadProxy: DownloadProxySettings{Mode: ProxyModeDirect, Listener: "socks", Protocol: "socks5", RemoteDNS: true},
		Subscription: SubscriptionSettings{
			SourceName: "main", SurgeManaged: true, Interval: 86400, Strict: true,
			DisabledResponse: DisabledNotFound,
		},
		Traffic: TrafficSettings{
			Enabled: true, Interval: 10, RetentionMinuteDays: 7, RetentionHourDays: 180, RetentionDayDays: 730,
		},
		Notify: NotifySettings{
			Port: 587, Security: SMTPSecuritySTARTTLS, To: []string{},
			Events:      []string{EventDailyLimit, EventQuotaLimit, EventExpired, EventEgressDown, EventEgressRecovered},
			WarnPercent: 80, ExpireDays: 3,
		},
	}
}

// 内置 stack 模板。
const (
	TemplatePair        = "pair"
	TemplateAutoURLTest = "auto-url-test"
	TemplateLoadBalance = "load-balance"
)

// 模板使用的固定值。
const (
	RelayInbound    = "relay"
	DefaultTestURL  = "http://www.gstatic.com/generate_204"
	DefaultInterval = 120
	SelectGroup     = "AllProxy"
)

// NewStackOptions 是新建向导的输入。
type NewStackOptions struct {
	Template string
	Name     string
	Labels   []string
	Members  []string
	// ReservedPorts 是文档之外已占用的端口（例如面板端口）。
	ReservedPorts []int
}

// NewStack 按模板生成一个完整的 stack：端口从端口池分配，凭据随机生成。
// ErrNoUsers 表示没有用户：新建 stack 的入站要引用用户，至少要有一个用户才能创建。
var ErrNoUsers = errors.New("create a user first: a new stack needs at least one user")

func NewStack(doc *Document, settings Settings, opts NewStackOptions) (Stack, error) {
	auto := opts.Template == TemplateAutoURLTest || opts.Template == TemplateLoadBalance
	if !auto && opts.Template != TemplatePair {
		return Stack{}, fmt.Errorf("unknown template: %s", opts.Template)
	}
	if auto && len(opts.Members) == 0 {
		return Stack{}, fmt.Errorf("auto templates require at least one member")
	}
	if len(doc.Users) == 0 {
		return Stack{}, ErrNoUsers
	}
	refs := make([]UserRef, 0, len(doc.Users))
	for _, user := range doc.Users {
		refs = append(refs, UserRef{User: user.Name})
	}
	stack := Stack{
		Name:           opts.Name,
		Enabled:        true,
		DesiredRunning: true,
		Role:           "edge",
		Labels:         append([]string{}, opts.Labels...),
		DirectUsers:    []string{},
		DirectSuffix:   DefaultDirectSuffix,
		Xray: Xray{
			AccessLog: Inherit,
			Enabled:   true,
			LogLevel:  Inherit,
			API:       Inherit,
			Stats:     Inherit,
			Policy:    Inherit,
			Outbound:  Outbound{Type: "mihomo"},
		},
		Mihomo: Mihomo{
			Enabled:          true,
			Mode:             Inherit,
			LogLevel:         Inherit,
			ControllerSecret: RandomSecret(),
			Socks:            Listener{Enabled: true, Name: "local", Listen: loopback, Auth: ListenerAuthInherit, Accounts: []ListenerAccount{}},
			HTTP:             Listener{Enabled: true, Name: "local-http", Listen: loopback, Auth: ListenerAuthInherit, Accounts: []ListenerAccount{}},
			Rules:            Rules{Profile: "default", Final: SelectGroup, Extra: []Rule{}},
		},
	}
	vmess := Inbound{
		Name: "vmess", Protocol: "vmess", Listen: "0.0.0.0", Sub: true, UDP: true, Network: "raw",
		Remark: opts.Name + " vmess", Users: refs, WSHeaders: map[string]string{},
	}
	if auto {
		stack.Role = "auto"
		relay := Inbound{
			Name: RelayInbound, Protocol: "socks5", Listen: "0.0.0.0", Sub: true, UDP: true,
			AuthType: "password", AuthUsername: opts.Name, AuthPassword: RandomPassword(),
			Remark: opts.Name + " relay socks", Users: append([]UserRef{}, refs...), WSHeaders: map[string]string{},
		}
		stack.Xray.Inbounds = []Inbound{relay, vmess}
		groupName, groupType := "AutoProxy", "url-test"
		strategy := ""
		if opts.Template == TemplateLoadBalance {
			groupName, groupType, strategy = "BalanceProxy", "load-balance", "consistent-hashing"
		}
		// 聚合 stack 的流量全部经成员 stack 的 relay 出去，代理组不放 DIRECT，避免从本机直接出网。
		stack.Mihomo.Groups = []Group{
			{Name: groupName, Type: groupType, Proxies: []string{}, URL: DefaultTestURL, Interval: DefaultInterval, Strategy: strategy},
			{Name: SelectGroup, Type: "select", Proxies: []string{groupName}},
		}
		stack.Mihomo.Upstreams = []Upstream{}
		for _, member := range opts.Members {
			AddMember(&stack, member)
		}
	} else {
		relay := Inbound{
			Name: RelayInbound, Protocol: "socks5", Listen: loopback, Sub: false, UDP: true,
			AuthType: "noauth", Users: []UserRef{}, WSHeaders: map[string]string{},
		}
		stack.Xray.Inbounds = []Inbound{relay, vmess}
		stack.Mihomo.Upstreams = []Upstream{}
		stack.Mihomo.Groups = []Group{{Name: SelectGroup, Type: "select", Proxies: []string{BuiltinDirect}}}
	}
	if err := AllocateStackPorts(doc, settings, &stack, opts.ReservedPorts); err != nil {
		return Stack{}, err
	}
	return stack, nil
}

// UsedPorts 收集文档中全部 stack 已占用的端口，skip 指向的 stack 不计入。
func UsedPorts(doc *Document, skip *Stack, reserved []int) map[int]bool {
	used := map[int]bool{}
	for _, port := range reserved {
		used[port] = true
	}
	for index := range doc.Stacks {
		stack := &doc.Stacks[index]
		if stack == skip {
			continue
		}
		for port := range StackPorts(stack) {
			used[port] = true
		}
	}
	return used
}

// StackPorts 返回 stack 占用的全部端口。
func StackPorts(stack *Stack) map[int]bool {
	ports := map[int]bool{}
	for _, inbound := range stack.Xray.Inbounds {
		ports[inbound.Port] = true
	}
	ports[stack.Xray.APIPort] = true
	ports[stack.Mihomo.ControllerPort] = true
	ports[stack.Mihomo.Socks.Port] = true
	if stack.Mihomo.HTTP.Enabled {
		ports[stack.Mihomo.HTTP.Port] = true
	}
	delete(ports, 0)
	return ports
}

// AllocateStackPorts 为 stack 的全部端口重新从端口池分配。
func AllocateStackPorts(doc *Document, settings Settings, stack *Stack, reserved []int) error {
	used := UsedPorts(doc, stack, reserved)
	take := func(pool PortRange, label string) (int, error) {
		ports, err := domain.PortRange(pool).Allocate(used, 1)
		if err != nil {
			return 0, fmt.Errorf("%s port pool is exhausted", label)
		}
		used[ports[0]] = true
		return ports[0], nil
	}
	var err error
	for index := range stack.Xray.Inbounds {
		if stack.Xray.Inbounds[index].Port, err = take(settings.PortPools.XrayInbound, "xray inbound"); err != nil {
			return err
		}
	}
	if stack.Xray.APIPort, err = take(settings.PortPools.XrayAPI, "xray api"); err != nil {
		return err
	}
	if stack.Mihomo.Socks.Port, err = take(settings.PortPools.MihomoSocks, "mihomo socks"); err != nil {
		return err
	}
	if stack.Mihomo.HTTP.Enabled {
		if stack.Mihomo.HTTP.Port, err = take(settings.PortPools.MihomoHTTP, "mihomo http"); err != nil {
			return err
		}
	}
	if stack.Mihomo.ControllerPort, err = take(settings.PortPools.MihomoController, "mihomo controller"); err != nil {
		return err
	}
	return nil
}

// AllocatePort 从指定端口池分配一个未占用端口。
func AllocatePort(doc *Document, pool PortRange, reserved []int) (int, error) {
	ports, err := domain.PortRange(pool).Allocate(UsedPorts(doc, nil, reserved), 1)
	if err != nil {
		return 0, fmt.Errorf("port pool is exhausted")
	}
	return ports[0], nil
}

// NewUser 生成一个凭据齐全的新用户。
func NewUser(name string) User {
	return User{
		Name:       name,
		Enabled:    true,
		UUID:       RandomUUID(),
		SSPassword: RandomPassword(),
		SubToken:   RandomToken(),
		ResetMode:  "none",
		Profiles:   []Profile{},
	}
}

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// RandomPassword 生成 24 位随机密码。
func RandomPassword() string {
	return randomString(24)
}

// RandomSecret 生成 32 位随机 secret。
func RandomSecret() string {
	return randomString(32)
}

// RandomToken 生成订阅 token（32 字节 base64url）。
func RandomToken() string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(32))
}

// RandomUUID 生成 UUID v4。
func RandomUUID() string {
	b := randomBytes(16)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RandomShadowsocksKey 按 method 生成密钥：2022 系列为对应长度的 base64 PSK，其他为随机密码。
func RandomShadowsocksKey(method string) string {
	switch method {
	case "2022-blake3-aes-128-gcm":
		return base64.StdEncoding.EncodeToString(randomBytes(16))
	case "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
		return base64.StdEncoding.EncodeToString(randomBytes(32))
	default:
		return RandomPassword()
	}
}

func randomString(length int) string {
	out := make([]byte, length)
	limit := big.NewInt(int64(len(passwordAlphabet)))
	for index := range out {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			panic(err)
		}
		out[index] = passwordAlphabet[n.Int64()]
	}
	return string(out)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
