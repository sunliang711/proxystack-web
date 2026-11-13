-- name: ListHealthAlerts :many
SELECT * FROM health_alerts ORDER BY check_key;

-- name: UpsertHealthAlert :exec
INSERT INTO health_alerts (check_key, since, status, detail) VALUES (?, ?, ?, ?)
ON CONFLICT (check_key) DO UPDATE SET since = excluded.since, status = excluded.status, detail = excluded.detail;

-- name: DeleteHealthAlert :exec
DELETE FROM health_alerts WHERE check_key = ?;
