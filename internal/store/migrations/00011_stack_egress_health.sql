-- +goose Up
CREATE TABLE stack_egress_health (
    stack_id INTEGER PRIMARY KEY REFERENCES stacks(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'unknown',
    checked_at INTEGER NOT NULL DEFAULT 0,
    failures INTEGER NOT NULL DEFAULT 0,
    failure_since INTEGER NOT NULL DEFAULT 0,
    incident INTEGER NOT NULL DEFAULT 0,
    alarmed INTEGER NOT NULL DEFAULT 0,
    fingerprint TEXT NOT NULL DEFAULT '',
    ipv4 TEXT NOT NULL DEFAULT '',
    ipv6 TEXT NOT NULL DEFAULT '',
    ipv4_error TEXT NOT NULL DEFAULT '',
    ipv6_error TEXT NOT NULL DEFAULT ''
);
ALTER TABLE notifications ADD COLUMN stack_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notifications ADD COLUMN stack_name TEXT NOT NULL DEFAULT '';
ALTER TABLE notifications ADD COLUMN incident INTEGER NOT NULL DEFAULT 0;
CREATE INDEX notifications_stack ON notifications(stack_id, status);

-- +goose Down
DROP INDEX notifications_stack;
ALTER TABLE notifications DROP COLUMN incident;
ALTER TABLE notifications DROP COLUMN stack_name;
ALTER TABLE notifications DROP COLUMN stack_id;
DROP TABLE stack_egress_health;
