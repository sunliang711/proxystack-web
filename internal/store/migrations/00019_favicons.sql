-- +goose Up
-- 网站图标缓存，按主域名存放。ok 为 0 表示没取到图标（data 为空）。fetched_at 是最近一次获取的时间，
-- expires_at 之前直接使用本行（取到的图标、或「没有图标」的结论），之后访问时重新获取。
CREATE TABLE favicons (
    domain       TEXT    PRIMARY KEY,
    ok           INTEGER NOT NULL,
    content_type TEXT    NOT NULL DEFAULT '',
    data         BLOB    NOT NULL DEFAULT x'',
    fetched_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
) WITHOUT ROWID;

-- +goose Down
DROP TABLE favicons;
