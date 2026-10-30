-- name: InsertAuditLog :exec
INSERT INTO audit_logs (at, actor, source_ip, action, object, summary)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListAuditLogs :many
SELECT * FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?;
