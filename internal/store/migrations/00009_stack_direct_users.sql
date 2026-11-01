-- +goose Up
CREATE TABLE stack_direct_users (
    stack_id INTEGER NOT NULL REFERENCES stacks(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    PRIMARY KEY (stack_id, user_id)
);

-- +goose Down
DROP TABLE stack_direct_users;
