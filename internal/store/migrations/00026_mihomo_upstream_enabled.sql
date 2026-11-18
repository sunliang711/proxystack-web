-- +goose Up
ALTER TABLE mihomo_upstreams ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE mihomo_upstreams DROP COLUMN enabled;
