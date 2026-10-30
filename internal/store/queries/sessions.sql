-- name: CreateSession :exec
INSERT INTO sessions (id_hash, admin_id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSession :one
SELECT * FROM sessions WHERE id_hash = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id_hash = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash = ?;

-- name: DeleteAdminSessions :exec
DELETE FROM sessions WHERE admin_id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= ?;

-- name: DeleteUnusedResetTokens :exec
DELETE FROM admin_reset_tokens WHERE admin_id = ? AND used_at = 0;

-- name: CreateResetToken :exec
INSERT INTO admin_reset_tokens (token_hash, admin_id, created_at, expires_at)
VALUES (?, ?, ?, ?);

-- name: GetResetToken :one
SELECT * FROM admin_reset_tokens WHERE token_hash = ?;

-- name: MarkResetTokenUsed :execrows
UPDATE admin_reset_tokens SET used_at = ? WHERE token_hash = ? AND used_at = 0;

-- name: DeleteAdminSessionsExcept :exec
DELETE FROM sessions WHERE admin_id = ? AND id_hash != ?;
