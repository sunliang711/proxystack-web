-- +goose Up
ALTER TABLE stacks ADD COLUMN xray_access_log TEXT NOT NULL DEFAULT 'inherit' CHECK (xray_access_log IN ('inherit', 'on', 'off'));

-- +goose Down
ALTER TABLE stacks DROP COLUMN xray_access_log;
