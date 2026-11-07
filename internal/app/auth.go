package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/auth"
	"proxystack-web/internal/store/dbq"
)

// 会话与登录保护参数。
const (
	// SessionIdleTimeout 是闲置超时：只有管理员实际操作的请求才把到期时间往后推，页面后台的定时刷新不算。
	SessionIdleTimeout = 24 * time.Hour
	// SessionMaxLifetime 是会话的绝对有效期：持续使用的会话到期后也要重新登录。
	SessionMaxLifetime = 7 * 24 * time.Hour
	ResetLinkTTL       = 15 * time.Minute
	maxLoginFailures   = 5
	lockoutDuration    = 15 * time.Minute
)

// RequestMeta 是请求来源信息，用于审计。
type RequestMeta struct {
	IP        string
	UserAgent string
}

// AdminView 是返回给前端的管理员信息。
type AdminView struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	MustChangePassword bool   `json:"must_change_password"`
	TOTPEnabled        bool   `json:"totp_enabled"`
}

// SessionInfo 是一个有效会话。
type SessionInfo struct {
	Token     string
	CSRFToken string
	Admin     AdminView
	// ExpiresAt 是闲置到期时间，MaxExpiresAt 是绝对到期时间，会话在两者中较早的一个失效。
	ExpiresAt    time.Time
	MaxExpiresAt time.Time
}

func adminView(a dbq.Admin) AdminView {
	return AdminView{ID: a.ID, Username: a.Username, MustChangePassword: a.MustChangePassword != 0, TOTPEnabled: a.TotpSecret != ""}
}

// Login 校验账号密码，账号开启两步验证时再校验验证码，然后创建会话。
// 密码正确但未提交验证码时返回 ErrTOTPRequired，不计入失败次数。
func (s *Service) Login(ctx context.Context, username, password, totpCode string, meta RequestMeta) (*SessionInfo, error) {
	now := s.Now()
	if err := s.guard.check(meta.IP, now); err != nil {
		return nil, err
	}
	admin, err := s.Store.Q.GetAdminByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		// 用固定哈希做一次校验，避免通过响应时间区分账号是否存在。
		_, _ = auth.VerifyPassword(password, dummyHash)
		s.guard.fail(meta.IP, now)
		s.auditLoginFailure(ctx, username, meta, "unknown user")
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if admin.LockedUntil > now.Unix() {
		s.guard.fail(meta.IP, now)
		return nil, ErrAccountLocked
	}
	ok, err := auth.VerifyPassword(password, admin.PasswordHash)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, s.recordLoginFailure(ctx, admin, meta, "bad password", ErrInvalidCredentials)
	}
	if admin.TotpSecret != "" {
		if totpCode == "" {
			return nil, ErrTOTPRequired
		}
		step, ok := auth.VerifyTOTP(admin.TotpSecret, totpCode, now, admin.TotpLastStep)
		if ok {
			// 条件更新保证同一个验证码在并发请求中也只能使用一次。
			n, err := s.Store.Q.AdvanceTOTPStep(ctx, dbq.AdvanceTOTPStepParams{Step: step, ID: admin.ID})
			if err != nil {
				return nil, err
			}
			ok = n == 1
		}
		if !ok {
			return nil, s.recordLoginFailure(ctx, admin, meta, "bad two-factor code", ErrInvalidTOTP)
		}
	}
	s.guard.succeed(meta.IP)
	info := &SessionInfo{
		Token: auth.RandomToken(), CSRFToken: auth.RandomToken(), Admin: adminView(admin),
		ExpiresAt: now.Add(SessionIdleTimeout), MaxExpiresAt: now.Add(SessionMaxLifetime),
	}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if err := q.ResetLoginFailures(ctx, admin.ID); err != nil {
			return err
		}
		if err := q.DeleteExpiredSessions(ctx, now.Unix()); err != nil {
			return err
		}
		if err := q.CreateSession(ctx, dbq.CreateSessionParams{
			IDHash:     auth.HashToken(info.Token),
			AdminID:    admin.ID,
			CsrfToken:  info.CSRFToken,
			CreatedAt:  now.Unix(),
			LastSeenAt: now.Unix(),
			ExpiresAt:  info.ExpiresAt.Unix(),
			Ip:         meta.IP,
			UserAgent:  meta.UserAgent,
		}); err != nil {
			return err
		}
		return audit.Write(ctx, q, now, audit.Entry{Actor: admin.Username, SourceIP: meta.IP, Action: audit.ActionLogin})
	})
	if err != nil {
		return nil, err
	}
	return info, nil
}

var dummyHash, _ = auth.HashPassword("proxystack-web-dummy")

// verifyPasswordAgain 在已登录的敏感操作中再次校验密码。失败计入来源 IP 限速，
// 防止拿到会话的人借这些入口猜测密码；不计入账号锁定，避免把管理员自己锁在外面。
func (s *Service) verifyPasswordAgain(admin dbq.Admin, password string, meta RequestMeta, field string) error {
	now := s.Now()
	if err := s.guard.check(meta.IP, now); err != nil {
		return err
	}
	ok, err := auth.VerifyPassword(password, admin.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		s.guard.fail(meta.IP, now)
		return &FieldError{Field: field, Message: "current password is incorrect"}
	}
	return nil
}

// recordLoginFailure 累计账号和来源 IP 的失败次数，账号达到阈值时锁定，返回 result。
func (s *Service) recordLoginFailure(ctx context.Context, admin dbq.Admin, meta RequestMeta, reason string, result error) error {
	now := s.Now()
	s.guard.fail(meta.IP, now)
	lockedUntil := int64(0)
	if admin.FailedLogins+1 >= maxLoginFailures {
		lockedUntil = now.Add(lockoutDuration).Unix()
	}
	if err := s.Store.Q.RecordLoginFailure(ctx, dbq.RecordLoginFailureParams{LockedUntil: lockedUntil, ID: admin.ID}); err != nil {
		return err
	}
	s.auditLoginFailure(ctx, admin.Username, meta, reason)
	return result
}

func (s *Service) auditLoginFailure(ctx context.Context, username string, meta RequestMeta, reason string) {
	_ = audit.Write(ctx, s.Store.Q, s.Now(), audit.Entry{Actor: username, SourceIP: meta.IP, Action: audit.ActionLoginFailed, Summary: reason})
}

// Authenticate 校验会话令牌并滑动续期。
// Authenticate 校验会话。activity 表示这是管理员实际操作发出的请求，只有这类请求续期闲置超时。
func (s *Service) Authenticate(ctx context.Context, token string, activity bool) (*SessionInfo, error) {
	if token == "" {
		return nil, ErrUnauthenticated
	}
	now := s.Now()
	idHash := auth.HashToken(token)
	sess, err := s.Store.Q.GetSession(ctx, idHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if sess.ExpiresAt <= now.Unix() || now.Sub(time.Unix(sess.CreatedAt, 0)) >= SessionMaxLifetime {
		_ = s.Store.Q.DeleteSession(ctx, idHash)
		return nil, ErrUnauthenticated
	}
	admin, err := s.Store.Q.GetAdminByID(ctx, sess.AdminID)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	expiresAt := sess.ExpiresAt
	if activity && now.Unix()-sess.LastSeenAt >= 60 {
		expiresAt = now.Add(SessionIdleTimeout).Unix()
		if err := s.Store.Q.TouchSession(ctx, dbq.TouchSessionParams{
			LastSeenAt: now.Unix(),
			ExpiresAt:  expiresAt,
			IDHash:     idHash,
		}); err != nil {
			return nil, err
		}
	}
	return &SessionInfo{
		Token: token, CSRFToken: sess.CsrfToken, Admin: adminView(admin),
		ExpiresAt: time.Unix(expiresAt, 0), MaxExpiresAt: time.Unix(sess.CreatedAt, 0).Add(SessionMaxLifetime),
	}, nil
}

// Logout 删除会话。
func (s *Service) Logout(ctx context.Context, sess *SessionInfo, meta RequestMeta) error {
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if err := q.DeleteSession(ctx, auth.HashToken(sess.Token)); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: sess.Admin.Username, SourceIP: meta.IP, Action: audit.ActionLogout})
	})
}

// ChangePassword 校验旧密码后设置新密码，并注销该管理员的其他会话。
func (s *Service) ChangePassword(ctx context.Context, sess *SessionInfo, oldPassword, newPassword string, meta RequestMeta) error {
	admin, err := s.Store.Q.GetAdminByID(ctx, sess.Admin.ID)
	if err != nil {
		return err
	}
	if err := s.verifyPasswordAgain(admin, oldPassword, meta, "old_password"); err != nil {
		return err
	}
	if err := auth.ValidateNewPassword(newPassword); err != nil {
		return &FieldError{Field: "new_password", Message: err.Error()}
	}
	if newPassword == oldPassword {
		return &FieldError{Field: "new_password", Message: "new password must differ from the current one"}
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	now := s.Now()
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if err := q.SetAdminPassword(ctx, dbq.SetAdminPasswordParams{PasswordHash: hash, MustChangePassword: 0, UpdatedAt: now.Unix(), ID: admin.ID}); err != nil {
			return err
		}
		if err := q.DeleteAdminSessionsExcept(ctx, dbq.DeleteAdminSessionsExceptParams{AdminID: admin.ID, IDHash: auth.HashToken(sess.Token)}); err != nil {
			return err
		}
		return audit.Write(ctx, q, now, audit.Entry{Actor: admin.Username, SourceIP: meta.IP, Action: audit.ActionPasswordChange})
	})
}

// ResetPasswordOptions 是命令行重置密码的输入。
type ResetPasswordOptions struct {
	Username    string
	Password    string // 为空时生成随机密码
	DisableTOTP bool
}

// ResetPassword 由本机命令行直接重置密码；返回生效的新密码。
func (s *Service) ResetPassword(ctx context.Context, opts ResetPasswordOptions) (string, error) {
	admin, err := s.lookupAdmin(ctx, opts.Username)
	if err != nil {
		return "", err
	}
	password := opts.Password
	if password == "" {
		password = auth.RandomString(20)
	} else if err := auth.ValidateNewPassword(password); err != nil {
		return "", err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	now := s.Now()
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if err := q.SetAdminPassword(ctx, dbq.SetAdminPasswordParams{PasswordHash: hash, MustChangePassword: 1, UpdatedAt: now.Unix(), ID: admin.ID}); err != nil {
			return err
		}
		if err := s.revokeAdminAccess(ctx, q, admin.ID, opts.DisableTOTP); err != nil {
			return err
		}
		return audit.Write(ctx, q, now, audit.Entry{Actor: "cli", Action: audit.ActionPasswordReset, Object: admin.Username, Summary: resetSummary("cli", opts.DisableTOTP)})
	})
	if err != nil {
		return "", err
	}
	return password, nil
}

// CreateResetLink 生成一次性重置令牌，使之前未使用的令牌作废；返回明文令牌。
func (s *Service) CreateResetLink(ctx context.Context, username string, disableTOTP bool) (string, error) {
	admin, err := s.lookupAdmin(ctx, username)
	if err != nil {
		return "", err
	}
	token := auth.RandomToken()
	now := s.Now()
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if err := q.DeleteUnusedResetTokens(ctx, admin.ID); err != nil {
			return err
		}
		if err := q.CreateResetToken(ctx, dbq.CreateResetTokenParams{
			TokenHash: auth.HashToken(token),
			AdminID:   admin.ID,
			CreatedAt: now.Unix(),
			ExpiresAt: now.Add(ResetLinkTTL).Unix(),
		}); err != nil {
			return err
		}
		if disableTOTP {
			if err := q.ClearAdminTOTP(ctx, dbq.ClearAdminTOTPParams{UpdatedAt: now.Unix(), ID: admin.ID}); err != nil {
				return err
			}
		}
		return audit.Write(ctx, q, now, audit.Entry{Actor: "cli", Action: audit.ActionResetLinkCreate, Object: admin.Username, Summary: resetSummary("cli", disableTOTP)})
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// ResetWithToken 用一次性链接设置新密码。
func (s *Service) ResetWithToken(ctx context.Context, token, newPassword string, meta RequestMeta) error {
	if err := s.guard.check(meta.IP, s.Now()); err != nil {
		return err
	}
	err := s.resetWithToken(ctx, token, newPassword, meta)
	if errors.Is(err, ErrInvalidResetToken) {
		s.guard.fail(meta.IP, s.Now())
	}
	return err
}

func (s *Service) resetWithToken(ctx context.Context, token, newPassword string, meta RequestMeta) error {
	if err := auth.ValidateNewPassword(newPassword); err != nil {
		return &FieldError{Field: "new_password", Message: err.Error()}
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	now := s.Now()
	tokenHash := auth.HashToken(token)
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		rt, err := q.GetResetToken(ctx, tokenHash)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidResetToken
		}
		if err != nil {
			return err
		}
		if rt.UsedAt != 0 || rt.ExpiresAt <= now.Unix() {
			return ErrInvalidResetToken
		}
		n, err := q.MarkResetTokenUsed(ctx, dbq.MarkResetTokenUsedParams{UsedAt: now.Unix(), TokenHash: tokenHash})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrInvalidResetToken
		}
		admin, err := q.GetAdminByID(ctx, rt.AdminID)
		if err != nil {
			return err
		}
		if err := q.SetAdminPassword(ctx, dbq.SetAdminPasswordParams{PasswordHash: hash, MustChangePassword: 0, UpdatedAt: now.Unix(), ID: admin.ID}); err != nil {
			return err
		}
		if err := q.DeleteAdminSessions(ctx, admin.ID); err != nil {
			return err
		}
		return audit.Write(ctx, q, now, audit.Entry{Actor: admin.Username, SourceIP: meta.IP, Action: audit.ActionPasswordReset, Object: admin.Username, Summary: "via=link"})
	})
}

func (s *Service) revokeAdminAccess(ctx context.Context, q *dbq.Queries, adminID int64, disableTOTP bool) error {
	if err := q.DeleteAdminSessions(ctx, adminID); err != nil {
		return err
	}
	if err := q.DeleteUnusedResetTokens(ctx, adminID); err != nil {
		return err
	}
	if disableTOTP {
		return q.ClearAdminTOTP(ctx, dbq.ClearAdminTOTPParams{UpdatedAt: s.Now().Unix(), ID: adminID})
	}
	return nil
}

func (s *Service) lookupAdmin(ctx context.Context, username string) (dbq.Admin, error) {
	if username == "" {
		admins, err := s.Store.Q.ListAdmins(ctx)
		if err != nil {
			return dbq.Admin{}, err
		}
		if len(admins) == 0 {
			return dbq.Admin{}, ErrNotInitialized
		}
		return admins[0], nil
	}
	admin, err := s.Store.Q.GetAdminByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return dbq.Admin{}, fmt.Errorf("admin %q does not exist", username)
	}
	return admin, err
}

func resetSummary(via string, disableTOTP bool) string {
	summary := "via=" + via
	if disableTOTP {
		summary += " totp=disabled"
	}
	return summary
}

// TOTPIssuer 是验证器应用中显示的服务名。
const TOTPIssuer = "proxystack-web"

// TOTPSetup 是开启两步验证时交给验证器应用的密钥。
type TOTPSetup struct {
	Secret string `json:"secret" doc:"Base32 密钥，无法扫码时手工输入"`
	URI    string `json:"uri" doc:"otpauth 地址，前端渲染为二维码"`
}

// BeginTOTP 生成新的待确认密钥；确认前不影响登录，再次调用会替换之前未确认的密钥。
func (s *Service) BeginTOTP(ctx context.Context, sess *SessionInfo) (*TOTPSetup, error) {
	admin, err := s.Store.Q.GetAdminByID(ctx, sess.Admin.ID)
	if err != nil {
		return nil, err
	}
	if admin.TotpSecret != "" {
		return nil, &FieldError{Field: "totp_code", Message: "two-factor authentication is already enabled"}
	}
	secret := auth.NewTOTPSecret()
	if err := s.Store.Q.SetAdminTOTPPending(ctx, dbq.SetAdminTOTPPendingParams{TotpPendingSecret: secret, UpdatedAt: s.Now().Unix(), ID: admin.ID}); err != nil {
		return nil, err
	}
	return &TOTPSetup{Secret: secret, URI: auth.TOTPURI(TOTPIssuer, admin.Username, secret)}, nil
}

// EnableTOTP 用验证器应用生成的第一个验证码确认密钥并开启两步验证，同时注销该管理员的其他会话。
func (s *Service) EnableTOTP(ctx context.Context, sess *SessionInfo, code string, meta RequestMeta) error {
	admin, err := s.Store.Q.GetAdminByID(ctx, sess.Admin.ID)
	if err != nil {
		return err
	}
	if admin.TotpSecret != "" {
		return &FieldError{Field: "totp_code", Message: "two-factor authentication is already enabled"}
	}
	if admin.TotpPendingSecret == "" {
		return &FieldError{Field: "totp_code", Message: "start the setup again to get a new secret"}
	}
	now := s.Now()
	step, ok := auth.VerifyTOTP(admin.TotpPendingSecret, code, now, 0)
	if !ok {
		return &FieldError{Field: "totp_code", Message: "code is incorrect; check the time on your phone"}
	}
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if err := q.EnableAdminTOTP(ctx, dbq.EnableAdminTOTPParams{TotpLastStep: step, UpdatedAt: now.Unix(), ID: admin.ID}); err != nil {
			return err
		}
		if err := q.DeleteAdminSessionsExcept(ctx, dbq.DeleteAdminSessionsExceptParams{AdminID: admin.ID, IDHash: auth.HashToken(sess.Token)}); err != nil {
			return err
		}
		return audit.Write(ctx, q, now, audit.Entry{Actor: admin.Username, SourceIP: meta.IP, Action: audit.ActionTOTPEnable, Object: admin.Username})
	})
}

// DisableTOTP 关闭两步验证，要求同时提供密码和当前验证码。验证码设备丢失时用命令行的 --disable-totp。
func (s *Service) DisableTOTP(ctx context.Context, sess *SessionInfo, password, code string, meta RequestMeta) error {
	admin, err := s.Store.Q.GetAdminByID(ctx, sess.Admin.ID)
	if err != nil {
		return err
	}
	if admin.TotpSecret == "" {
		return &FieldError{Field: "totp_code", Message: "two-factor authentication is not enabled"}
	}
	if err := s.verifyPasswordAgain(admin, password, meta, "password"); err != nil {
		return err
	}
	now := s.Now()
	step, ok := auth.VerifyTOTP(admin.TotpSecret, code, now, admin.TotpLastStep)
	if !ok {
		s.guard.fail(meta.IP, now)
		return &FieldError{Field: "totp_code", Message: "code is incorrect or already used"}
	}
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		n, err := q.AdvanceTOTPStep(ctx, dbq.AdvanceTOTPStepParams{Step: step, ID: admin.ID})
		if err != nil {
			return err
		}
		if n != 1 {
			return &FieldError{Field: "totp_code", Message: "code is incorrect or already used"}
		}
		if err := q.ClearAdminTOTP(ctx, dbq.ClearAdminTOTPParams{UpdatedAt: now.Unix(), ID: admin.ID}); err != nil {
			return err
		}
		return audit.Write(ctx, q, now, audit.Entry{Actor: admin.Username, SourceIP: meta.IP, Action: audit.ActionTOTPDisable, Object: admin.Username, Summary: "via=panel"})
	})
}
