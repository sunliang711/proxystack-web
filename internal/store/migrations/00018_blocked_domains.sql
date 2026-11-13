-- +goose Up
-- 用户域名黑名单：逗号分隔的条目，example.com 拦截该域名及全部子域名，full:www.example.com 只拦截该域名。
ALTER TABLE users ADD COLUMN blocked_domains TEXT NOT NULL DEFAULT '';

-- 访问统计区分放行与拦截：count 是放行的连接次数，blocked 是被域名黑名单拦截的连接次数。
ALTER TABLE access_domain_day ADD COLUMN blocked INTEGER NOT NULL DEFAULT 0;
DROP INDEX access_domain_day_day;
DROP INDEX access_domain_day_domain;
CREATE INDEX access_domain_day_day ON access_domain_day (day, domain, user_id, count, blocked);
CREATE INDEX access_domain_day_domain ON access_domain_day (domain, day, user_id, count, blocked);

-- +goose Down
DROP INDEX access_domain_day_domain;
DROP INDEX access_domain_day_day;
CREATE INDEX access_domain_day_day ON access_domain_day (day, domain, user_id, count);
CREATE INDEX access_domain_day_domain ON access_domain_day (domain, day, user_id, count);
ALTER TABLE access_domain_day DROP COLUMN blocked;
ALTER TABLE users DROP COLUMN blocked_domains;
