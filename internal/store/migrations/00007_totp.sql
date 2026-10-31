-- +goose Up
-- totp_pending_secret holds a secret during enrollment until the first code confirms it.
-- totp_last_step is the last accepted time step; codes at or before it are rejected as replays.
ALTER TABLE admins ADD COLUMN totp_pending_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE admins ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE admins DROP COLUMN totp_last_step;
ALTER TABLE admins DROP COLUMN totp_pending_secret;
