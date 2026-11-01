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
