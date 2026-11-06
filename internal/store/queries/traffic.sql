-- name: AddTrafficMinute :exec
INSERT INTO traffic_minute (bucket_start, stack_id, scope, subject, up, down)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (bucket_start, stack_id, scope, subject) DO UPDATE SET
    up = up + excluded.up, down = down + excluded.down;

-- name: GetUserUsage :one
SELECT * FROM user_usage WHERE user_id = ?;

-- name: ListUserUsage :many
SELECT * FROM user_usage;

-- name: ResetUserUsage :exec
INSERT INTO user_usage (user_id, period_start, up, down) VALUES (?, ?, 0, 0)
ON CONFLICT (user_id) DO UPDATE SET period_start = excluded.period_start, up = 0, down = 0;

-- name: AddUserUsage :exec
UPDATE user_usage SET up = up + ?, down = down + ? WHERE user_id = ?;

-- name: AddUserDailyUsage :exec
INSERT INTO user_daily_usage (user_id, day, up, down) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, day) DO UPDATE SET up = up + excluded.up, down = down + excluded.down;

-- name: ListUserDailyUsage :many
SELECT * FROM user_daily_usage WHERE day = ?;

-- name: ListStackDailyUsage :many
SELECT stack_id, up, down
FROM traffic_day
WHERE bucket_start = ? AND scope = 'inbound';

-- name: ListUserDailySeries :many
SELECT * FROM user_daily_usage WHERE user_id = ? AND day >= ? ORDER BY day;

-- name: ExemptUserDaily :exec
INSERT INTO user_daily_usage (user_id, day, exempt) VALUES (?, ?, 1)
ON CONFLICT (user_id, day) DO UPDATE SET exempt = 1;

-- name: DeleteUserDailyBefore :exec
DELETE FROM user_daily_usage WHERE day < ?;

-- name: ListAutoPauses :many
SELECT * FROM user_auto_pause ORDER BY user_id, reason;

-- name: InsertAutoPause :exec
INSERT OR IGNORE INTO user_auto_pause (user_id, reason, since) VALUES (?, ?, ?);

-- name: DeleteAutoPause :exec
DELETE FROM user_auto_pause WHERE user_id = ? AND reason = ?;

-- name: InsertNotification :execrows
INSERT OR IGNORE INTO notifications (at, event, user_id, user_name, dedup_key, subject, body, status, next_attempt_at, stack_id, stack_name, incident)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListDueNotifications :many
SELECT * FROM notifications WHERE status = 'pending' AND next_attempt_at <= ? ORDER BY id LIMIT 20;

-- name: UpdateNotification :exec
UPDATE notifications SET status = ?, error = ?, attempts = ?, next_attempt_at = ? WHERE id = ?;

-- name: GetNotification :one
SELECT * FROM notifications WHERE id = ?;

-- name: CountFailedNotifications :one
SELECT count(*) FROM notifications WHERE status = 'failed';

-- name: ListNotifications :many
SELECT * FROM notifications ORDER BY id DESC LIMIT ?;

-- name: DeleteNotificationsBefore :exec
DELETE FROM notifications WHERE at < ?;

-- name: UpsertTrafficName :exec
INSERT INTO traffic_names (kind, id, name, role) VALUES (?, ?, ?, ?)
ON CONFLICT (kind, id) DO UPDATE SET name = excluded.name, role = excluded.role;

-- name: ListTrafficNames :many
SELECT * FROM traffic_names;

-- name: GetOpenTrafficGap :one
SELECT * FROM traffic_gaps WHERE stack_id = ? AND open = 1 ORDER BY id DESC LIMIT 1;

-- name: InsertTrafficGap :exec
INSERT INTO traffic_gaps (stack_id, kind, reason, start_at, end_at, open) VALUES (?, ?, ?, ?, ?, ?);

-- name: ExtendTrafficGap :exec
UPDATE traffic_gaps SET end_at = ?, reason = ? WHERE id = ?;

-- name: CloseTrafficGap :exec
UPDATE traffic_gaps SET end_at = ?, open = 0 WHERE id = ?;

-- name: CloseOpenTrafficGapsExcept :exec
UPDATE traffic_gaps SET open = 0 WHERE open = 1 AND stack_id NOT IN (sqlc.slice('stack_ids'));

-- name: ListTrafficGaps :many
SELECT * FROM traffic_gaps WHERE end_at >= ? AND start_at < ? ORDER BY start_at, stack_id;

-- name: DeleteTrafficGapsBefore :exec
DELETE FROM traffic_gaps WHERE end_at < ? AND open = 0;

-- name: GetTrafficHeartbeat :one
SELECT at FROM traffic_heartbeat WHERE id = 1;

-- name: PutTrafficHeartbeat :exec
INSERT INTO traffic_heartbeat (id, at) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET at = excluded.at;
