-- +goose Up
CREATE TABLE manual_nodes (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    data     TEXT    NOT NULL
);

CREATE TABLE remote_sources (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    position INTEGER NOT NULL,
    name     TEXT    NOT NULL UNIQUE,
    url      TEXT    NOT NULL,
    token    TEXT    NOT NULL,
    enabled  INTEGER NOT NULL
);

-- Runtime state of a remote source: the nodes fetched by the last successful sync.
CREATE TABLE remote_source_state (
    source_id    INTEGER PRIMARY KEY REFERENCES remote_sources (id) ON DELETE CASCADE,
    last_sync_at INTEGER NOT NULL,
    last_error   TEXT    NOT NULL,
    node_count   INTEGER NOT NULL,
    nodes        TEXT    NOT NULL
);

CREATE TABLE sub_templates (
    format  TEXT PRIMARY KEY,
    content TEXT NOT NULL
);

-- +goose Down
DROP TABLE sub_templates;
DROP TABLE remote_source_state;
DROP TABLE remote_sources;
DROP TABLE manual_nodes;
