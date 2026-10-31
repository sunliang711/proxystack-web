package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/auth"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/tlsutil"
)

// DefaultAdminUsername 是 init 创建的管理员账号名。
const DefaultAdminUsername = "admin"

// InitOptions 是 init 的输入。
type InitOptions struct {
	Listen  string
	Port    int
	TLSMode string
	Domain  string
	Email   string
}

// InitResult 是 init 的输出；Password 只在此处出现一次。
type InitResult struct {
	Username    string
	Password    string
	PanelURL    string
	Fingerprint string
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

// Initialize 建库、迁移、生成管理员随机密码、面板随机路径和自签证书。
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
	if opts.TLSMode == store.TLSSelfSigned {
		hosts := tlsutil.LocalHosts()
		if opts.Domain != "" {
			hosts = append([]string{opts.Domain}, hosts...)
		}
		if err := os.MkdirAll(s.Layout.TLSDir(), 0o750); err != nil {
			return nil, err
		}
		if err := tlsutil.GenerateSelfSigned(s.Layout.TLSCert(), s.Layout.TLSKey(), hosts); err != nil {
			return nil, fmt.Errorf("generate self-signed certificate: %w", err)
		}
	}
	password := auth.RandomString(20)
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	panel := store.PanelSettings{
		Listen:     opts.Listen,
		Port:       opts.Port,
		Path:       strings.ToLower(auth.RandomString(16)),
		TLSMode:    opts.TLSMode,
		ACMEDomain: opts.Domain,
		ACMEEmail:  opts.Email,
	}
	basic := store.DefaultBasicSettings()
	basic.ExternalHost = opts.Domain
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
		return audit.Write(ctx, q, now, audit.Entry{Actor: "cli", Action: audit.ActionInit, Summary: "tls=" + opts.TLSMode})
	})
	if err != nil {
		return nil, err
	}
	info, err := s.Info(ctx)
	if err != nil {
		return nil, err
	}
	return &InitResult{Username: DefaultAdminUsername, Password: password, PanelURL: info.PanelURL, Fingerprint: info.Fingerprint}, nil
}

func validateInitOptions(opts *InitOptions) error {
	switch opts.TLSMode {
	case "":
		opts.TLSMode = store.TLSSelfSigned
	case store.TLSSelfSigned, store.TLSProxy:
	case store.TLSACME:
		return &FieldError{Field: "tls", Message: "ACME mode is not available yet; use self-signed or proxy"}
	default:
		return &FieldError{Field: "tls", Message: "must be one of self-signed, acme, proxy"}
	}
	if opts.Listen == "" {
		if opts.TLSMode == store.TLSProxy {
			opts.Listen = "127.0.0.1"
		} else {
			opts.Listen = "0.0.0.0"
		}
	}
	if opts.Port == 0 {
		opts.Port = 9443
	}
	if opts.Port < 1 || opts.Port > 65535 {
		return &FieldError{Field: "port", Message: "must be between 1 and 65535"}
	}
	return nil
}
