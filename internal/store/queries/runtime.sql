-- name: ListUserPausedStacks :many
SELECT * FROM user_paused_stacks ORDER BY user_id, stack_id;

-- name: DeleteUserPausedStacks :exec
DELETE FROM user_paused_stacks;

-- name: InsertUserPausedStack :exec
INSERT INTO user_paused_stacks (user_id, stack_id) VALUES (?, ?);

-- name: ListAppliedState :many
SELECT * FROM applied_state ORDER BY stack_name;

-- name: UpsertAppliedState :exec
INSERT INTO applied_state (stack_name, revision_id, xray_sha, mihomo_sha, applied_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (stack_name) DO UPDATE SET
    revision_id = excluded.revision_id, xray_sha = excluded.xray_sha,
    mihomo_sha = excluded.mihomo_sha, applied_at = excluded.applied_at;

-- name: UpdateAppliedHashes :exec
UPDATE applied_state SET xray_sha = ?, mihomo_sha = ? WHERE stack_name = ?;

-- name: DeleteAppliedState :exec
DELETE FROM applied_state WHERE stack_name = ?;

-- name: InsertTask :one
INSERT INTO tasks (kind, title, actor, status, started_at) VALUES (?, ?, ?, 'running', ?)
RETURNING id;

-- name: FinishTask :exec
UPDATE tasks SET status = ?, finished_at = ?, log = ?, error = ? WHERE id = ?;

-- name: GetTask :one
SELECT * FROM tasks WHERE id = ?;

-- name: ListTasks :many
SELECT * FROM tasks ORDER BY id DESC LIMIT ?;

-- name: MarkInterruptedTasks :exec
UPDATE tasks SET status = 'failed', error = 'interrupted by service restart', finished_at = ?
WHERE status = 'running';

-- name: InsertDepInstall :exec
INSERT INTO dep_installs (component, version, platform, source, sha256, actor, started_at, finished_at, result, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListDepInstalls :many
SELECT * FROM dep_installs WHERE component = ? ORDER BY id DESC LIMIT ?;

-- name: GetConfigRevisionSnapshot :one
SELECT snapshot FROM config_revisions WHERE id = ?;
