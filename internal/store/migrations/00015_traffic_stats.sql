-- +goose Up
-- Last known name of every user and stack, kept after deletion so historical traffic stays labelled.
-- role is the stack role (edge / auto); auto stacks are left out of cross-stack totals.
CREATE TABLE traffic_names (
    kind TEXT    NOT NULL,
    id   INTEGER NOT NULL,
    name TEXT    NOT NULL,
    role TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (kind, id)
) WITHOUT ROWID;
INSERT INTO traffic_names (kind, id, name, role) SELECT 'user', id, name, '' FROM users;
INSERT INTO traffic_names (kind, id, name, role) SELECT 'stack', id, name, role FROM stacks;

-- Periods with incomplete traffic data. stack_id 0 means the whole panel.
-- kind is lost (counters reset before they were collected) or delayed (counted later than it happened).
-- open marks a period that has not ended yet; end_at follows the latest check.
CREATE TABLE traffic_gaps (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    stack_id INTEGER NOT NULL,
    kind     TEXT    NOT NULL,
    reason   TEXT    NOT NULL,
    start_at INTEGER NOT NULL,
    end_at   INTEGER NOT NULL,
    open     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX traffic_gaps_end ON traffic_gaps (end_at);

-- Time of the latest successful traffic write, used to detect downtime of the panel.
CREATE TABLE traffic_heartbeat (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE traffic_heartbeat;
DROP TABLE traffic_gaps;
DROP TABLE traffic_names;
