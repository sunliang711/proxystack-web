// Package app 是应用服务层：所有业务操作的唯一入口。
package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/eagle/proxystack-web/internal/domain/validation"
	"github.com/eagle/proxystack-web/internal/paths"
	"github.com/eagle/proxystack-web/internal/store"
)

// Service 汇集应用服务层依赖。
type Service struct {
	Store  *store.Store
	Layout paths.Layout
	Now    func() time.Time
	// PortChecker 探测本机端口占用；为 nil 时不探测。
	PortChecker validation.PortChecker
}

// New 创建应用服务。
func New(st *store.Store, layout paths.Layout) *Service {
	return &Service{Store: st, Layout: layout, Now: time.Now, PortChecker: validation.TCPPortChecker{}}
}

// 应用层错误，由 HTTP 层映射为状态码。
var (
	ErrNotInitialized     = errors.New("database is not initialized; run `proxystack-web init` first")
	ErrAlreadyInitialized = errors.New("database is already initialized")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrAccountLocked      = errors.New("account is temporarily locked after repeated failures")
	ErrUnauthenticated    = errors.New("not logged in")
	ErrInvalidResetToken  = errors.New("reset link is invalid, expired or already used")
)

// FieldError 是可以定位到具体字段的校验错误。
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Message) }
