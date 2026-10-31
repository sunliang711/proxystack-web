-- +goose Up
ALTER TABLE users ADD COLUMN daily_limit_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN daily_limit_action TEXT NOT NULL DEFAULT 'pause';

-- Traffic detail. scope is user, inbound or outbound; subject is the user ID ("12" or "12.3" with
-- a profile, "unknown" when the counter maps to no current user) or the inbound/outbound tag.
-- stack_id has no foreign key: history outlives deleted stacks.
CREATE TABLE traffic_minute (
    bucket_start INTEGER NOT NULL,
    stack_id     INTEGER NOT NULL,
    scope        TEXT    NOT NULL,
    subject      TEXT    NOT NULL,
    up           INTEGER NOT NULL DEFAULT 0,
    down         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket_start, stack_id, scope, subject)
) WITHOUT ROWID;
CREATE TABLE traffic_hour (
    bucket_start INTEGER NOT NULL,
    stack_id     INTEGER NOT NULL,
    scope        TEXT    NOT NULL,
    subject      TEXT    NOT NULL,
    up           INTEGER NOT NULL DEFAULT 0,
    down         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket_start, stack_id, scope, subject)
) WITHOUT ROWID;
CREATE TABLE traffic_day (
    bucket_start INTEGER NOT NULL,
    stack_id     INTEGER NOT NULL,
    scope        TEXT    NOT NULL,
    subject      TEXT    NOT NULL,
    up           INTEGER NOT NULL DEFAULT 0,
    down         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket_start, stack_id, scope, subject)
) WITHOUT ROWID;
CREATE TABLE traffic_month (
    bucket_start INTEGER NOT NULL,
    stack_id     INTEGER NOT NULL,
    scope        TEXT    NOT NULL,
    subject      TEXT    NOT NULL,
    up           INTEGER NOT NULL DEFAULT 0,
    down         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket_start, stack_id, scope, subject)
) WITHOUT ROWID;

-- Usage in the current billing period, for quota checks.
CREATE TABLE user_usage (
    user_id      INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    period_start INTEGER NOT NULL,
    up           INTEGER NOT NULL DEFAULT 0,
    down         INTEGER NOT NULL DEFAULT 0
);

-- Usage per calendar day in the configured time zone. exempt means an administrator lifted the
-- daily pause: the user is not paused for the daily limit again that day.
CREATE TABLE user_daily_usage (
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day     TEXT    NOT NULL,
    up      INTEGER NOT NULL DEFAULT 0,
    down    INTEGER NOT NULL DEFAULT 0,
    exempt  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);

-- Automatic pauses apply to every stack the user is on, including stacks created later.
-- reason is daily, quota or expired.
CREATE TABLE user_auto_pause (
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reason  TEXT    NOT NULL,
    since   INTEGER NOT NULL,
    PRIMARY KEY (user_id, reason)
);

-- Email log and send queue. dedup_key stops the same event being recorded twice in its period.
-- user_id is not a foreign key: records outlive deleted users.
CREATE TABLE notifications (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    at              INTEGER NOT NULL,
    event           TEXT    NOT NULL,
    user_id         INTEGER NOT NULL DEFAULT 0,
    user_name       TEXT    NOT NULL DEFAULT '',
    dedup_key       TEXT    NOT NULL,
    subject         TEXT    NOT NULL,
    body            TEXT    NOT NULL,
    status          TEXT    NOT NULL,
    error           TEXT    NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX notifications_dedup ON notifications (dedup_key);
CREATE INDEX notifications_queue ON notifications (status, next_attempt_at);

-- +goose Down
DROP TABLE notifications;
DROP TABLE user_auto_pause;
DROP TABLE user_daily_usage;
DROP TABLE user_usage;
DROP TABLE traffic_month;
DROP TABLE traffic_day;
DROP TABLE traffic_hour;
DROP TABLE traffic_minute;
ALTER TABLE users DROP COLUMN daily_limit_action;
ALTER TABLE users DROP COLUMN daily_limit_bytes;
