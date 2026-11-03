// Package app 是应用服务层：所有业务操作的唯一入口。
package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"proxystack-web/internal/deps"
	"proxystack-web/internal/domain/validation"
	"proxystack-web/internal/events"
	"proxystack-web/internal/notify"
	"proxystack-web/internal/paths"
	"proxystack-web/internal/runtime"
	"proxystack-web/internal/store"
	"proxystack-web/internal/subpublish"
	"proxystack-web/internal/units"
	"proxystack-web/internal/xrayapi"
)

// Service 汇集应用服务层依赖。
type Service struct {
	Store  *store.Store
	Layout paths.Layout
	Now    func() time.Time
	// PortChecker 探测本机端口占用；为 nil 时不探测。
	PortChecker validation.PortChecker

	// 运行时依赖：unit 管理、日志、xray 用户 API、生成物检查和事件总线。
	Units   units.Manager
	Logs    units.Logs
	XrayAPI xrayapi.API
	Checker runtime.Checker
	Events  *events.Bus
	// SettleDelay 是启动 unit 后检查其是否保持运行前的等待时间，为 0 时取默认值。
	SettleDelay time.Duration
	// HealthWait 是依赖替换后重启 unit 到检查其状态之间的等待时间，为 0 时取 10 秒。
	HealthWait time.Duration
	// ProbePorts 为 true 时依赖替换后的检查还要求监听端口已打开（内存实现下没有真实端口）。
	ProbePorts bool
	// RequestRestart 请求管理服务退出并由 systemd 重启（自更新后调用）。
	RequestRestart func()
	// SubPublisher 通过 SSH 正向转发测试并投递订阅包；测试可替换为内存实现。
	SubPublisher subpublish.Client
	// SubDeployer 通过同一 SSH 通道检测、安装、更新和回滚订阅端。
	SubDeployer subpublish.Deployer

	// runtimeMu 串行化 apply、unit 操作和暂停生效，避免并发写生成物或重启同一 unit。
	runtimeMu    sync.Mutex
	tasks        taskRegistry
	subs         subCache
	traffic      trafficState
	autoPause    autoPauseState
	scheduler    schedulerState
	subDeployMu  sync.Mutex
	subDeploying map[int64]bool
	guard        loginGuard
	// notificationMu 串行化邮件发送与出口事件替换，避免恢复后发送滞后的故障邮件。
	notificationMu sync.Mutex
	egressMu       sync.Mutex
	// background 跟踪退出前要收尾的后台循环。
	background sync.WaitGroup

	// SendMail 发送邮件，为 nil 时通过 SMTP 发送（测试替换）。
	SendMail func(ctx context.Context, config notify.Config, subject, body string) error
	// ProbeEgress 替换出口探测，生产环境为 nil 时使用真实 SOCKS 请求。
	ProbeEgress func(ctx context.Context, proxy deps.ProxyConfig, timeout time.Duration) ([]EgressResult, error)
}

// New 创建应用服务。
func New(st *store.Store, layout paths.Layout) *Service {
	fake := units.NewFake()
	return &Service{
		Store: st, Layout: layout, Now: time.Now, PortChecker: validation.TCPPortChecker{},
		Units: fake, Logs: fake, XrayAPI: &xrayapi.Fake{}, Checker: runtime.BinaryChecker{Layout: layout},
		Events:       events.New(),
		SubPublisher: subpublish.SSHClient{},
		SubDeployer:  subpublish.SSHClient{},
	}
}

// 应用层错误，由 HTTP 层映射为状态码。
var (
	ErrNotInitialized     = errors.New("database is not initialized; run `proxystack-web init` first")
	ErrAlreadyInitialized = errors.New("database is already initialized")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrAccountLocked      = errors.New("account is temporarily locked after repeated failures")
	ErrUnauthenticated    = errors.New("not logged in")
	ErrInvalidResetToken  = errors.New("reset link is invalid, expired or already used")
	// ErrTOTPRequired 表示密码正确但账号开启了两步验证，需要再提交验证码。
	ErrTOTPRequired = &FieldError{Field: "totp_code", Message: "two-factor code required"}
	// ErrInvalidTOTP 表示两步验证码错误或已被使用。
	ErrInvalidTOTP = &FieldError{Field: "totp_code", Message: "two-factor code is incorrect or already used"}
)

// FieldError 是可以定位到具体字段的校验错误。
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Message) }

// RemoteError 表示管理端访问远端订阅端失败，消息仅允许使用已脱敏的远端操作描述。
type RemoteError struct {
	Message string
}

func (e *RemoteError) Error() string { return e.Message }
