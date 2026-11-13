-- +goose Up
-- 管理员手动设置的网站图标，优先于自动获取的图标，不过期也不被清理。kind 是 domain（主域名）或 service（服务名，小写），
-- name 是主域名或服务名；updated_by 是最后设置它的管理员。
CREATE TABLE manual_icons (
    kind         TEXT    NOT NULL,
    name         TEXT    NOT NULL,
    content_type TEXT    NOT NULL,
    data         BLOB    NOT NULL,
    updated_at   INTEGER NOT NULL,
    updated_by   TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (kind, name)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE manual_icons;
