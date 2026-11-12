-- +goose Up
-- 每日停服时段：inherit 跟随全局设置，custom 使用本行的 pause_windows（逗号分隔的 HH:MM-HH:MM），off 不限制。
ALTER TABLE users ADD COLUMN pause_window_mode TEXT NOT NULL DEFAULT 'inherit';
ALTER TABLE users ADD COLUMN pause_windows TEXT NOT NULL DEFAULT '';

-- 管理员放行本次停服时段：until 之前不按停服时段暂停该用户。
CREATE TABLE user_window_exempt (
    user_id INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    until   INTEGER NOT NULL
);

-- +goose Down
DROP TABLE user_window_exempt;
ALTER TABLE users DROP COLUMN pause_windows;
ALTER TABLE users DROP COLUMN pause_window_mode;
