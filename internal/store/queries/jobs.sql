-- name: ListJobs :many
SELECT * FROM jobs ORDER BY id;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: InsertJob :exec
INSERT OR IGNORE INTO jobs (kind, schedule, enabled, timeout, params) VALUES (?, ?, ?, ?, ?);

-- name: UpdateJob :exec
UPDATE jobs SET schedule = ?, enabled = ?, timeout = ?, params = ? WHERE id = ?;

-- name: InsertJobRun :one
INSERT INTO job_runs (job_id, started_at, finished_at, status, trigger_by, log)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: FinishJobRun :exec
UPDATE job_runs SET finished_at = ?, status = ?, log = ? WHERE id = ?;

-- name: MarkInterruptedJobRuns :exec
UPDATE job_runs SET finished_at = ?, status = 'failed', log = log || 'interrupted by service restart' || char(10) WHERE status = 'running';

-- name: ListJobRuns :many
SELECT * FROM job_runs WHERE job_id = ? ORDER BY id DESC LIMIT ?;

-- name: LatestJobRuns :many
SELECT * FROM job_runs WHERE id IN (SELECT MAX(id) FROM job_runs GROUP BY job_id);

-- name: PruneJobRuns :exec
DELETE FROM job_runs
WHERE job_runs.job_id = sqlc.arg(job) AND job_runs.id NOT IN (
    SELECT recent.id FROM job_runs AS recent WHERE recent.job_id = sqlc.arg(job) ORDER BY recent.id DESC LIMIT sqlc.arg(keep)
);
