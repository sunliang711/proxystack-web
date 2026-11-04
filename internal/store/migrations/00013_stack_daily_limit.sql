-- +goose Up
ALTER TABLE stacks ADD COLUMN daily_limit_bytes INTEGER NOT NULL DEFAULT 0;

-- 自动停止记录按 Stack 和自然日保存；跨日后判定逻辑会自动清除。
CREATE TABLE stack_auto_stop (
    stack_id INTEGER PRIMARY KEY REFERENCES stacks(id) ON DELETE CASCADE,
    day      TEXT    NOT NULL,
    since    INTEGER NOT NULL
);

-- +goose Down
DROP TABLE stack_auto_stop;
ALTER TABLE stacks DROP COLUMN daily_limit_bytes;
