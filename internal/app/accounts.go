package app

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/auth"
	"proxystack-web/internal/store/dbq"
)

// 管理账号的角色，与代理用户独立。
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

var (
	ErrForbidden    = errors.New("当前账号无权执行此操作")
	ErrLastAdmin    = errors.New("不能停用或降级最后一个启用的管理员")
	accountUsername = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)
)

// AccountView 是账号管理列表，不包含密码哈希或验证器密钥。
type AccountView struct {
	AdminView
	Disabled    bool      `json:"disabled"`
	LockedUntil time.Time `json:"locked_until"`
	LastLoginAt time.Time `json:"last_login_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func accountView(a dbq.Admin) AccountView {
	return AccountView{AdminView: adminView(a), Disabled: a.Disabled != 0,
		LockedUntil: time.Unix(a.LockedUntil, 0), LastLoginAt: time.Unix(a.LastLoginAt, 0),
		CreatedAt: time.Unix(a.CreatedAt, 0), UpdatedAt: time.Unix(a.UpdatedAt, 0)}
}

func validateAccountUsername(username string) error {
	if !accountUsername.MatchString(username) {
		return &FieldError{Field: "username", Message: "用户名需为 3～64 位字母、数字、点、下划线或连字符，且以字母或数字开头"}
	}
	return nil
}

func validateAccountRole(role string) error {
	if role != RoleAdmin && role != RoleViewer {
		return &FieldError{Field: "role", Message: "角色只能是 admin 或 viewer"}
	}
	return nil
}

// ListAccounts 只向管理员提供管理账号列表。
func (s *Service) ListAccounts(ctx context.Context, sess *SessionInfo) ([]AccountView, error) {
	admin, err := s.Store.Q.GetAdminByID(ctx, sess.Admin.ID)
	if err != nil {
		return nil, err
	}
	if admin.Role != RoleAdmin || admin.Disabled != 0 {
		return nil, ErrForbidden
	}
	rows, err := s.Store.Q.ListAdmins(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AccountView, 0, len(rows))
	for _, row := range rows {
		out = append(out, accountView(row))
	}
	return out, nil
}

// changeAccount 将密码二次验证、权限复核、账号修改和审计绑定在同一操作中。
func (s *Service) changeAccount(ctx context.Context, sess *SessionInfo, password string, meta RequestMeta, change func(*dbq.Queries, dbq.Admin) error) error {
	admin, err := s.Store.Q.GetAdminByID(ctx, sess.Admin.ID)
	if err != nil {
		return err
	}
	if admin.Role != RoleAdmin || admin.Disabled != 0 {
		return ErrForbidden
	}
	if err := s.verifyPasswordAgain(admin, password, meta, "current_password"); err != nil {
		return err
	}
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		// 防止校验密码后权限或会话发生变化，仍以旧权限执行管理操作。
		current, err := q.GetAdminByID(ctx, admin.ID)
		if err != nil {
			return err
		}
		if current.Disabled != 0 || current.Role != RoleAdmin || current.PasswordHash != admin.PasswordHash {
			return ErrForbidden
		}
		if _, err := q.GetSession(ctx, auth.HashToken(sess.Token)); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrUnauthenticated
			}
			return err
		}
		return change(q, current)
	})
}

func accountTarget(ctx context.Context, q *dbq.Queries, id int64) (dbq.Admin, error) {
	admin, err := q.GetAdminByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return dbq.Admin{}, ErrNotFound
	}
	return admin, err
}

func accountUsernameAvailable(ctx context.Context, q *dbq.Queries, username string, id int64) error {
	existing, err := q.GetAdminByUsername(ctx, username)
	if err == nil && existing.ID != id {
		return &FieldError{Field: "username", Message: "用户名已存在"}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

// CreateAccount 创建管理账号，首次登录必须修改初始密码。
func (s *Service) CreateAccount(ctx context.Context, sess *SessionInfo, currentPassword, username, password, role string, meta RequestMeta) (*AccountView, error) {
	if err := validateAccountUsername(username); err != nil {
		return nil, err
	}
	if err := validateAccountRole(role); err != nil {
		return nil, err
	}
	if err := auth.ValidateNewPassword(password); err != nil {
		return nil, &FieldError{Field: "password", Message: err.Error()}
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	var out AccountView
	err = s.changeAccount(ctx, sess, currentPassword, meta, func(q *dbq.Queries, actor dbq.Admin) error {
		if err := accountUsernameAvailable(ctx, q, username, 0); err != nil {
			return err
		}
		now := s.Now()
		admin, err := q.CreateManagedAdmin(ctx, dbq.CreateManagedAdminParams{Username: username, PasswordHash: hash, Role: role, CreatedAt: now.Unix(), UpdatedAt: now.Unix()})
		if err != nil {
			return err
		}
		out = accountView(admin)
		return audit.Write(ctx, q, now, audit.Entry{Actor: actor.Username, SourceIP: meta.IP, Action: "admin.create", Object: username, Summary: "role=" + role})
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RenameAccount 修改用户名，保留该账号的现有会话。
func (s *Service) RenameAccount(ctx context.Context, sess *SessionInfo, password string, id int64, username string, meta RequestMeta) error {
	if err := validateAccountUsername(username); err != nil {
		return err
	}
	return s.changeAccount(ctx, sess, password, meta, func(q *dbq.Queries, actor dbq.Admin) error {
		target, err := accountTarget(ctx, q, id)
		if err != nil {
			return err
		}
		if err := accountUsernameAvailable(ctx, q, username, id); err != nil {
			return err
		}
		if err := q.SetAdminUsername(ctx, dbq.SetAdminUsernameParams{Username: username, UpdatedAt: s.Now().Unix(), ID: id}); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: actor.Username, SourceIP: meta.IP, Action: "admin.rename", Object: username, Summary: "previous_username=" + target.Username})
	})
}

func ensureOtherAdmin(ctx context.Context, q *dbq.Queries, target dbq.Admin) error {
	if target.Role != RoleAdmin || target.Disabled != 0 {
		return nil
	}
	count, err := q.CountEnabledAdmins(ctx)
	if err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// ChangeAccountRole 变更角色并撤销该账号全部会话及未使用的重置链接。
func (s *Service) ChangeAccountRole(ctx context.Context, sess *SessionInfo, password string, id int64, role string, meta RequestMeta) error {
	if err := validateAccountRole(role); err != nil {
		return err
	}
	return s.changeAccount(ctx, sess, password, meta, func(q *dbq.Queries, actor dbq.Admin) error {
		target, err := accountTarget(ctx, q, id)
		if err != nil || target.Role == role {
			return err
		}
		if role != RoleAdmin {
			if err := ensureOtherAdmin(ctx, q, target); err != nil {
				return err
			}
		}
		if err := q.SetAdminRole(ctx, dbq.SetAdminRoleParams{Role: role, UpdatedAt: s.Now().Unix(), ID: id}); err != nil {
			return err
		}
		if err := s.revokeAdminAccess(ctx, q, id, false); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: actor.Username, SourceIP: meta.IP, Action: "admin.role_change", Object: target.Username, Summary: "role=" + role})
	})
}

// SetAccountDisabled 停用时立即撤销访问，不允许停用当前登录账号。
func (s *Service) SetAccountDisabled(ctx context.Context, sess *SessionInfo, password string, id int64, disabled bool, meta RequestMeta) error {
	return s.changeAccount(ctx, sess, password, meta, func(q *dbq.Queries, actor dbq.Admin) error {
		target, err := accountTarget(ctx, q, id)
		if err != nil || (target.Disabled != 0) == disabled {
			return err
		}
		value, action := int64(0), "admin.enable"
		if disabled {
			if err := ensureOtherAdmin(ctx, q, target); err != nil {
				return err
			}
			if id == actor.ID {
				return &FieldError{Field: "id", Message: "不能停用当前登录账号"}
			}
			value, action = 1, "admin.disable"
		}
		if err := q.SetAdminDisabled(ctx, dbq.SetAdminDisabledParams{Disabled: value, UpdatedAt: s.Now().Unix(), ID: id}); err != nil {
			return err
		}
		if disabled {
			if err := s.revokeAdminAccess(ctx, q, id, false); err != nil {
				return err
			}
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: actor.Username, SourceIP: meta.IP, Action: action, Object: target.Username})
	})
}

// ResetAccountPassword 重置其他账号后强制其改密；重置自己时保留当前会话。
func (s *Service) ResetAccountPassword(ctx context.Context, sess *SessionInfo, currentPassword string, id int64, password string, meta RequestMeta) error {
	if err := auth.ValidateNewPassword(password); err != nil {
		return &FieldError{Field: "new_password", Message: err.Error()}
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.changeAccount(ctx, sess, currentPassword, meta, func(q *dbq.Queries, actor dbq.Admin) error {
		target, err := accountTarget(ctx, q, id)
		if err != nil {
			return err
		}
		mustChange, keep := int64(1), ""
		if id == actor.ID {
			mustChange, keep = 0, auth.HashToken(sess.Token)
		}
		if err := q.SetAdminPassword(ctx, dbq.SetAdminPasswordParams{PasswordHash: hash, MustChangePassword: mustChange, UpdatedAt: s.Now().Unix(), ID: id}); err != nil {
			return err
		}
		if err := q.DeleteAdminSessionsExcept(ctx, dbq.DeleteAdminSessionsExceptParams{AdminID: id, IDHash: keep}); err != nil {
			return err
		}
		if err := q.DeleteUnusedResetTokens(ctx, id); err != nil {
			return err
		}
		return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: actor.Username, SourceIP: meta.IP, Action: audit.ActionPasswordReset, Object: target.Username, Summary: "via=account_management"})
	})
}
