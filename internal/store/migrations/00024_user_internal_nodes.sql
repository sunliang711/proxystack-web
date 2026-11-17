-- +goose Up
ALTER TABLE users ADD COLUMN internal_nodes INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN internal_nodes;
