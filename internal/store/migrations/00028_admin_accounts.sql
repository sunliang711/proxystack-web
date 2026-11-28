-- +goose Up
ALTER TABLE admins ADD COLUMN role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'viewer'));
ALTER TABLE admins ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1));
ALTER TABLE admins ADD COLUMN last_login_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE admins DROP COLUMN last_login_at;
ALTER TABLE admins DROP COLUMN disabled;
ALTER TABLE admins DROP COLUMN role;
