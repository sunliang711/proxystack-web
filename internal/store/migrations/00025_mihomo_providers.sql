-- +goose Up
CREATE TABLE mihomo_providers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    stack_id INTEGER NOT NULL REFERENCES stacks(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    config TEXT NOT NULL
);
ALTER TABLE mihomo_groups ADD COLUMN provider_use TEXT NOT NULL DEFAULT '[]';
CREATE TABLE mihomo_provider_state (
    provider_id INTEGER PRIMARY KEY REFERENCES mihomo_providers(id) ON DELETE CASCADE,
    config_hash TEXT NOT NULL DEFAULT '',
    last_attempt_at INTEGER NOT NULL DEFAULT 0,
    last_success_at INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE mihomo_provider_state;
ALTER TABLE mihomo_groups DROP COLUMN provider_use;
DROP TABLE mihomo_providers;
