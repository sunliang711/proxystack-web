package app

import (
	"context"
	"net"
	"strings"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/auth"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
)

// DefaultAdminUsername 是 init 创建的管理员账号名。
const DefaultAdminUsername = "admin"

// InitOptions 是 init 的输入。
type InitOptions struct {
	Listen string
	Port   int
}

// InitResult 是 init 的输出；Password 只在此处出现一次。
type InitResult struct {
	Username string
	Password string
	PanelURL string
}

// IsInitialized 判断数据库是否已经完成 init。
func (s *Service) IsInitialized(ctx context.Context) (bool, error) {
	current, _, err := s.Store.MigrationStatus(ctx)
	if err != nil || current == 0 {
		return false, nil
	}
	n, err := s.Store.Q.CountAdmins(ctx)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Initialize 建库、迁移、生成管理员随机密码和面板随机路径。
func (s *Service) Initialize(ctx context.Context, opts InitOptions) (*InitResult, error) {
	if err := validateInitOptions(&opts); err != nil {
		return nil, err
	}
	if err := s.Store.Migrate(ctx); err != nil {
		return nil, err
	}
	if ok, err := s.IsInitialized(ctx); err != nil {
		return nil, err
	} else if ok {
		return nil, ErrAlreadyInitialized
	}
	password := auth.RandomString(20)
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	panel := store.PanelSettings{
		Listen: opts.Listen,
		Port:   opts.Port,
		Path:   strings.ToLower(auth.RandomString(16)),
	}
	basic := store.DefaultBasicSettings()
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if _, err := q.CreateAdmin(ctx, dbq.CreateAdminParams{
			Username:           DefaultAdminUsername,
			PasswordHash:       hash,
			MustChangePassword: 1,
			CreatedAt:          now.Unix(),
			UpdatedAt:          now.Unix(),
		}); err != nil {
			return err
		}
		if err := store.PutJSON(ctx, q, store.SettingPanel, panel); err != nil {
			return err
		}
		if err := store.PutJSON(ctx, q, store.SettingBasic, basic); err != nil {
			return err
		}
		return audit.Write(ctx, q, now, audit.Entry{Actor: "cli", Action: audit.ActionInit, Summary: "初始化"})
	})
	if err != nil {
		return nil, err
	}
	info, err := s.Info(ctx)
	if err != nil {
		return nil, err
	}
	return &InitResult{Username: DefaultAdminUsername, Password: password, PanelURL: info.PanelURL}, nil
}

func validateInitOptions(opts *InitOptions) error {
	// 面板只在局域网使用，默认监听全部地址，nginx 可以在别的机器上；TLS 在 nginx 上终结。
	if opts.Listen == "" {
		opts.Listen = "0.0.0.0"
	}
	if net.ParseIP(opts.Listen) == nil {
		return &FieldError{Field: "listen", Message: "must be an IP address"}
	}
	if opts.Port == 0 {
		opts.Port = 9443
	}
	if opts.Port < 1 || opts.Port > 65535 {
		return &FieldError{Field: "port", Message: "must be between 1 and 65535"}
	}
	return nil
}
