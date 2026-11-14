-- +goose Up
CREATE TABLE stack_direct_overrides (
    stack_id INTEGER NOT NULL REFERENCES stacks(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    upstream TEXT NOT NULL,
    position INTEGER NOT NULL,
    config TEXT NOT NULL,
    PRIMARY KEY (stack_id, user_id, upstream)
);

-- +goose Down
DROP TABLE stack_direct_overrides;
