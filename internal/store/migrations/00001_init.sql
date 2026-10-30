-- +goose Up
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE admins (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    username             TEXT    NOT NULL UNIQUE,
    password_hash        TEXT    NOT NULL,
    must_change_password INTEGER NOT NULL DEFAULT 1,
    totp_secret          TEXT    NOT NULL DEFAULT '',
    failed_logins        INTEGER NOT NULL DEFAULT 0,
    locked_until         INTEGER NOT NULL DEFAULT 0,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
);

CREATE TABLE sessions (
    id_hash      TEXT    PRIMARY KEY,
    admin_id     INTEGER NOT NULL REFERENCES admins (id) ON DELETE CASCADE,
    csrf_token   TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    ip           TEXT    NOT NULL DEFAULT '',
    user_agent   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX sessions_admin_id ON sessions (admin_id);

CREATE TABLE admin_reset_tokens (
    token_hash TEXT    PRIMARY KEY,
    admin_id   INTEGER NOT NULL REFERENCES admins (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE audit_logs (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    at        INTEGER NOT NULL,
    actor     TEXT    NOT NULL,
    source_ip TEXT    NOT NULL DEFAULT '',
    action    TEXT    NOT NULL,
    object    TEXT    NOT NULL DEFAULT '',
    summary   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX audit_logs_at ON audit_logs (at);

-- +goose Down
DROP TABLE audit_logs;
DROP TABLE admin_reset_tokens;
DROP TABLE sessions;
DROP TABLE admins;
DROP TABLE settings;
