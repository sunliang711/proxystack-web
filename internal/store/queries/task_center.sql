-- name: ListTaskCenter :many
WITH entries AS (
    SELECT 'task' AS source, id, kind, title, actor, status, started_at, finished_at, log, error, 0 AS job_id FROM tasks
    UNION ALL
    SELECT 'job' AS source, r.id, j.kind, j.kind AS title, r.trigger_by AS actor, r.status, r.started_at, r.finished_at, r.log, '' AS error, j.id AS job_id
    FROM job_runs r JOIN jobs j ON j.id = r.job_id
)
SELECT * FROM entries
WHERE (CAST(sqlc.arg(source) AS TEXT) = 'all' OR source = sqlc.arg(source))
  AND (CAST(sqlc.arg(status) AS TEXT) = 'all' OR status = sqlc.arg(status))
  AND (CAST(sqlc.arg(kind) AS TEXT) = 'all' OR kind = sqlc.arg(kind))
ORDER BY (status = 'running') DESC, started_at DESC, source, id DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountTaskCenter :one
WITH entries AS (
    SELECT 'task' AS source, kind, status FROM tasks
    UNION ALL
    SELECT 'job' AS source, j.kind, r.status FROM job_runs r JOIN jobs j ON j.id = r.job_id
)
SELECT COUNT(*) FROM entries
WHERE (CAST(sqlc.arg(source) AS TEXT) = 'all' OR source = sqlc.arg(source))
  AND (CAST(sqlc.arg(status) AS TEXT) = 'all' OR status = sqlc.arg(status))
  AND (CAST(sqlc.arg(kind) AS TEXT) = 'all' OR kind = sqlc.arg(kind));

-- name: GetTaskCenterJob :one
SELECT r.*, j.kind FROM job_runs r JOIN jobs j ON j.id = r.job_id WHERE r.id = ?;
