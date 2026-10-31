-- +goose Up
-- One row per job kind; kinds are defined in code and rows are created on first start.
CREATE TABLE jobs (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    kind     TEXT    NOT NULL UNIQUE,
    schedule TEXT    NOT NULL,
    enabled  INTEGER NOT NULL DEFAULT 1,
    timeout  INTEGER NOT NULL,
    params   TEXT    NOT NULL DEFAULT '{}'
);

-- status is running, succeeded, failed, skipped or timeout.
CREATE TABLE job_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id      INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0,
    status      TEXT    NOT NULL,
    trigger_by  TEXT    NOT NULL DEFAULT 'schedule',
    log         TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX job_runs_job ON job_runs (job_id, id);

-- +goose Down
DROP TABLE job_runs;
DROP TABLE jobs;
