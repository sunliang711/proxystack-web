-- +goose Up
ALTER TABLE stack_auto_stop ADD COLUMN restore_xray INTEGER NOT NULL DEFAULT 0;
ALTER TABLE stack_auto_stop ADD COLUMN restore_mihomo INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE stack_auto_stop DROP COLUMN restore_mihomo;
ALTER TABLE stack_auto_stop DROP COLUMN restore_xray;
