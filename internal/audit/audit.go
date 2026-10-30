// Package audit 写入操作审计日志。
package audit

import (
	"context"
	"time"

	"github.com/eagle/proxystack-web/internal/store/dbq"
)

// 审计动作名。
const (
	ActionLogin           = "auth.login"
	ActionLoginFailed     = "auth.login_failed"
	ActionLogout          = "auth.logout"
	ActionPasswordChange  = "admin.password_change"
	ActionPasswordReset   = "admin.password_reset"
	ActionResetLinkCreate = "admin.reset_link_create"
	ActionInit            = "system.init"
)

// Entry 是一条审计记录。
type Entry struct {
	Actor    string
	SourceIP string
	Action   string
	Object   string
	Summary  string
}

// Write 写入一条审计记录。
func Write(ctx context.Context, q *dbq.Queries, at time.Time, e Entry) error {
	return q.InsertAuditLog(ctx, dbq.InsertAuditLogParams{
		At:       at.Unix(),
		Actor:    e.Actor,
		SourceIp: e.SourceIP,
		Action:   e.Action,
		Object:   e.Object,
		Summary:  e.Summary,
	})
}
