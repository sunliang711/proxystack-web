-- +goose Up
-- 用户访问的主域名按天计数。day 是基本设置时区下的日期（YYYY-MM-DD），count 是连接次数。
-- user_id 没有外键：删除用户后历史记录保留，名称取自 traffic_names。
CREATE TABLE access_domain_day (
    user_id INTEGER NOT NULL,
    day     TEXT    NOT NULL,
    domain  TEXT    NOT NULL,
    count   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day, domain)
) WITHOUT ROWID;
-- 按日期范围汇总全部用户（主域名排行）和按域名反查用户各用一个索引，都带上 user_id 和 count，查询不用回表。
CREATE INDEX access_domain_day_day ON access_domain_day (day, domain, user_id, count);
CREATE INDEX access_domain_day_domain ON access_domain_day (domain, day, user_id, count);

-- 访问日志的读取位置（journald 书签），与计数在同一事务中写入。
CREATE TABLE access_cursor (
    id     INTEGER PRIMARY KEY CHECK (id = 1),
    cursor TEXT    NOT NULL,
    at     INTEGER NOT NULL
);

-- +goose Down
DROP TABLE access_cursor;
DROP TABLE access_domain_day;
