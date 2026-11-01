-- +goose Up
ALTER TABLE stacks ADD COLUMN direct_suffix TEXT NOT NULL DEFAULT ' [DRT]';

-- +goose Down
ALTER TABLE stacks DROP COLUMN direct_suffix;
