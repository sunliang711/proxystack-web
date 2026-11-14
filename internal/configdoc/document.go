// Package configdoc 定义配置的规范文档：实体之间按名字引用，可序列化为 YAML/JSON。
//
// 同一份结构用于数据库组装后的内存模型、表单 API 的请求体、配置修订快照和导入导出文件。
// 实体带有数据库 ID（导出时清空），引用一律写名字，改名时由本包同步改写引用方。
package configdoc

// 文件头常量。
const (
	Schema  = "proxystack-web.config"
	Version = 1
)

// 三态取值：继承全局默认 / 开 / 关。
const (
	Inherit = "inherit"
	On      = "on"
	Off     = "off"
)

// 凭据处理方式。
const (
	SecretsPlain     = "plain"
	SecretsEncrypted = "encrypted"
	SecretsOmitted   = "omitted"
)

// Document 是完整或部分配置。
type Document struct {
	Schema     string      `json:"schema" yaml:"schema"`
	Version    int         `json:"version" yaml:"version"`
	ExportedAt string      `json:"exported_at,omitempty" yaml:"exported_at,omitempty"`
	Scope      string      `json:"scope,omitempty" yaml:"scope,omitempty"`
	Secrets    string      `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Encryption *Encryption `json:"encryption,omitempty" yaml:"encryption,omitempty"`
	Settings   *Settings   `json:"settings,omitempty" yaml:"settings,omitempty"`
	Users      []User      `json:"users,omitempty" yaml:"users,omitempty"`
	Stacks     []Stack     `json:"stacks,omitempty" yaml:"stacks,omitempty"`
	// ManualNodes 是手工添加的订阅节点。
	ManualNodes []ManualNode `json:"manual_nodes,omitempty" yaml:"manual_nodes,omitempty"`
	// RemoteSources 是从其他实例拉取节点的远端节点源。
	RemoteSources []RemoteSource `json:"remote_sources,omitempty" yaml:"remote_sources,omitempty"`
	// Templates 是自定义订阅模板，nil 表示文档不涉及模板。
	Templates *Templates `json:"templates,omitempty" yaml:"templates,omitempty"`
}

// 手工节点模式。
const (
	NodeStandard = "standard"
	NodeDirect   = "direct"
)

// ManualNode 是手工添加到某个用户订阅中的节点。标准节点按协议填写字段；
// 原生节点（direct）按 mihomo proxy 字段原样输出到 Clash/Premium Clash。
type ManualNode struct {
	ID              int64             `json:"id,omitempty" yaml:"-"`
	User            string            `json:"user" yaml:"user"`
	Mode            string            `json:"mode" yaml:"mode" enum:"standard,direct"`
	Protocol        string            `json:"protocol" yaml:"protocol,omitempty" enum:",vmess,shadowsocks,socks5,http"`
	Server          string            `json:"server" yaml:"server"`
	Port            int               `json:"port" yaml:"port" minimum:"0" maximum:"65535"`
	Tag             string            `json:"tag" yaml:"tag,omitempty"`
	Remark          string            `json:"remark" yaml:"remark"`
	Region          string            `json:"region" yaml:"region,omitempty"`
	UDP             bool              `json:"udp" yaml:"udp,omitempty"`
	UUID            string            `json:"uuid" yaml:"uuid,omitempty"`
	Network         string            `json:"network" yaml:"network,omitempty" enum:",raw,ws,grpc"`
	WSPath          string            `json:"ws_path" yaml:"ws_path,omitempty"`
	WSHeaders       map[string]string `json:"ws_headers" yaml:"ws_headers,omitempty"`
	GRPCServiceName string            `json:"grpc_service_name" yaml:"grpc_service_name,omitempty"`
	Method          string            `json:"method" yaml:"method,omitempty"`
	Password        string            `json:"password" yaml:"password,omitempty"`
	AuthType        string            `json:"auth_type" yaml:"auth_type,omitempty" enum:",noauth,password"`
	AuthUsername    string            `json:"auth_username" yaml:"auth_username,omitempty"`
	AuthPassword    string            `json:"auth_password" yaml:"auth_password,omitempty"`
	Config          RawMapping        `json:"config" yaml:"config,omitempty" doc:"mode=direct 时的 mihomo proxy 字段"`
}

// RemoteSource 是另一个实例的节点导出接口。
type RemoteSource struct {
	ID      int64  `json:"id,omitempty" yaml:"-"`
	Name    string `json:"name" yaml:"name"`
	URL     string `json:"url" yaml:"url"`
	Token   string `json:"token" yaml:"token"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
}

// Templates 是三种订阅格式的自定义模板，空字符串表示使用内置模板。
type Templates struct {
	Clash   string `json:"clash" yaml:"clash,omitempty"`
	Premium string `json:"premium" yaml:"premium,omitempty"`
	Surge   string `json:"surge" yaml:"surge,omitempty"`
}

// Encryption 描述口令加密参数。
type Encryption struct {
	KDF    string `json:"kdf" yaml:"kdf"`
	Salt   string `json:"salt" yaml:"salt"`
	Time   uint32 `json:"time" yaml:"time"`
	Memory uint32 `json:"memory" yaml:"memory"`
	Check  string `json:"check" yaml:"check"`
}

// Settings 是参与配置生成的全局设置。
type Settings struct {
	Basic         BasicSettings         `json:"basic" yaml:"basic"`
	PortPools     PortPools             `json:"port_pools" yaml:"port_pools"`
	Defaults      DefaultsSettings      `json:"defaults" yaml:"defaults"`
	Security      SecuritySettings      `json:"security" yaml:"security"`
	Deps          DepsSettings          `json:"deps" yaml:"deps"`
	DownloadProxy DownloadProxySettings `json:"download_proxy" yaml:"download_proxy"`
	Subscription  SubscriptionSettings  `json:"subscription" yaml:"subscription"`
	NodeExport    NodeExportSettings    `json:"node_export" yaml:"node_export"`
	Traffic       TrafficSettings       `json:"traffic" yaml:"traffic"`
	Notify        NotifySettings        `json:"notify" yaml:"notify"`
}

// 用户被停用或已到期时订阅的返回方式。
const (
	DisabledNotFound = "not_found"
	DisabledEmpty    = "empty"
)

// SubscriptionSettings 是订阅监听器的设置。
type SubscriptionSettings struct {
	SourceName       string `json:"source_name" yaml:"source_name" doc:"本机在订阅端的来源名，订阅端按它找到验签用的公钥"`
	PublicBaseURL    string `json:"public_base_url" yaml:"public_base_url,omitempty" doc:"订阅端的对外地址，用于订阅链接和 Surge managed 首行；为空时订阅端用请求地址"`
	DisplayTemplate  string `json:"display_template" yaml:"display_template,omitempty" doc:"用户档案和入站均未配置展示名模板时使用的全局模板"`
	SurgeManaged     bool   `json:"surge_managed" yaml:"surge_managed"`
	Interval         int    `json:"interval" yaml:"interval" minimum:"0" doc:"Surge managed config 的更新间隔（秒）"`
	Strict           bool   `json:"strict" yaml:"strict"`
	DisabledResponse string `json:"disabled_response" yaml:"disabled_response" enum:"not_found,empty"`
	// ManualPublish 为 false（默认）时，订阅内容变化后自动签发并发布到已部署且测试通过的在线订阅端。
	ManualPublish bool `json:"manual_publish" yaml:"manual_publish,omitempty" doc:"为 true 时关闭自动发布，订阅包只在「发布订阅包」页面手动签发"`
}

// 达到每日阈值后的动作。
const (
	DailyActionPause  = "pause"
	DailyActionNotify = "notify"
)

// TrafficSettings 是流量监控设置。
type TrafficSettings struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Interval 是采集间隔（秒）。
	Interval int `json:"interval" yaml:"interval" enum:"5,10,30"`
	// 各级明细的保留天数，0 表示永久保留。
	RetentionMinuteDays int `json:"retention_minute_days" yaml:"retention_minute_days" minimum:"0"`
	RetentionHourDays   int `json:"retention_hour_days" yaml:"retention_hour_days" minimum:"0"`
	RetentionDayDays    int `json:"retention_day_days" yaml:"retention_day_days" minimum:"0"`
	RetentionMonthDays  int `json:"retention_month_days" yaml:"retention_month_days" minimum:"0"`
	// DisconnectOnPause 为真时自动暂停后重启受影响的 xray，切断已建立的连接。
	DisconnectOnPause bool `json:"disconnect_on_pause" yaml:"disconnect_on_pause"`
	// PauseWindows 是全部用户默认的每日停服时段，按基本设置的时区计算，时段内自动暂停用户。
	PauseWindows []string `json:"pause_windows" yaml:"pause_windows,omitempty" required:"false" doc:"每日停服时段，HH:MM-HH:MM，可跨零点；用户可单独设置"`
	// AccessStats 为真时读取 xray 访问日志，按用户统计访问的主域名。
	AccessStats bool `json:"access_stats" yaml:"access_stats,omitempty" required:"false" doc:"按用户统计访问的主域名（读取 xray 访问日志）"`
	// AccessRetentionDays 是访问域名统计的保留天数。
	AccessRetentionDays int `json:"access_retention_days" yaml:"access_retention_days,omitempty" required:"false" minimum:"0" maximum:"3650" doc:"访问域名统计保留天数，缺省 90"`
	// AccessGroups 是访问域名统计中手动补充的归类，每条为「域名=服务名」，优先于 geosite 清单。
	AccessGroups []string `json:"access_groups" yaml:"access_groups,omitempty" required:"false" doc:"访问域名的手动归类，每条 域名=服务名，域名匹配它本身及全部子域名"`
	// BlockedDomains 是对全部连接生效的域名黑名单，由 xray 路由拦截。
	BlockedDomains []string `json:"blocked_domains" yaml:"blocked_domains,omitempty" required:"false" doc:"全局域名黑名单：example.com 拦截该域名及全部子域名，full:www.example.com 只拦截该域名"`
}

// 邮件加密方式。
const (
	SMTPSecurityNone     = "none"
	SMTPSecuritySTARTTLS = "starttls"
	SMTPSecurityTLS      = "tls"
)

// 通知事件。
const (
	EventDailyLimit      = "daily_limit"
	EventDailyWarning    = "daily_warning"
	EventQuotaLimit      = "quota_limit"
	EventQuotaWarning    = "quota_warning"
	EventExpired         = "expired"
	EventExpiring        = "expiring"
	EventEgressDown      = "egress_down"
	EventEgressRecovered = "egress_recovered"
	EventHealthAlert     = "health_alert"
	EventHealthRecovered = "health_recovered"
)

// NotifySettings 是邮件通知设置。
type NotifySettings struct {
	Enabled  bool     `json:"enabled" yaml:"enabled"`
	Host     string   `json:"host" yaml:"host,omitempty"`
	Port     int      `json:"port" yaml:"port,omitempty" minimum:"0" maximum:"65535"`
	Security string   `json:"security" yaml:"security,omitempty" enum:"none,starttls,tls"`
	Username string   `json:"username" yaml:"username,omitempty"`
	Password string   `json:"password" yaml:"password,omitempty"`
	From     string   `json:"from" yaml:"from,omitempty"`
	To       []string `json:"to" yaml:"to,omitempty"`
	// Events 是要发送邮件的事件。
	Events []string `json:"events" yaml:"events,omitempty"`
	// WarnPercent 是用量预警比例，ExpireDays 是到期提醒提前天数。
	WarnPercent int `json:"warn_percent" yaml:"warn_percent" minimum:"1" maximum:"100"`
	ExpireDays  int `json:"expire_days" yaml:"expire_days" minimum:"1"`
	// DBWarnMB 是数据库大小的告警阈值（MB），超过后健康检查给出警告。
	DBWarnMB int `json:"db_warn_mb" yaml:"db_warn_mb" minimum:"1" maximum:"1048576"`
}

// NodeExportSettings 是只读节点导出接口：其他实例凭 token 拉取本机节点。
type NodeExportSettings struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Token   string `json:"token" yaml:"token"`
}

// DepSource 是一个组件的安装来源。
type DepSource struct {
	Version       string `json:"version" yaml:"version" doc:"latest 或具体版本号"`
	Source        string `json:"source" yaml:"source" enum:"auto,github,custom"`
	URL           string `json:"url" yaml:"url,omitempty" doc:"source=custom 时的下载地址"`
	SHA256        string `json:"sha256" yaml:"sha256,omitempty"`
	ArchiveMember string `json:"archive_member" yaml:"archive_member,omitempty"`
}

// DepsSettings 是 xray、mihomo、geo 数据的安装与升级设置。
type DepsSettings struct {
	Xray              DepSource `json:"xray" yaml:"xray"`
	Mihomo            DepSource `json:"mihomo" yaml:"mihomo"`
	Geo               DepSource `json:"geo" yaml:"geo"`
	MihomoVariant     string    `json:"mihomo_variant" yaml:"mihomo_variant" enum:"auto,standard,compatible"`
	AutoUpgradeXray   bool      `json:"auto_upgrade_xray" yaml:"auto_upgrade_xray"`
	AutoUpgradeMihomo bool      `json:"auto_upgrade_mihomo" yaml:"auto_upgrade_mihomo"`
	WindowStart       string    `json:"window_start" yaml:"window_start" doc:"维护时间窗开始，HH:MM"`
	WindowEnd         string    `json:"window_end" yaml:"window_end" doc:"维护时间窗结束，HH:MM"`
}

// 下载代理模式。
const (
	ProxyModeDirect = "direct"
	ProxyModeStack  = "stack"
	ProxyModeCustom = "custom"
)

// DownloadProxySettings 是依赖管理和自更新的全部对外请求使用的代理。
type DownloadProxySettings struct {
	Mode      string `json:"mode" yaml:"mode" enum:"direct,stack,custom"`
	Stack     string `json:"stack" yaml:"stack,omitempty" doc:"mode=stack 时经该 stack 的 mihomo 监听出站"`
	Listener  string `json:"listener" yaml:"listener,omitempty" enum:"socks,http"`
	Protocol  string `json:"protocol" yaml:"protocol,omitempty" enum:"http,socks5"`
	Host      string `json:"host" yaml:"host,omitempty"`
	Port      int    `json:"port" yaml:"port,omitempty" minimum:"0" maximum:"65535"`
	Username  string `json:"username" yaml:"username,omitempty"`
	Password  string `json:"password" yaml:"password,omitempty"`
	RemoteDNS bool   `json:"remote_dns" yaml:"remote_dns"`
}

// BasicSettings 是基本设置。
type BasicSettings struct {
	ExternalHost string `json:"external_host" yaml:"external_host" doc:"订阅节点默认 server，域名或 IP"`
	Timezone     string `json:"timezone" yaml:"timezone" doc:"流量统计周期边界使用的时区"`
	PanelURL     string `json:"panel_url" yaml:"panel_url,omitempty" doc:"面板经 nginx 访问的地址，用于通知邮件中的链接"`
}

// PortRange 是闭区间端口池。
type PortRange struct {
	Start int `json:"start" yaml:"start" minimum:"1" maximum:"65535"`
	End   int `json:"end" yaml:"end" minimum:"1" maximum:"65535"`
}

// PortPools 是各类端口的自动分配区间。
type PortPools struct {
	XrayInbound      PortRange `json:"xray_inbound" yaml:"xray_inbound"`
	MihomoSocks      PortRange `json:"mihomo_socks" yaml:"mihomo_socks"`
	MihomoHTTP       PortRange `json:"mihomo_http" yaml:"mihomo_http"`
	XrayAPI          PortRange `json:"xray_api" yaml:"xray_api"`
	MihomoController PortRange `json:"mihomo_controller" yaml:"mihomo_controller"`
}

// DefaultsSettings 是 stack 未覆盖时使用的默认值。
type DefaultsSettings struct {
	Mihomo MihomoDefaults `json:"mihomo" yaml:"mihomo"`
	Xray   XrayDefaults   `json:"xray" yaml:"xray"`
}

// MihomoDefaults 是 mihomo 默认值。
type MihomoDefaults struct {
	Mode     string `json:"mode" yaml:"mode" enum:"Rule,Global,Direct"`
	LogLevel string `json:"loglevel" yaml:"loglevel" enum:"debug,info,warning,error,silent"`
}

// XrayDefaults 是 xray 默认值。
type XrayDefaults struct {
	AccessLog   *bool    `json:"access_log,omitempty" yaml:"access_log,omitempty" required:"false" doc:"记录客户端访问日志，缺省开启"`
	LogLevel    string   `json:"loglevel" yaml:"loglevel" enum:"debug,info,warning,error,none"`
	APIEnabled  bool     `json:"api_enabled" yaml:"api_enabled"`
	APIServices []string `json:"api_services" yaml:"api_services" enum:"HandlerService,StatsService"`
	Stats       bool     `json:"stats" yaml:"stats"`
	Policy      Policy   `json:"policy" yaml:"policy"`
}

// Policy 是 xray policy 默认值：level 0 的用户统计和 system 的入站/出站统计。
type Policy struct {
	Enabled               bool `json:"enabled" yaml:"enabled"`
	StatsUserUplink       bool `json:"stats_user_uplink" yaml:"stats_user_uplink"`
	StatsUserDownlink     bool `json:"stats_user_downlink" yaml:"stats_user_downlink"`
	StatsInboundUplink    bool `json:"stats_inbound_uplink" yaml:"stats_inbound_uplink"`
	StatsInboundDownlink  bool `json:"stats_inbound_downlink" yaml:"stats_inbound_downlink"`
	StatsOutboundUplink   bool `json:"stats_outbound_uplink" yaml:"stats_outbound_uplink"`
	StatsOutboundDownlink bool `json:"stats_outbound_downlink" yaml:"stats_outbound_downlink"`
}

// SecuritySettings 是安全校验开关。
type SecuritySettings struct {
	RequireAuthForPublicSocksHTTP bool `json:"require_auth_for_public_socks_http" yaml:"require_auth_for_public_socks_http"`
	AllowNoAuthPublic             bool `json:"allow_noauth_public" yaml:"allow_noauth_public"`
}

// User 是订阅用户，自身即 default 档案。
type User struct {
	ID              int64  `json:"id,omitempty" yaml:"-"`
	Name            string `json:"name" yaml:"name"`
	Enabled         bool   `json:"enabled" yaml:"enabled"`
	UUID            string `json:"uuid" yaml:"uuid"`
	SSPassword      string `json:"ss_password" yaml:"ss_password"`
	Remark          string `json:"remark" yaml:"remark,omitempty"`
	DisplayTemplate string `json:"display_template" yaml:"display_template,omitempty"`
	Tag             string `json:"tag" yaml:"tag,omitempty"`
	SubToken        string `json:"sub_token" yaml:"sub_token"`
	QuotaBytes      int64  `json:"quota_bytes" yaml:"quota_bytes" minimum:"0"`
	// DailyLimitBytes 是每日用量阈值（上下行合计，按设置时区的自然日统计），0 表示不限。
	DailyLimitBytes  int64  `json:"daily_limit_bytes" yaml:"daily_limit_bytes,omitempty" minimum:"0" required:"false"`
	DailyLimitAction string `json:"daily_limit_action" yaml:"daily_limit_action,omitempty" enum:"pause,notify" required:"false" doc:"达到每日阈值后：pause 暂停并通知，notify 只通知"`
	ResetMode        string `json:"reset_mode" yaml:"reset_mode" enum:"none,monthly"`
	ResetDay         int    `json:"reset_day" yaml:"reset_day" minimum:"0" maximum:"28"`
	ExpireAt         string `json:"expire_at" yaml:"expire_at,omitempty" doc:"RFC 3339 时间，空表示不过期"`
	// PauseWindowMode 决定每日停服时段：inherit 跟随全局设置，custom 使用 PauseWindows，off 不限制。
	PauseWindowMode string   `json:"pause_window_mode" yaml:"pause_window_mode,omitempty" enum:",inherit,custom,off" required:"false" doc:"每日停服时段：inherit 跟随全局，custom 使用自己的时段，off 不限制"`
	PauseWindows    []string `json:"pause_windows" yaml:"pause_windows,omitempty" required:"false" doc:"pause_window_mode=custom 时的停服时段，HH:MM-HH:MM，可跨零点"`
	// BlockedDomains 是只对该用户生效的域名黑名单，与全局名单叠加。
	BlockedDomains []string  `json:"blocked_domains" yaml:"blocked_domains,omitempty" required:"false" doc:"用户域名黑名单，与全局名单叠加；写法同全局名单"`
	Note           string    `json:"note" yaml:"note,omitempty"`
	Profiles       []Profile `json:"profiles" yaml:"profiles,omitempty"`
	// PausedStacks 是手动暂停该用户的 stack；Enabled 为 false 时在全部 stack 上暂停。
	PausedStacks []string `json:"paused_stacks" yaml:"paused_stacks,omitempty" required:"false"`
}

// Profile 是用户的额外凭据档案。
type Profile struct {
	ID              int64  `json:"id,omitempty" yaml:"-"`
	Name            string `json:"name" yaml:"name"`
	UUID            string `json:"uuid" yaml:"uuid"`
	SSPassword      string `json:"ss_password" yaml:"ss_password"`
	Remark          string `json:"remark" yaml:"remark,omitempty"`
	DisplayTemplate string `json:"display_template" yaml:"display_template,omitempty"`
	Tag             string `json:"tag" yaml:"tag,omitempty"`
}

// Stack 是一组 xray + mihomo 进程的配置。
type Stack struct {
	ID             int64  `json:"id,omitempty" yaml:"-"`
	Name           string `json:"name" yaml:"name"`
	Enabled        bool   `json:"enabled" yaml:"enabled"`
	DesiredRunning bool   `json:"desired_running" yaml:"desired_running"`
	// DailyLimitBytes 是 Stack 每日用量阈值（上下行合计，按设置时区的自然日统计），0 表示不限。
	DailyLimitBytes int64    `json:"daily_limit_bytes" yaml:"daily_limit_bytes,omitempty" minimum:"0" required:"false"`
	Role            string   `json:"role" yaml:"role" enum:"edge,auto"`
	Labels          []string `json:"labels" yaml:"labels,omitempty"`
	Xray            Xray     `json:"xray" yaml:"xray"`
	Mihomo          Mihomo   `json:"mihomo" yaml:"mihomo"`
	// DirectUsers 可获取外部上游直连节点，与入站用户列表独立。
	DirectUsers []string `json:"direct_users" yaml:"direct_users,omitempty" required:"false"`
	// DirectSuffix 原样追加到直连节点名，空值使用默认后缀。
	DirectSuffix string `json:"direct_suffix" yaml:"direct_suffix,omitempty" required:"false" doc:"直连节点后缀，留空使用默认值（前导空格加 [DRT]）"`
	// DirectOverrides 为直连用户在单个外部上游上覆盖节点字段，未列出的组合沿用上游配置。
	DirectOverrides []DirectOverride `json:"direct_overrides" yaml:"direct_overrides,omitempty" required:"false"`
}

// DirectOverride 是某个直连用户在某个外部上游上覆盖的 mihomo 节点字段。
// 顶层字段整体替换上游中的同名字段，嵌套映射不逐层合并。
type DirectOverride struct {
	User     string     `json:"user" yaml:"user"`
	Upstream string     `json:"upstream" yaml:"upstream"`
	Config   RawMapping `json:"config" yaml:"config" doc:"覆盖的 mihomo proxy 字段，不能包含 type、name 及服务器专用字段"`
}

// Xray 是 stack 的 xray 部分。
type Xray struct {
	AccessLog string    `json:"access_log" yaml:"access_log" enum:"inherit,on,off" required:"false" doc:"记录客户端访问日志，缺省继承全局"`
	Enabled   bool      `json:"enabled" yaml:"enabled"`
	LogLevel  string    `json:"loglevel" yaml:"loglevel" enum:"inherit,debug,info,warning,error,none"`
	API       string    `json:"api" yaml:"api" enum:"inherit,on,off"`
	Stats     string    `json:"stats" yaml:"stats" enum:"inherit,on,off"`
	Policy    string    `json:"policy" yaml:"policy" enum:"inherit,on,off"`
	APIPort   int       `json:"api_port" yaml:"api_port" minimum:"1" maximum:"65535"`
	Outbound  Outbound  `json:"outbound" yaml:"outbound"`
	Inbounds  []Inbound `json:"inbounds" yaml:"inbounds"`
}

// Outbound 是 xray 唯一的出站。
type Outbound struct {
	Type          string `json:"type" yaml:"type" enum:"mihomo,socks5,http,direct" doc:"mihomo 表示转发到本 stack 的 mihomo socks 监听"`
	Server        string `json:"server" yaml:"server,omitempty"`
	Port          int    `json:"port" yaml:"port,omitempty" minimum:"0" maximum:"65535"`
	Username      string `json:"username" yaml:"username,omitempty"`
	Password      string `json:"password" yaml:"password,omitempty"`
	PrivateDirect bool   `json:"private_direct" yaml:"private_direct,omitempty"`
}

// Inbound 是 xray 入站。
type Inbound struct {
	ID              int64             `json:"id,omitempty" yaml:"-"`
	Name            string            `json:"name" yaml:"name"`
	Protocol        string            `json:"protocol" yaml:"protocol" enum:"vmess,shadowsocks,socks5,http"`
	Listen          string            `json:"listen" yaml:"listen"`
	Port            int               `json:"port" yaml:"port" minimum:"1" maximum:"65535"`
	Sub             bool              `json:"sub" yaml:"sub"`
	UDP             bool              `json:"udp" yaml:"udp"`
	Network         string            `json:"network" yaml:"network,omitempty" enum:",raw,ws,grpc"`
	WSPath          string            `json:"ws_path" yaml:"ws_path,omitempty"`
	WSHeaders       map[string]string `json:"ws_headers" yaml:"ws_headers,omitempty"`
	GRPCServiceName string            `json:"grpc_service_name" yaml:"grpc_service_name,omitempty"`
	Method          string            `json:"method" yaml:"method,omitempty"`
	Password        string            `json:"password" yaml:"password,omitempty"`
	AuthType        string            `json:"auth_type" yaml:"auth_type,omitempty" enum:",noauth,password"`
	AuthUsername    string            `json:"auth_username" yaml:"auth_username,omitempty"`
	AuthPassword    string            `json:"auth_password" yaml:"auth_password,omitempty"`
	Users           []UserRef         `json:"users" yaml:"users,omitempty"`
	Server          string            `json:"server" yaml:"server,omitempty"`
	Remark          string            `json:"remark" yaml:"remark,omitempty"`
	DisplayTemplate string            `json:"display_template" yaml:"display_template,omitempty"`
	Region          string            `json:"region" yaml:"region,omitempty"`
	Tag             string            `json:"tag" yaml:"tag,omitempty"`
}

// UserRef 是入站对用户档案的引用。
type UserRef struct {
	User            string `json:"user" yaml:"user"`
	Profile         string `json:"profile" yaml:"profile,omitempty" doc:"空表示 default 档案"`
	Remark          string `json:"remark" yaml:"remark,omitempty"`
	DisplayTemplate string `json:"display_template" yaml:"display_template,omitempty"`
	Tag             string `json:"tag" yaml:"tag,omitempty"`
}

// Mihomo 是 stack 的 mihomo 部分。
type Mihomo struct {
	Enabled          bool       `json:"enabled" yaml:"enabled"`
	Mode             string     `json:"mode" yaml:"mode" enum:"inherit,Rule,Global,Direct"`
	LogLevel         string     `json:"loglevel" yaml:"loglevel" enum:"inherit,debug,info,warning,error,silent"`
	ControllerPort   int        `json:"controller_port" yaml:"controller_port" minimum:"1" maximum:"65535"`
	ControllerSecret string     `json:"controller_secret" yaml:"controller_secret"`
	Socks            Listener   `json:"socks" yaml:"socks"`
	HTTP             Listener   `json:"http" yaml:"http"`
	Upstreams        []Upstream `json:"upstreams" yaml:"upstreams"`
	Groups           []Group    `json:"groups" yaml:"groups"`
	Rules            Rules      `json:"rules" yaml:"rules"`
}

// 监听认证方式。
const (
	ListenerAuthInherit  = "inherit"
	ListenerAuthNone     = "none"
	ListenerAuthAccounts = "accounts"
)

// Listener 是 mihomo 本机监听。socks 监听总是启用，http 监听由 Enabled 控制。
type Listener struct {
	Enabled  bool              `json:"enabled" yaml:"enabled"`
	Name     string            `json:"name" yaml:"name"`
	Listen   string            `json:"listen" yaml:"listen"`
	Port     int               `json:"port" yaml:"port" minimum:"0" maximum:"65535"`
	Auth     string            `json:"auth" yaml:"auth" enum:"inherit,none,accounts"`
	Accounts []ListenerAccount `json:"accounts" yaml:"accounts,omitempty"`
}

// ListenerAccount 是 mihomo 监听的认证账号。
type ListenerAccount struct {
	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`
}

// Upstream 是 mihomo 上游代理。
type Upstream struct {
	ID      int64      `json:"id,omitempty" yaml:"-"`
	Name    string     `json:"name" yaml:"name"`
	Type    string     `json:"type" yaml:"type" enum:"raw,xray-socks5"`
	Stack   string     `json:"stack" yaml:"stack,omitempty" doc:"type=xray-socks5 时引用的 stack"`
	Inbound string     `json:"inbound" yaml:"inbound,omitempty" doc:"type=xray-socks5 时引用的 socks5 入站"`
	Config  RawMapping `json:"config" yaml:"config,omitempty" doc:"type=raw 时的 mihomo 节点字段"`
}

// Group 是 mihomo 代理组。
type Group struct {
	ID       int64    `json:"id,omitempty" yaml:"-"`
	Name     string   `json:"name" yaml:"name"`
	Type     string   `json:"type" yaml:"type" enum:"select,url-test,load-balance,fallback"`
	Proxies  []string `json:"proxies" yaml:"proxies"`
	URL      string   `json:"url" yaml:"url,omitempty"`
	Interval int      `json:"interval" yaml:"interval,omitempty" minimum:"0"`
	Strategy string   `json:"strategy" yaml:"strategy,omitempty" enum:",consistent-hashing,round-robin,sticky-sessions"`
}

// Rules 是 mihomo 规则设置。
type Rules struct {
	Profile string `json:"profile" yaml:"profile" enum:"default"`
	Final   string `json:"final" yaml:"final"`
	Extra   []Rule `json:"extra" yaml:"extra,omitempty"`
}

// Rule 是一条附加规则。
type Rule struct {
	Type      string `json:"type" yaml:"type"`
	Value     string `json:"value" yaml:"value"`
	Target    string `json:"target" yaml:"target"`
	NoResolve bool   `json:"no_resolve" yaml:"no_resolve,omitempty"`
}

// 内置策略名。
const (
	BuiltinDirect = "DIRECT"
	BuiltinReject = "REJECT"
)

// UserByName 返回同名用户的下标，不存在时返回 -1。
func (d *Document) UserByName(name string) int {
	for index := range d.Users {
		if d.Users[index].Name == name {
			return index
		}
	}
	return -1
}

// StackByName 返回同名 stack 的下标，不存在时返回 -1。
func (d *Document) StackByName(name string) int {
	for index := range d.Stacks {
		if d.Stacks[index].Name == name {
			return index
		}
	}
	return -1
}

// UserByID 返回指定 ID 用户的下标，不存在时返回 -1。
func (d *Document) UserByID(id int64) int {
	for index := range d.Users {
		if d.Users[index].ID == id {
			return index
		}
	}
	return -1
}

// StackByID 返回指定 ID stack 的下标，不存在时返回 -1。
func (d *Document) StackByID(id int64) int {
	for index := range d.Stacks {
		if d.Stacks[index].ID == id {
			return index
		}
	}
	return -1
}

// InboundByName 返回 stack 内同名入站的下标，不存在时返回 -1。
func (s *Stack) InboundByName(name string) int {
	for index := range s.Xray.Inbounds {
		if s.Xray.Inbounds[index].Name == name {
			return index
		}
	}
	return -1
}

// ProfileNames 返回用户全部档案名（含 default）。
func (u *User) ProfileNames() []string {
	names := []string{DefaultProfile}
	for _, profile := range u.Profiles {
		names = append(names, profile.Name)
	}
	return names
}

// DefaultProfile 是用户自身对应的档案名。
const DefaultProfile = "default"
