package app

import (
	"context"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/auth"
	"proxystack-web/internal/store/dbq"
)

// ActiveSessionView 是当前账号的活跃会话，不包含任何登录凭据。
type ActiveSessionView struct {
	ID         string    `json:"id"`
	ClientIP   string    `json:"client_ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at" doc:"闲置与绝对有效期中较早的到期时间"`
	Current    bool      `json:"current"`
}

// 会话管理使用单独派生的标识，避免将数据库内的令牌哈希暴露给浏览器。
func sessionPublicID(idHash string) string { return auth.HashToken("session-id:" + idHash) }

func activeSessionParams(adminID int64, now time.Time) dbq.ListActiveAdminSessionsParams {
	return dbq.ListActiveAdminSessionsParams{AdminID: adminID, Now: now.Unix(), CreatedAfter: now.Add(-SessionMaxLifetime).Unix()}
}

// ListActiveSessions 只列出当前账号尚未超过闲置或绝对有效期的会话。
func (s *Service) ListActiveSessions(ctx context.Context, sess *SessionInfo) ([]ActiveSessionView, error) {
	rows, err := s.Store.Q.ListActiveAdminSessions(ctx, activeSessionParams(sess.Admin.ID, s.Now()))
	if err != nil {
		return nil, err
	}
	out := make([]ActiveSessionView, 0, len(rows))
	current := auth.HashToken(sess.Token)
	for _, row := range rows {
		expires := min(row.ExpiresAt, time.Unix(row.CreatedAt, 0).Add(SessionMaxLifetime).Unix())
		out = append(out, ActiveSessionView{ID: sessionPublicID(row.IDHash), ClientIP: row.Ip, UserAgent: row.UserAgent,
			CreatedAt: time.Unix(row.CreatedAt, 0), LastSeenAt: time.Unix(row.LastSeenAt, 0), ExpiresAt: time.Unix(expires, 0), Current: row.IDHash == current})
	}
	return out, nil
}

// RevokeSession 只注销调用者自己的会话；不存在或不属于自己的标识均返回未找到。
func (s *Service) RevokeSession(ctx context.Context, sess *SessionInfo, id string, meta RequestMeta) (bool, error) {
	current := false
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListActiveAdminSessions(ctx, activeSessionParams(sess.Admin.ID, s.Now()))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if sessionPublicID(row.IDHash) != id {
				continue
			}
			n, err := q.DeleteOwnedSession(ctx, dbq.DeleteOwnedSessionParams{IDHash: row.IDHash, AdminID: sess.Admin.ID})
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrNotFound
			}
			current = row.IDHash == auth.HashToken(sess.Token)
			return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: sess.Admin.Username, SourceIP: meta.IP, Action: "auth.session_revoke", Object: id})
		}
		return ErrNotFound
	})
	return current, err
}
