-- +goose Up
CREATE TABLE user_paused_stacks (
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    stack_id INTEGER NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, stack_id)
);

-- One row per stack name whose generated files were written by apply.
-- revision_id is the config revision the files were rendered from.
CREATE TABLE applied_state (
    stack_name  TEXT    PRIMARY KEY,
    revision_id INTEGER NOT NULL,
    xray_sha    TEXT    NOT NULL,
    mihomo_sha  TEXT    NOT NULL,
    applied_at  INTEGER NOT NULL
);

CREATE TABLE tasks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT    NOT NULL,
    title       TEXT    NOT NULL,
    actor       TEXT    NOT NULL,
    status      TEXT    NOT NULL,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0,
    log         TEXT    NOT NULL DEFAULT '',
    error       TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE dep_installs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    component   TEXT    NOT NULL,
    version     TEXT    NOT NULL,
    platform    TEXT    NOT NULL,
    source      TEXT    NOT NULL,
    sha256      TEXT    NOT NULL,
    actor       TEXT    NOT NULL,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL,
    result      TEXT    NOT NULL,
    error       TEXT    NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE dep_installs;
DROP TABLE tasks;
DROP TABLE applied_state;
DROP TABLE user_paused_stacks;
