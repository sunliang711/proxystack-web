-- name: InsertAuditLog :exec
INSERT INTO audit_logs (at, actor, source_ip, action, object, summary)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListAuditLogs :many
SELECT * FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: CountAuditLogs :one
SELECT count(*) FROM audit_logs
WHERE (sqlc.arg(action) = '' OR action LIKE sqlc.arg(action) || '%')
  AND (sqlc.arg(actor) = '' OR actor = sqlc.arg(actor));

-- name: SearchAuditLogs :many
SELECT * FROM audit_logs
WHERE (sqlc.arg(action) = '' OR action LIKE sqlc.arg(action) || '%')
  AND (sqlc.arg(actor) = '' OR actor = sqlc.arg(actor))
ORDER BY id DESC LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);
